package webhook

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/IDEA-Amrita/paystable/internal/config"
	gatewaypkg "github.com/IDEA-Amrita/paystable/internal/gateway"
	"github.com/IDEA-Amrita/paystable/internal/gateway/adapters"
	"github.com/IDEA-Amrita/paystable/internal/gateway/payu"
	"github.com/IDEA-Amrita/paystable/internal/metrics"
	"github.com/IDEA-Amrita/paystable/internal/secrets"
)

type Handler struct {
	db       *sql.DB
	cfg      *config.Config
	adapters map[string]gatewaypkg.Adapter
}

func NewHandler(db *sql.DB, cfg *config.Config) *Handler {
	return &Handler{db: db, cfg: cfg, adapters: adapters.New(cfg)}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	gateway := r.PathValue("gateway")
	if gateway == "" {
		http.Error(w, "missing gateway", http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}

	if len(body) > 1<<20 {
		http.Error(w, "webhook body exceeds the size limit", http.StatusRequestEntityTooLarge)
		return
	}
	adapter := h.adapters[gateway]
	if adapter == nil || (h.cfg.Gateway != "" && gateway != h.cfg.Gateway) {
		http.Error(w, "gateway is not active", http.StatusBadRequest)
		return
	}
	if err := h.verify(r.Context(), gateway, adapter, body, r.Header); err != nil {
		reason := "hmac_mismatch"
		if errors.Is(err, gatewaypkg.ErrMalformedWebhook) {
			reason = "malformed_payload"
		} else {
			metrics.WebhookHMACFailures.Inc()
		}
		h.quarantine(gateway, reason, r, body)
		w.WriteHeader(http.StatusOK)
		return
	}
	event, err := adapter.ParseWebhook(body, r.Header)
	if err != nil {
		h.quarantine(gateway, "malformed_payload", r, body)
		w.WriteHeader(http.StatusOK)
		return
	}
	if event.Timestamp != 0 && time.Since(time.Unix(event.Timestamp, 0)) > 5*time.Minute {
		h.quarantine(gateway, "replay_attack", r, body)
		w.WriteHeader(http.StatusOK)
		return
	}
	if err := h.persist(gateway, event); err != nil {
		slog.Error("failed to persist webhook", "error", err, "gateway", gateway)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) verify(ctx context.Context, name string, adapter gatewaypkg.Adapter, body []byte, headers http.Header) error {
	candidates := h.activeSecrets(ctx, name)
	if len(candidates) == 0 && h.cfg.WebhookSecret != "" {
		candidates = append(candidates, h.cfg.WebhookSecret)
	}
	for _, secret := range candidates {
		err := adapter.VerifyWebhook(body, headers, secret)
		if err == nil || errors.Is(err, gatewaypkg.ErrMalformedWebhook) {
			return err
		}
	}
	return gatewaypkg.ErrSignature
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

func (h *Handler) persist(name string, event gatewaypkg.Webhook) error {
	tx, err := h.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, event.TxnID); err != nil {
		return err
	}
	var insertedID int64
	err = tx.QueryRow(`INSERT INTO webhooks (txn_id,gateway,gateway_event_id,event_type,payload,actionable)
	 VALUES ($1,$2,NULLIF($3,''),$4,$5::jsonb,$6)
	 ON CONFLICT (gateway,gateway_event_id) DO NOTHING RETURNING id`, event.TxnID, name, event.EventID, event.EventType, event.Payload, event.Actionable).Scan(&insertedID)
	if err == sql.ErrNoRows {
		return tx.Commit()
	}
	if err != nil {
		return err
	}
	if event.Actionable {
		_, err = tx.Exec(`INSERT INTO verification_polls (txn_id,attempt_number,scheduled_at,status)
		 SELECT $1,1,now(),'pending' WHERE EXISTS (SELECT 1 FROM holds WHERE txn_id=$1 AND gateway=$2)`, event.TxnID, name)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
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

func parsePayload(body []byte, contentType string) (map[string]string, error) {
	return payu.ParsePayload(body, contentType)
}
