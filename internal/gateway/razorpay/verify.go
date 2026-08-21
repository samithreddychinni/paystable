package razorpay

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// VerifyWebhookSignature checks Razorpay's HMAC-SHA256 signature over the raw request body.
func VerifyWebhookSignature(body []byte, signature, secret string) bool {
	received, err := hex.DecodeString(signature)
	if err != nil || len(received) == 0 || secret == "" {
		return false
	}

	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return hmac.Equal(mac.Sum(nil), received)
}
