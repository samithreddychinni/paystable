package payu

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"github.com/IDEA-Amrita/paystable/internal/gateway"
)

func ParsePayload(body []byte, contentType string) (map[string]string, error) {
	params := make(map[string]string)
	if contentType == "application/json" || (len(body) > 0 && body[0] == '{') {
		if err := json.Unmarshal(body, &params); err != nil {
			return nil, gateway.ErrMalformedWebhook
		}
		return params, nil
	}
	values, err := url.ParseQuery(string(body))
	if err != nil {
		return nil, gateway.ErrMalformedWebhook
	}
	for k, v := range values {
		if len(v) > 0 {
			params[k] = v[0]
		}
	}
	return params, nil
}

func (c *Client) VerifyWebhook(body []byte, headers http.Header, secret string) error {
	params, err := ParsePayload(body, headers.Get("Content-Type"))
	if err != nil {
		return err
	}
	if !VerifyResponseHash(params, secret) {
		return gateway.ErrSignature
	}
	return nil
}

func (c *Client) ParseWebhook(body []byte, headers http.Header) (gateway.Webhook, error) {
	params, err := ParsePayload(body, headers.Get("Content-Type"))
	if err != nil {
		return gateway.Webhook{}, err
	}
	payload, err := json.Marshal(params)
	if err != nil {
		return gateway.Webhook{}, err
	}
	amount, _ := parseAmount(params["amount"])
	timestamp, _ := strconv.ParseInt(params["timestamp"], 10, 64)
	return gateway.Webhook{TxnID: params["txnid"], EventID: params["mihpayid"], EventType: "payment." + params["status"], Status: normalizeStatus(params["status"]), Amount: amount, Currency: "INR", Timestamp: timestamp, Payload: payload, Actionable: true}, nil
}
