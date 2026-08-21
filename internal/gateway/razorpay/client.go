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
)

type Client struct {
	BaseURL   string
	KeyID     string
	KeySecret string
	HTTP      *http.Client
}

func NewClient(baseURL, keyID, keySecret string) *Client {
	return &Client{
		BaseURL:   strings.TrimRight(baseURL, "/"),
		KeyID:     keyID,
		KeySecret: keySecret,
		HTTP:      &http.Client{Timeout: 10 * time.Second},
	}
}

// Status fetches all payment attempts for a Razorpay order.
// The client selects a captured payment before any other payment attempt.
func (c *Client) Status(ctx context.Context, orderID string) (string, int64, json.RawMessage, error) {
	if c.BaseURL == "" || c.KeyID == "" || c.KeySecret == "" {
		return "", 0, nil, fmt.Errorf("razorpay API is not configured")
	}

	endpoint := c.BaseURL + "/orders/" + url.PathEscape(orderID) + "/payments"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", 0, nil, err
	}
	req.SetBasicAuth(c.KeyID, c.KeySecret)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", 0, nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", 0, json.RawMessage(body), fmt.Errorf("razorpay status API returned %s", resp.Status)
	}

	var result struct {
		Items []struct {
			Status         string `json:"status"`
			Amount         int64  `json:"amount"`
			AmountCaptured int64  `json:"amount_captured"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", 0, json.RawMessage(body), fmt.Errorf("invalid Razorpay response JSON: %w", err)
	}
	if len(result.Items) == 0 {
		return "not_found", 0, json.RawMessage(body), nil
	}

	best := result.Items[0]
	for _, payment := range result.Items[1:] {
		if razorpayStatusRank(payment.Status) > razorpayStatusRank(best.Status) {
			best = payment
		}
	}
	amount := best.Amount
	if best.Status == "captured" && best.AmountCaptured > 0 {
		amount = best.AmountCaptured
	}
	return best.Status, amount, json.RawMessage(body), nil
}

func razorpayStatusRank(status string) int {
	switch status {
	case "captured":
		return 5
	case "authorized":
		return 4
	case "created":
		return 3
	case "failed":
		return 2
	case "refunded":
		return 1
	default:
		return 0
	}
}
