package webhook

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/IDEA-Amrita/paystable/internal/config"
	"github.com/IDEA-Amrita/paystable/internal/gateway/payu"
	"github.com/IDEA-Amrita/paystable/internal/gateway/razorpay"
	"github.com/IDEA-Amrita/paystable/internal/metrics"
	"github.com/IDEA-Amrita/paystable/internal/secrets"
)

type Handler struct {
	db  *sql.DB
	cfg *config.Config
}

func NewHandler(db *sql.DB, cfg *config.Config) *Handler {
	return &Handler{db: db, cfg: cfg}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	gateway := r.PathValue("gateway")
	if gateway == "" {
		http.Error(w, "missing gateway", http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}

	params, err := parseGatewayPayload(gateway, body, r.Header.Get("Content-Type"))
	if err != nil {
		h.quarantine(gateway, "malformed_payload", r, body)
		w.WriteHeader(http.StatusOK)
		return
	}

	if !h.verify(r.Context(), gateway, params, body, r.Header) {
		h.quarantine(gateway, "hmac_mismatch", r, body)
		metrics.WebhookHMACFailures.Inc()
		w.WriteHeader(http.StatusOK)
		return
	}
	if gateway == "razorpay" && r.Header.Get("X-Razorpay-Event-Id") == "" {
		h.quarantine(gateway, "missing_event_id", r, body)
		w.WriteHeader(http.StatusOK)
		return
	}

	// Timestamp replay protection
	if ts := params["timestamp"]; ts != "" {
		if t, err := strconv.ParseInt(ts, 10, 64); err == nil {
			age := time.Since(time.Unix(t, 0))
			if age > 5*time.Minute {
				h.quarantine(gateway, "replay_attack", r, body)
				w.WriteHeader(http.StatusOK)
				return
			}
		}
	}

	if err := h.persist(gateway, params, body, r.Header.Get("X-Razorpay-Event-Id")); err != nil {
		slog.Error("failed to persist webhook", "error", err, "gateway", gateway)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) verify(ctx context.Context, gateway string, params map[string]string, body []byte, headers http.Header) bool {
	candidates := h.activeSecrets(ctx, gateway)

	switch gateway {
	case "payu":
		if len(candidates) == 0 && h.cfg.WebhookSecret != "" {
			candidates = append(candidates, h.cfg.WebhookSecret)
		}
		for _, secret := range candidates {
			if payu.VerifyResponseHash(params, secret) {
				return true
			}
		}
		return false
	case "razorpay":
		if len(candidates) == 0 && h.cfg.RazorpayWebhookSecret != "" {
			candidates = append(candidates, h.cfg.RazorpayWebhookSecret)
		}
		for _, secret := range candidates {
			if razorpay.VerifyWebhookSignature(body, headers.Get("X-Razorpay-Signature"), secret) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func (h *Handler) activeSecrets(ctx context.Context, gateway string) []string {
	if h.cfg.SecretEncryptionKey == "" {
		return nil
	}
	key, err := secrets.ParseKey(h.cfg.SecretEncryptionKey)
	if err != nil {
		slog.Error("webhook secret key invalid", "error", err)
		return nil
	}
	rows, err := h.db.QueryContext(ctx, `
		SELECT secret_encrypted
		FROM gateway_secrets
		WHERE gateway=$1 AND is_active=true
		  AND (rotation_window_end IS NULL OR rotation_window_end > now())
		ORDER BY created_at DESC`, gateway)
	if err != nil {
		slog.Error("load active gateway secrets failed", "error", err, "gateway", gateway)
		return nil
	}
	defer func() {
		_ = rows.Close()
	}()

	var out []string
	for rows.Next() {
		var ciphertext []byte
		if err := rows.Scan(&ciphertext); err != nil {
			continue
		}
		plain, err := secrets.Decrypt(ciphertext, key)
		if err != nil {
			slog.Error("decrypt gateway secret failed", "error", err, "gateway", gateway)
			continue
		}
		out = append(out, string(plain))
	}
	return out
}

func (h *Handler) persist(gateway string, params map[string]string, body []byte, eventID string) error {
	txnID := extractTxnID(gateway, params)
	eventType := extractEventType(gateway, params)
	if eventID == "" {
		eventID = params["mihpayid"]
	}

	payload := body
	if !json.Valid(payload) {
		payload, _ = json.Marshal(params)
	}

	var insertedID int64
	err := h.db.QueryRow(`
		INSERT INTO webhooks (txn_id, gateway, gateway_event_id, event_type, payload)
		VALUES ($1, $2, NULLIF($3, ''), $4, $5::jsonb)
		ON CONFLICT (gateway, gateway_event_id) DO NOTHING
		RETURNING id`,
		txnID, gateway, eventID, eventType, payload).Scan(&insertedID)

	if err == sql.ErrNoRows {
		slog.Info("duplicate webhook ignored", "gateway", gateway, "txn_id", txnID, "event", eventType)
		return nil
	}
	if err != nil {
		return err
	}

	if _, err := h.db.Exec(`INSERT INTO verification_polls (txn_id, attempt_number, scheduled_at, status)
		SELECT $1, 1, now(), 'pending' WHERE EXISTS (SELECT 1 FROM holds WHERE txn_id = $1)`, txnID); err != nil {
		slog.Warn("enqueue verification poll after webhook failed", "txn_id", txnID, "error", err)
	}

	slog.Info("webhook persisted", "gateway", gateway, "txn_id", txnID, "event", eventType)
	return nil
}

func (h *Handler) quarantine(gateway, reason string, r *http.Request, body []byte) {
	headers, _ := json.Marshal(r.Header)
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)

	_, err := h.db.Exec(`
		INSERT INTO webhooks_rejected (gateway, rejection_reason, headers, raw_body, source_ip)
		VALUES ($1, $2, $3::jsonb, $4, $5::inet)`,
		gateway, reason, headers, body, ip)

	if err != nil {
		slog.Error("failed to quarantine webhook", "error", err, "gateway", gateway)
	} else {
		slog.Warn("webhook quarantined", "gateway", gateway, "reason", reason)
	}
}

func extractTxnID(gateway string, params map[string]string) string {
	switch gateway {
	case "payu":
		return params["txnid"]
	case "razorpay":
		if params["order_id"] != "" {
			return params["order_id"]
		}
		return params["payment_id"]
	default:
		return params["txnid"]
	}
}

func extractEventType(gateway string, params map[string]string) string {
	switch gateway {
	case "payu":
		return "payment." + params["status"]
	case "razorpay":
		return params["event"]
	default:
		return "unknown"
	}
}

func parseGatewayPayload(gateway string, body []byte, contentType string) (map[string]string, error) {
	if gateway == "razorpay" {
		return parseRazorpayPayload(body)
	}
	return parsePayload(body, contentType)
}

func parseRazorpayPayload(body []byte) (map[string]string, error) {
	var event struct {
		Event   string `json:"event"`
		Payload struct {
			Payment struct {
				Entity struct {
					ID      string `json:"id"`
					OrderID string `json:"order_id"`
					Status  string `json:"status"`
					Amount  int64  `json:"amount"`
				} `json:"entity"`
			} `json:"payment"`
			Order struct {
				Entity struct {
					ID string `json:"id"`
				} `json:"entity"`
			} `json:"order"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(body, &event); err != nil {
		return nil, err
	}
	if event.Event == "" {
		return nil, fmt.Errorf("missing Razorpay event type")
	}
	orderID := event.Payload.Payment.Entity.OrderID
	if orderID == "" {
		orderID = event.Payload.Order.Entity.ID
	}
	if orderID == "" {
		return nil, fmt.Errorf("missing Razorpay order id")
	}
	return map[string]string{
		"event":      event.Event,
		"payment_id": event.Payload.Payment.Entity.ID,
		"order_id":   orderID,
		"status":     event.Payload.Payment.Entity.Status,
		"amount":     strconv.FormatInt(event.Payload.Payment.Entity.Amount, 10),
	}, nil
}

func parsePayload(body []byte, contentType string) (map[string]string, error) {
	params := make(map[string]string)

	if strings.HasPrefix(contentType, "application/json") || (len(body) > 0 && body[0] == '{') {
		if err := json.Unmarshal(body, &params); err != nil {
			return nil, err
		}
		return params, nil
	}

	//payu sends application/x-www-form-urlencoded
	values, err := url.ParseQuery(string(body))
	if err != nil {
		return nil, err
	}
	for k, v := range values {
		if len(v) > 0 {
			params[k] = v[0]
		}
	}
	return params, nil
}
