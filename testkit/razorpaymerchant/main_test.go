package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/IDEA-Amrita/paystable/internal/delivery"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCreateOrderAndVerifyCheckout(t *testing.T) {
	var razorpayAuth, paystableAuth string
	client := &http.Client{Timeout: time.Second, Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := `{"read_token":"pst_rt_test"}`
		status := http.StatusCreated
		if r.URL.Host == "razorpay.test" {
			razorpayAuth = r.Header.Get("Authorization")
			body = `{"id":"order_test","amount":49900,"currency":"INR"}`
			status = http.StatusOK
		} else if r.URL.Path == "/api/v1/hold" {
			paystableAuth = r.Header.Get("Authorization")
		} else {
			body = `{}`
			status = http.StatusOK
		}
		return &http.Response{
			StatusCode: status, Status: http.StatusText(status),
			Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header),
		}, nil
	})}

	a := &app{
		keyID: "rzp_test_key", keySecret: "secret", apiBaseURL: "https://razorpay.test/v1",
		paystableURL: "https://paystable.test", adminKey: "admin", callbackURL: "http://merchant/callback",
		webhookSecret: "webhook-secret", fixturePath: filepath.Join(t.TempDir(), "fixture.json"),
		http: client, orders: make(map[string]struct{}), effects: make(map[string]string),
	}
	recorder := httptest.NewRecorder()
	a.createOrder(recorder, httptest.NewRequest(http.MethodPost, "/orders", nil))
	if recorder.Code != http.StatusCreated || razorpayAuth == "" || paystableAuth != "Bearer admin" {
		t.Fatalf("create order returned %d; Razorpay auth=%t Paystable auth=%q", recorder.Code, razorpayAuth != "", paystableAuth)
	}

	message := "order_test|pay_test"
	mac := hmac.New(sha256.New, []byte("secret"))
	_, _ = mac.Write([]byte(message))
	checkout := `{"razorpay_order_id":"order_test","razorpay_payment_id":"pay_test","razorpay_signature":"` + hex.EncodeToString(mac.Sum(nil)) + `"}`
	recorder = httptest.NewRecorder()
	a.verifyCheckout(recorder, httptest.NewRequest(http.MethodPost, "/checkout", strings.NewReader(checkout)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("verify checkout returned %d: %s", recorder.Code, recorder.Body.String())
	}

	webhookBody := []byte(`{"event":"payment.captured","payload":{"payment":{"entity":{"id":"pay_test","order_id":"order_test","status":"captured","amount":49900}}}}`)
	mac = hmac.New(sha256.New, []byte(a.webhookSecret))
	_, _ = mac.Write(webhookBody)
	request := httptest.NewRequest(http.MethodPost, "/webhooks/razorpay", strings.NewReader(string(webhookBody)))
	request.Header.Set("X-Razorpay-Signature", hex.EncodeToString(mac.Sum(nil)))
	request.Header.Set("X-Razorpay-Event-Id", "event_test")
	recorder = httptest.NewRecorder()
	a.acceptWebhook(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("accept webhook returned %d: %s", recorder.Code, recorder.Body.String())
	}
	savedFixture, err := os.ReadFile(a.fixturePath)
	if err != nil {
		t.Fatalf("signed fixture was not saved: %v", err)
	}
	var fixture struct {
		Signature  string `json:"signature"`
		BodyBase64 string `json:"body_base64"`
	}
	if err := json.Unmarshal(savedFixture, &fixture); err != nil {
		t.Fatalf("signed fixture is invalid: %v", err)
	}
	savedBody, err := base64.StdEncoding.DecodeString(fixture.BodyBase64)
	if err != nil || !hmac.Equal(savedBody, webhookBody) {
		t.Fatal("signed fixture did not preserve the webhook body")
	}
	mac = hmac.New(sha256.New, []byte(a.webhookSecret))
	_, _ = mac.Write(savedBody)
	if fixture.Signature != hex.EncodeToString(mac.Sum(nil)) {
		t.Fatal("saved webhook signature does not match its body")
	}

	a.callbackSecret = "callback-secret"
	callbackBody := []byte(`{"txn_id":"order_test","status":"CONFIRMED"}`)
	request = httptest.NewRequest(http.MethodPost, "/callback", strings.NewReader(string(callbackBody)))
	request.Header.Set("X-Paystable-Signature", delivery.Sign(callbackBody, a.callbackSecret))
	request.Header.Set("X-Paystable-Idempotency-Key", "effect_test")
	recorder = httptest.NewRecorder()
	a.acceptCallback(recorder, request)
	if recorder.Code != http.StatusNoContent || len(a.effects) != 1 {
		t.Fatalf("accept callback returned %d with %d effects", recorder.Code, len(a.effects))
	}
}
