package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

func razorpayEntities(txnID string, s txnState) (map[string]any, map[string]any) {
	status := s.Status
	if !s.FailUntil.IsZero() && time.Now().Before(s.FailUntil) {
		status = "failed"
	}
	status = razorpayStatus(status)
	orderStatus := "attempted"
	paid, due := int64(0), int64(s.Amount)
	if status == "captured" {
		orderStatus = "paid"
		paid, due = int64(s.Amount), 0
	}
	o := map[string]any{"id": txnID, "entity": "order", "status": orderStatus, "amount": int64(s.Amount), "currency": "INR", "amount_paid": paid, "amount_due": due}
	p := map[string]any{"id": "pay_" + txnID, "entity": "payment", "order_id": txnID, "status": status, "amount": int64(s.Amount), "currency": "INR", "amount_refunded": 0}
	return o, p
}

func razorpayStatus(status string) string {
	switch status {
	case "success":
		return "captured"
	case "failure":
		return "failed"
	case "pending":
		return "authorized"
	default:
		return status
	}
}

func razorpayState(w http.ResponseWriter, r *http.Request) (txnState, bool) {
	id, secret, ok := r.BasicAuth()
	if !ok || id != envOr("RAZORPAY_KEY_ID", "rzp_test_mock") || secret != envOr("RAZORPAY_KEY_SECRET", "test-key-secret") {
		http.Error(w, "invalid test credentials", http.StatusUnauthorized)
		return txnState{}, false
	}
	mu.RLock()
	s, ok := states[r.PathValue("id")]
	mu.RUnlock()
	if !ok {
		http.Error(w, "order not found", 404)
		return s, false
	}
	return s, true
}

func handleRazorpayOrder(w http.ResponseWriter, r *http.Request) {
	s, ok := razorpayState(w, r)
	if !ok {
		return
	}
	o, _ := razorpayEntities(r.PathValue("id"), s)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(o)
}

func handleRazorpayPayments(w http.ResponseWriter, r *http.Request) {
	s, ok := razorpayState(w, r)
	if !ok {
		return
	}
	_, p := razorpayEntities(r.PathValue("id"), s)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"entity": "collection", "count": 1, "items": []any{p}})
}

func fireRazorpayWebhook(w http.ResponseWriter, r *http.Request, txnID, status string, s txnState) {
	status = razorpayStatus(status)
	o, p := razorpayEntities(txnID, s)
	p["status"] = status
	body, _ := json.Marshal(map[string]any{"event": "payment." + status, "payload": map[string]any{"payment": map[string]any{"entity": p}, "order": map[string]any{"entity": o}}})
	mac := hmac.New(sha256.New, []byte(salt))
	_, _ = mac.Write(body)
	req, err := http.NewRequestWithContext(r.Context(), "POST", psURL+"/webhooks/razorpay", bytes.NewReader(body))
	if err != nil {
		http.Error(w, "invalid webhook request", 500)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Razorpay-Signature", hex.EncodeToString(mac.Sum(nil)))
	req.Header.Set("X-Razorpay-Event-Id", "evt_"+txnID+"_"+status)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		http.Error(w, "webhook request failed", http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		http.Error(w, fmt.Sprintf("webhook returned HTTP %d", resp.StatusCode), http.StatusBadGateway)
		return
	}
	w.WriteHeader(200)
}
