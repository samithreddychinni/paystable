package razorpay

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"regexp"

	"github.com/IDEA-Amrita/paystable/internal/gateway"
)

var orderIDPattern = regexp.MustCompile(`^order_[A-Za-z0-9]+$`)

func (c *Client) VerifyWebhook(body []byte, headers http.Header, secret string) error {
	signature, err := hex.DecodeString(headers.Get("X-Razorpay-Signature"))
	if err != nil || len(signature) != sha256.Size || secret == "" {
		return gateway.ErrSignature
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return gateway.ErrSignature
	}
	return nil
}

func (c *Client) ParseWebhook(body []byte, headers http.Header) (gateway.Webhook, error) {
	var envelope struct {
		Event   string `json:"event"`
		Payload struct {
			Payment struct {
				Entity payment `json:"entity"`
			} `json:"payment"`
			Order struct {
				Entity order `json:"entity"`
			} `json:"order"`
		} `json:"payload"`
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.Event == "" {
		return gateway.Webhook{}, gateway.ErrMalformedWebhook
	}
	p := envelope.Payload.Payment.Entity
	o := envelope.Payload.Order.Entity
	e := gateway.Webhook{TxnID: p.OrderID, EventID: headers.Get("X-Razorpay-Event-Id"), EventType: envelope.Event, Status: normalizeStatus(p.Status), Amount: p.Amount, Currency: p.Currency, Payload: json.RawMessage(body)}
	if e.EventID == "" {
		sum := sha256.Sum256(body)
		e.EventID = hex.EncodeToString(sum[:])
	}
	switch envelope.Event {
	case "payment.captured", "payment.failed", "payment.authorized", "order.paid":
		e.Actionable = true
	}
	if envelope.Event == "order.paid" {
		if e.TxnID != "" && e.TxnID != o.ID {
			return gateway.Webhook{}, gateway.ErrMalformedWebhook
		}
		e.TxnID = o.ID
	}
	if e.Actionable && (!orderIDPattern.MatchString(e.TxnID) || p.ID == "" || p.Entity != "payment" || p.Amount <= 0 || p.Currency != "INR") {
		return gateway.Webhook{}, gateway.ErrMalformedWebhook
	}
	return e, nil
}
