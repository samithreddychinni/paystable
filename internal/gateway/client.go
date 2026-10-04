package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

// GatewayClient abstracts gateway status checks.
// Status returns normalized status (e.g. "success","failed","pending","not_found"),
// amount in smallest currency unit, raw JSON from gateway, and error.
type GatewayClient interface {
	Status(ctx context.Context, txnID string) (gatewayStatus string, gatewayAmount int64, raw json.RawMessage, err error)
}

var ErrMalformedWebhook = errors.New("malformed webhook")
var ErrSignature = errors.New("invalid webhook signature")
var ErrTransient = errors.New("gateway request failed temporarily")

type Webhook struct {
	TxnID, EventID, EventType, Status, Currency string
	Amount                                      int64
	Timestamp                                   int64
	Payload                                     json.RawMessage
	Actionable                                  bool
}

type Adapter interface {
	GatewayClient
	VerifyWebhook([]byte, http.Header, string) error
	ParseWebhook([]byte, http.Header) (Webhook, error)
}

// ExpiryPolicy keeps retryable order attempts open until the hold expires.
type ExpiryPolicy interface {
	WaitForExpiry() bool
}

func WaitForExpiry(client GatewayClient) bool {
	p, ok := client.(ExpiryPolicy)
	return ok && p.WaitForExpiry()
}

type CaptureObserver interface {
	CaptureObserved(json.RawMessage) bool
}
