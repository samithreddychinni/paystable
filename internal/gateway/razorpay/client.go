package razorpay

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/IDEA-Amrita/paystable/internal/gateway"
)

type Client struct {
	BaseURL, KeyID, KeySecret string
	HTTP                      *http.Client
}

func NewClient(baseURL, keyID, keySecret string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), KeyID: keyID, KeySecret: keySecret, HTTP: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (c *Client) WaitForExpiry() bool { return true }

type order struct {
	ID             string `json:"id"`
	Entity         string `json:"entity"`
	Amount         int64  `json:"amount"`
	AmountPaid     int64  `json:"amount_paid"`
	AmountDue      int64  `json:"amount_due"`
	Currency       string `json:"currency"`
	Status         string `json:"status"`
	PartialPayment bool   `json:"partial_payment"`
}

type payment struct {
	ID             string `json:"id"`
	Entity         string `json:"entity"`
	OrderID        string `json:"order_id"`
	Amount         int64  `json:"amount"`
	Currency       string `json:"currency"`
	Status         string `json:"status"`
	AmountRefunded int64  `json:"amount_refunded"`
}

func (c *Client) fetch(ctx context.Context, path string) (json.RawMessage, error) {
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, fmt.Errorf("razorpay API URL is invalid")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("razorpay request is invalid")
	}
	req.SetBasicAuth(c.KeyID, c.KeySecret)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, gateway.ErrTransient
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 500 || resp.StatusCode == 408 || resp.StatusCode == 429 {
		return nil, gateway.ErrTransient
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("razorpay API returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil {
		return nil, gateway.ErrTransient
	}
	if len(body) > 1<<20 || !json.Valid(body) {
		return nil, fmt.Errorf("razorpay API response is invalid")
	}
	return json.RawMessage(body), nil
}

func (c *Client) Status(ctx context.Context, txnID string) (string, int64, json.RawMessage, error) {
	if !orderIDPattern.MatchString(txnID) {
		return "", 0, nil, fmt.Errorf("razorpay transaction ID must be an order ID")
	}
	orderRaw, err := c.fetch(ctx, "/orders/"+txnID)
	if err != nil {
		return "", 0, nil, err
	}
	paymentsRaw, err := c.fetch(ctx, "/orders/"+txnID+"/payments")
	if err != nil {
		return "", 0, nil, err
	}
	raw, _ := json.Marshal(map[string]json.RawMessage{"order": orderRaw, "payments": paymentsRaw})
	var o order
	var attempts struct {
		Entity string    `json:"entity"`
		Count  int       `json:"count"`
		Items  []payment `json:"items"`
	}
	if !hasFields(orderRaw, "amount_paid", "amount_due", "status") || !hasFields(paymentsRaw, "count", "items") || json.Unmarshal(orderRaw, &o) != nil || json.Unmarshal(paymentsRaw, &attempts) != nil || o.ID != txnID || o.Entity != "order" || o.Amount <= 0 || o.Currency != "INR" || o.AmountPaid < 0 || o.AmountDue < 0 || attempts.Entity != "collection" || attempts.Items == nil || attempts.Count != len(attempts.Items) {
		return "", 0, raw, fmt.Errorf("razorpay API response fields are invalid")
	}
	if o.Status != "created" && o.Status != "attempted" && o.Status != "paid" {
		return "pending", o.Amount, raw, nil
	}
	var captured *payment
	pending, refunded, partial := false, false, o.PartialPayment || (o.AmountPaid > 0 && o.AmountDue > 0)
	seen := map[string]bool{}
	for i := range attempts.Items {
		p := &attempts.Items[i]
		if p.ID == "" || seen[p.ID] || p.Entity != "payment" || p.OrderID != txnID || p.Amount <= 0 || p.Currency != "INR" || p.AmountRefunded < 0 {
			return "", 0, raw, fmt.Errorf("razorpay payment fields are invalid")
		}
		seen[p.ID] = true
		if p.Status == "refunded" || p.AmountRefunded > 0 {
			refunded = true
		}
		switch normalizeStatus(p.Status) {
		case "success":
			if captured != nil {
				partial = true
			}
			captured = p
		case "failed":
		default:
			pending = true
		}
	}
	if refunded {
		return "indeterminate", o.AmountPaid, raw, nil
	}
	if partial {
		return "mismatch", o.AmountPaid, raw, nil
	}
	if captured != nil {
		if o.Status != "paid" {
			return "pending", captured.Amount, raw, nil
		}
		if captured.Amount != o.Amount || o.AmountPaid != o.Amount || o.AmountDue != 0 {
			return "mismatch", captured.Amount, raw, nil
		}
		return "success", captured.Amount, raw, nil
	}
	if o.Status == "paid" || o.AmountPaid != 0 {
		return "indeterminate", o.AmountPaid, raw, nil
	}
	if pending {
		return "pending", o.Amount, raw, nil
	}
	return "failed", o.Amount, raw, nil
}

func normalizeStatus(status string) string {
	switch status {
	case "captured":
		return "success"
	case "failed":
		return "failed"
	default:
		return "pending"
	}
}

func hasFields(raw json.RawMessage, keys ...string) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return false
	}
	for _, key := range keys {
		if len(fields[key]) == 0 || string(fields[key]) == "null" {
			return false
		}
	}
	return true
}

func (c *Client) CaptureObserved(raw json.RawMessage) bool {
	var evidence struct {
		Payments struct {
			Items []payment `json:"items"`
		} `json:"payments"`
	}
	if json.Unmarshal(raw, &evidence) != nil {
		return false
	}
	for _, p := range evidence.Payments.Items {
		if p.Status == "captured" {
			return true
		}
	}
	return false
}
