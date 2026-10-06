package delivery

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

// Sign authenticates the delivery timestamp, event key, and raw body.
// The wire format is v2=<lowercase hex HMAC-SHA256>.
func Sign(body []byte, idempotencyKey, timestamp, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("v2\n" + timestamp + "\n" + idempotencyKey + "\n"))
	mac.Write(body)
	return "v2=" + hex.EncodeToString(mac.Sum(nil))
}

// Verify checks a v2 signature and permits at most five minutes of clock skew.
// Merchants must still deduplicate the authenticated key before fulfillment.
func Verify(body []byte, header, idempotencyKey, timestamp, secret string) bool {
	if secret == "" || idempotencyKey == "" || strings.ContainsAny(idempotencyKey, "\r\n") {
		return false
	}
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	now := time.Now().Unix()
	if err != nil || strconv.FormatInt(ts, 10) != timestamp || ts < now-300 || ts > now+300 {
		return false
	}
	return hmac.Equal([]byte(header), []byte(Sign(body, idempotencyKey, timestamp, secret)))
}
