package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/IDEA-Amrita/paystable/internal/delivery"
	"github.com/IDEA-Amrita/paystable/internal/gateway/razorpay"
)

const checkoutPage = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Paystable test checkout</title></head>
<body>
  <main>
    <h1>Paystable test checkout</h1>
    <p>This page uses Razorpay Test Mode. It does not use real money.</p>
    <button id="pay">Create test payment</button>
    <pre id="result" aria-live="polite"></pre>
  </main>
  <script src="https://checkout.razorpay.com/v1/checkout.js"></script>
  <script>
    const result = document.getElementById('result');
    document.getElementById('pay').addEventListener('click', async () => {
      result.textContent = 'Creating the test order...';
      const response = await fetch('/orders', {method: 'POST'});
      const order = await response.json();
      if (!response.ok) { result.textContent = order.error || 'Could not create the test order.'; return; }
      new Razorpay({
        key: order.key_id,
        amount: order.amount,
        currency: order.currency,
        name: 'Paystable test merchant',
        description: 'Test payment',
        order_id: order.order_id,
        handler: async payment => {
          const verified = await fetch('/checkout', {
            method: 'POST',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify(payment)
          });
          result.textContent = verified.ok
            ? 'Checkout is verified. Paystable is waiting for the webhook.'
            : 'Checkout verification failed.';
        }
      }).open();
    });
  </script>
</body>
</html>`

type app struct {
	keyID          string
	keySecret      string
	apiBaseURL     string
	paystableURL   string
	adminKey       string
	callbackURL    string
	callbackSecret string
	webhookSecret  string
	fixturePath    string
	http           *http.Client
	mu             sync.Mutex
	orders         map[string]struct{}
	effects        map[string]string
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "shadow-check" {
		if err := runShadowCheck(); err != nil {
			slog.Error("shadow check failed", "error", err)
			os.Exit(1)
		}
		return
	}

	a := &app{
		keyID:          os.Getenv("RAZORPAY_KEY_ID"),
		keySecret:      os.Getenv("RAZORPAY_KEY_SECRET"),
		apiBaseURL:     envOr("RAZORPAY_API_BASE_URL", "https://api.razorpay.com/v1"),
		paystableURL:   envOr("PAYSTABLE_URL", "http://localhost:8080"),
		adminKey:       os.Getenv("ADMIN_API_KEY"),
		callbackURL:    envOr("MERCHANT_CALLBACK_URL", "http://localhost:9092/callback"),
		callbackSecret: os.Getenv("MERCHANT_CALLBACK_SECRET"),
		webhookSecret:  os.Getenv("RAZORPAY_WEBHOOK_SECRET"),
		fixturePath:    envOr("RAZORPAY_FIXTURE_PATH", "artifacts/razorpay-webhook.json"),
		http:           &http.Client{Timeout: 10 * time.Second},
		orders:         make(map[string]struct{}),
		effects:        make(map[string]string),
	}
	if a.keyID == "" || a.keySecret == "" || a.adminKey == "" || a.callbackSecret == "" || a.webhookSecret == "" {
		slog.Error("required test merchant configuration is missing")
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", a.index)
	mux.HandleFunc("POST /orders", a.createOrder)
	mux.HandleFunc("POST /checkout", a.verifyCheckout)
	mux.HandleFunc("POST /callback", a.acceptCallback)
	mux.HandleFunc("POST /webhooks/razorpay", a.acceptWebhook)
	mux.HandleFunc("GET /effects", a.listEffects)

	port := envOr("PORT", "9092")
	slog.Info("Razorpay test merchant started", "port", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		slog.Error("Razorpay test merchant stopped", "error", err)
		os.Exit(1)
	}
}

type shadowEvent struct {
	Event        string `json:"event"`
	Status       string `json:"status"`
	Amount       int64  `json:"amount"`
	Currency     string `json:"currency"`
	HasPaymentID bool   `json:"has_payment_id"`
	HasOrderID   bool   `json:"has_order_id"`
}

type shadowReport struct {
	FixtureSignatureValid          bool        `json:"fixture_signature_valid"`
	SignedNoiseSignatureValid      bool        `json:"signed_noise_signature_valid"`
	ChangedBodySignatureRejected   bool        `json:"changed_body_signature_rejected"`
	ChangedAmountSignatureRejected bool        `json:"changed_amount_signature_rejected"`
	SanitizedEvent                 shadowEvent `json:"sanitized_event"`
}

func runShadowCheck() error {
	fixturePath := envOr("RAZORPAY_FIXTURE_PATH", "artifacts/razorpay-webhook.json")
	fixture, err := os.ReadFile(fixturePath)
	if err != nil {
		return fmt.Errorf("read the local webhook fixture: %w", err)
	}
	report, err := checkShadowFixture(fixture, os.Getenv("RAZORPAY_WEBHOOK_SECRET"))
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(report)
}

func checkShadowFixture(data []byte, secret string) (shadowReport, error) {
	var fixture struct {
		Signature  string `json:"signature"`
		BodyBase64 string `json:"body_base64"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		return shadowReport{}, fmt.Errorf("decode the local webhook fixture: %w", err)
	}
	body, err := base64.StdEncoding.DecodeString(fixture.BodyBase64)
	if err != nil {
		return shadowReport{}, fmt.Errorf("decode the webhook body: %w", err)
	}
	if !razorpay.VerifyWebhookSignature(body, fixture.Signature, secret) {
		return shadowReport{}, fmt.Errorf("the webhook fixture signature is invalid")
	}

	event, payload, err := sanitizeShadowEvent(body)
	if err != nil {
		return shadowReport{}, err
	}
	payment := payload["payload"].(map[string]any)["payment"].(map[string]any)["entity"].(map[string]any)

	payload["shadow_noise"] = map[string]any{"optional": true}
	noisyBody, err := json.Marshal(payload)
	if err != nil {
		return shadowReport{}, fmt.Errorf("encode the noisy webhook: %w", err)
	}
	noisyEvent, _, err := sanitizeShadowEvent(noisyBody)
	if err != nil {
		return shadowReport{}, err
	}

	payment["amount"] = event.Amount + 1
	tamperedBody, err := json.Marshal(payload)
	if err != nil {
		return shadowReport{}, fmt.Errorf("encode the changed webhook: %w", err)
	}

	return shadowReport{
		FixtureSignatureValid:          true,
		SignedNoiseSignatureValid:      razorpay.VerifyWebhookSignature(noisyBody, signWebhook(noisyBody, secret), secret) && noisyEvent == event,
		ChangedBodySignatureRejected:   !razorpay.VerifyWebhookSignature(noisyBody, fixture.Signature, secret),
		ChangedAmountSignatureRejected: !razorpay.VerifyWebhookSignature(tamperedBody, fixture.Signature, secret),
		SanitizedEvent:                 event,
	}, nil
}

func sanitizeShadowEvent(body []byte) (shadowEvent, map[string]any, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return shadowEvent{}, nil, fmt.Errorf("decode the webhook payload: %w", err)
	}
	payloadValue, ok := payload["payload"].(map[string]any)
	if !ok {
		return shadowEvent{}, nil, fmt.Errorf("the webhook payload is missing")
	}
	paymentValue, ok := payloadValue["payment"].(map[string]any)
	if !ok {
		return shadowEvent{}, nil, fmt.Errorf("the webhook payment is missing")
	}
	entity, ok := paymentValue["entity"].(map[string]any)
	if !ok {
		return shadowEvent{}, nil, fmt.Errorf("the webhook payment entity is missing")
	}
	amount, ok := entity["amount"].(float64)
	if !ok {
		return shadowEvent{}, nil, fmt.Errorf("the webhook payment amount is missing")
	}
	return shadowEvent{
		Event:        stringValue(payload["event"]),
		Status:       stringValue(entity["status"]),
		Amount:       int64(amount),
		Currency:     stringValue(entity["currency"]),
		HasPaymentID: stringValue(entity["id"]) != "",
		HasOrderID:   stringValue(entity["order_id"]) != "",
	}, payload, nil
}

func signWebhook(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func (a *app) index(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, checkoutPage)
}

func (a *app) createOrder(w http.ResponseWriter, r *http.Request) {
	const amount int64 = 49900
	const currency = "INR"

	var order struct {
		ID       string `json:"id"`
		Amount   int64  `json:"amount"`
		Currency string `json:"currency"`
	}
	request := map[string]any{
		"amount": amount, "currency": currency,
		"receipt": fmt.Sprintf("paystable-%d", time.Now().UnixMilli()),
	}
	if err := a.postJSON(r, a.apiBaseURL+"/orders", request, &order, a.keyID, a.keySecret, ""); err != nil {
		slog.Error("create Razorpay order", "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Could not create the Razorpay order."})
		return
	}
	if order.ID == "" || order.Amount != amount || order.Currency != currency {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Razorpay returned an invalid order"})
		return
	}

	var hold struct {
		ReadToken string `json:"read_token"`
	}
	holdRequest := map[string]any{
		"txn_id": order.ID, "gateway": "razorpay", "amount": amount,
		"currency": currency, "ttl_seconds": 300, "callback_url": a.callbackURL,
		"metadata": map[string]string{"source": "razorpay-test-merchant"},
	}
	if err := a.postJSON(r, a.paystableURL+"/api/v1/hold", holdRequest, &hold, "", "", "Bearer "+a.adminKey); err != nil {
		slog.Error("create Paystable hold", "error", err, "order_id", order.ID)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Could not create the Paystable hold."})
		return
	}

	a.mu.Lock()
	a.orders[order.ID] = struct{}{}
	a.mu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]any{
		"key_id": a.keyID, "order_id": order.ID, "amount": amount,
		"currency": currency, "read_token": hold.ReadToken,
	})
}

func (a *app) verifyCheckout(w http.ResponseWriter, r *http.Request) {
	var result struct {
		OrderID   string `json:"razorpay_order_id"`
		PaymentID string `json:"razorpay_payment_id"`
		Signature string `json:"razorpay_signature"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&result); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid checkout response"})
		return
	}
	a.mu.Lock()
	_, known := a.orders[result.OrderID]
	a.mu.Unlock()
	if !known || result.PaymentID == "" || !validSignature(result.OrderID+"|"+result.PaymentID, result.Signature, a.keySecret) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid checkout signature"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"verified": true})
}

func (a *app) acceptCallback(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil || !delivery.Verify(body, r.Header.Get("X-Paystable-Signature"), a.callbackSecret) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid callback signature"})
		return
	}
	key := r.Header.Get("X-Paystable-Idempotency-Key")
	var event struct {
		TxnID  string `json:"txn_id"`
		Status string `json:"status"`
	}
	if key == "" || json.Unmarshal(body, &event) != nil || event.TxnID == "" || event.Status != "CONFIRMED" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid confirmed payment callback"})
		return
	}
	a.mu.Lock()
	a.effects[key] = event.TxnID
	a.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) acceptWebhook(w http.ResponseWriter, r *http.Request) {
	slog.Info("Razorpay webhook received",
		"content_length", r.ContentLength,
		"chunked", len(r.TransferEncoding) > 0,
		"expects_continue", strings.EqualFold(r.Header.Get("Expect"), "100-continue"),
	)
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	signature := r.Header.Get("X-Razorpay-Signature")
	eventID := r.Header.Get("X-Razorpay-Event-Id")
	if err != nil {
		slog.Warn("Razorpay webhook rejected", "reason", "body_read_failed")
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid Razorpay webhook"})
		return
	}
	if eventID == "" {
		slog.Warn("Razorpay webhook rejected", "reason", "event_id_missing")
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid Razorpay webhook"})
		return
	}
	if !razorpay.VerifyWebhookSignature(body, signature, a.webhookSecret) {
		slog.Warn("Razorpay webhook rejected", "reason", "signature_invalid")
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid Razorpay webhook"})
		return
	}
	fixture, err := json.MarshalIndent(struct {
		RecordedAt string `json:"recorded_at"`
		EventID    string `json:"event_id"`
		Signature  string `json:"signature"`
		BodyBase64 string `json:"body_base64"`
	}{time.Now().UTC().Format(time.RFC3339Nano), eventID, signature, base64.StdEncoding.EncodeToString(body)}, "", "  ")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not encode the webhook fixture"})
		return
	}
	if err := os.MkdirAll(filepath.Dir(a.fixturePath), 0o755); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create the fixture directory"})
		return
	}
	if err := os.WriteFile(a.fixturePath, fixture, 0o600); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not save the webhook fixture"})
		return
	}
	slog.Info("Razorpay webhook fixture saved")

	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, a.paystableURL+"/webhooks/razorpay", bytes.NewReader(body))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not forward the webhook"})
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Razorpay-Signature", signature)
	req.Header.Set("X-Razorpay-Event-Id", eventID)
	resp, err := a.http.Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Paystable did not accept the webhook"})
		return
	}
	defer resp.Body.Close()
	w.WriteHeader(resp.StatusCode)
}

func (a *app) listEffects(w http.ResponseWriter, _ *http.Request) {
	a.mu.Lock()
	effects := make(map[string]string, len(a.effects))
	for key, orderID := range a.effects {
		effects[key] = orderID
	}
	a.mu.Unlock()
	writeJSON(w, http.StatusOK, effects)
}

func (a *app) postJSON(r *http.Request, endpoint string, value, result any, user, password, authorization string) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if user != "" {
		req.SetBasicAuth(user, password)
	}
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("%s returned %s: %s", endpoint, resp.Status, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(result)
}

func validSignature(message, signature, secret string) bool {
	received, err := hex.DecodeString(signature)
	if err != nil || len(received) == 0 || secret == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(message))
	return hmac.Equal(mac.Sum(nil), received)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
