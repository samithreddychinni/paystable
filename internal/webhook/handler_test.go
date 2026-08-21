package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"testing"

	"github.com/IDEA-Amrita/paystable/internal/config"
)

func TestParsePayload_JSON(t *testing.T) {
	body := []byte(`{"txnid":"order_1","status":"success","amount":"100.00"}`)

	params, err := parsePayload(body, "application/json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if params["txnid"] != "order_1" {
		t.Errorf("txnid = %q, want order_1", params["txnid"])
	}
	if params["status"] != "success" {
		t.Errorf("status = %q, want success", params["status"])
	}
}

func TestParsePayload_FormEncoded(t *testing.T) {
	body := []byte("txnid=order_2&status=failure&amount=250.00&email=user%40test.com")

	params, err := parsePayload(body, "application/x-www-form-urlencoded")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if params["txnid"] != "order_2" {
		t.Errorf("txnid = %q, want order_2", params["txnid"])
	}
	if params["email"] != "user@test.com" {
		t.Errorf("email = %q, want user@test.com", params["email"])
	}
}

func TestParsePayload_JSONAutoDetect(t *testing.T) {
	body := []byte(`{"txnid":"order_3"}`)

	params, err := parsePayload(body, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if params["txnid"] != "order_3" {
		t.Errorf("txnid = %q, want order_3", params["txnid"])
	}
}

func TestParsePayload_MalformedJSON(t *testing.T) {
	body := []byte(`{"txnid": broken`)

	_, err := parsePayload(body, "application/json")
	if err == nil {
		t.Error("expected error for malformed JSON")
	}
}

func TestParsePayload_EmptyBody(t *testing.T) {
	body := []byte("")

	params, err := parsePayload(body, "application/x-www-form-urlencoded")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(params) != 0 {
		t.Errorf("expected empty params, got %d entries", len(params))
	}
}

func TestParsePayload_FormWithSpecialChars(t *testing.T) {
	body := []byte("productinfo=Test+Product+%26+More&firstname=John+Doe")

	params, err := parsePayload(body, "application/x-www-form-urlencoded")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if params["productinfo"] != "Test Product & More" {
		t.Errorf("productinfo = %q, want 'Test Product & More'", params["productinfo"])
	}
	if params["firstname"] != "John Doe" {
		t.Errorf("firstname = %q, want 'John Doe'", params["firstname"])
	}
}

func TestParseRazorpayPayload(t *testing.T) {
	body := []byte(`{
		"event":"payment.captured",
		"payload":{"payment":{"entity":{"id":"pay_123","order_id":"order_123","status":"captured","amount":49900}}}
	}`)

	params, err := parseGatewayPayload("razorpay", body, "application/json; charset=utf-8")
	if err != nil {
		t.Fatal(err)
	}
	if params["event"] != "payment.captured" || params["payment_id"] != "pay_123" || params["order_id"] != "order_123" || params["amount"] != "49900" {
		t.Fatalf("unexpected normalized payload: %#v", params)
	}
	if extractTxnID("razorpay", params) != "order_123" || extractEventType("razorpay", params) != "payment.captured" {
		t.Fatal("Razorpay identity or event type was not extracted")
	}
}

func TestHandlerVerifiesRazorpayRawBody(t *testing.T) {
	body := []byte(`{"event":"payment.captured"}`)
	secret := "webhook-secret"
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	headers := make(http.Header)
	headers.Set("X-Razorpay-Signature", hex.EncodeToString(mac.Sum(nil)))

	h := NewHandler(nil, &config.Config{RazorpayWebhookSecret: secret})
	if !h.verify(context.Background(), "razorpay", nil, body, headers) {
		t.Fatal("valid Razorpay signature rejected")
	}
	if h.verify(context.Background(), "razorpay", nil, append(body, ' '), headers) {
		t.Fatal("tampered Razorpay body accepted")
	}
}
