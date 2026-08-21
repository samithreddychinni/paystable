package razorpay

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestVerifyWebhookSignature(t *testing.T) {
	body := []byte(`{"event":"payment.captured"}`)
	secret := "test-webhook-secret"
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	signature := hex.EncodeToString(mac.Sum(nil))

	if !VerifyWebhookSignature(body, signature, secret) {
		t.Fatal("valid signature rejected")
	}
	if VerifyWebhookSignature([]byte(`{"event":"payment.failed"}`), signature, secret) {
		t.Fatal("tampered body accepted")
	}
	if VerifyWebhookSignature(body, "not-hex", secret) {
		t.Fatal("malformed signature accepted")
	}
}
