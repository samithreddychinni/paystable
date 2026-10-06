package razorpay

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/IDEA-Amrita/paystable/internal/gateway"
)

func signature(body []byte) string {
	mac := hmac.New(sha256.New, []byte("test-secret"))
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func TestWebhookSignature(t *testing.T) {
	body := []byte(`{"event":"payment.captured"}`)
	c := NewClient("", "", "")
	for _, tc := range []struct {
		name  string
		body  []byte
		sig   string
		valid bool
	}{
		{"valid", body, signature(body), true},
		{"invalid", body, strings.Repeat("0", 64), false},
		{"tampered", append(append([]byte{}, body...), ' '), signature(body), false},
		{"malformed signature", body, "xyz", false},
		{"missing", body, "", false},
		{"raw malformed JSON", []byte(`{broken`), signature([]byte(`{broken`)), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			headers := http.Header{}
			headers.Set("X-Razorpay-Signature", tc.sig)
			if got := c.VerifyWebhook(tc.body, headers, "test-secret") == nil; got != tc.valid {
				t.Fatalf("valid=%v", got)
			}
		})
	}
}

func TestStatusMappings(t *testing.T) {
	for status, want := range map[string]string{"captured": "success", "failed": "failed", "created": "pending", "authorized": "pending", "unexpected": "pending", "refunded": "pending"} {
		if got := normalizeStatus(status); got != want {
			t.Errorf("%s=%s, want %s", status, got, want)
		}
	}
}

func TestOrderAttempts(t *testing.T) {
	for _, tc := range []struct {
		name            string
		statuses        []string
		orderStatus     string
		partial, refund bool
		want            string
	}{
		{"captured", []string{"captured"}, "paid", false, false, "success"},
		{"failed", []string{"failed"}, "attempted", false, false, "failed"},
		{"no attempts", []string{}, "created", false, false, "failed"},
		{"created", []string{"created"}, "attempted", false, false, "pending"},
		{"authorized", []string{"authorized"}, "attempted", false, false, "pending"},
		{"failed then captured", []string{"failed", "captured"}, "paid", false, false, "success"},
		{"failed then authorized", []string{"failed", "authorized"}, "attempted", false, false, "pending"},
		{"captured but order attempted", []string{"captured"}, "attempted", false, false, "pending"},
		{"partial", []string{"captured"}, "attempted", true, false, "mismatch"},
		{"refund", []string{"refunded"}, "paid", false, true, "indeterminate"},
		{"partial refund", []string{"captured"}, "paid", false, true, "indeterminate"},
		{"unknown", []string{"unknown"}, "attempted", false, false, "pending"},
		{"paid without payment", []string{}, "paid", false, false, "indeterminate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := order{ID: "order_test", Entity: "order", Status: tc.orderStatus, Amount: 49900, AmountDue: 49900, Currency: "INR", PartialPayment: tc.partial}
			if tc.orderStatus == "paid" {
				o.AmountPaid = 49900
				o.AmountDue = 0
			}
			items := make([]payment, 0, len(tc.statuses))
			for i, status := range tc.statuses {
				p := payment{ID: "pay_" + string(rune('a'+i)), Entity: "payment", OrderID: o.ID, Status: status, Amount: 49900, Currency: "INR"}
				if tc.refund {
					p.AmountRefunded = 100
				}
				items = append(items, p)
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				id, secret, ok := r.BasicAuth()
				if !ok || id != "rzp_test_key" || secret != "test-key-secret" || r.Method != "GET" {
					t.Error("incorrect auth or method")
				}
				if strings.HasSuffix(r.URL.Path, "/payments") {
					_ = json.NewEncoder(w).Encode(map[string]any{"entity": "collection", "count": len(items), "items": items})
				} else {
					_ = json.NewEncoder(w).Encode(o)
				}
			}))
			defer srv.Close()
			status, amount, raw, err := NewClient(srv.URL, "rzp_test_key", "test-key-secret").Status(context.Background(), o.ID)
			if err != nil || status != tc.want {
				t.Fatalf("status=%s err=%v, want %s", status, err, tc.want)
			}
			if tc.want == "success" && amount != 49900 {
				t.Fatalf("amount=%d", amount)
			}
			if !json.Valid(raw) {
				t.Fatal("missing evidence")
			}
		})
	}
}

func TestStatusErrors(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		code       int
		slow       bool
		transient  bool
	}{
		{"malformed", "{broken", 200, false, false},
		{"missing fields", "{}", 200, false, false},
		{"fractional amount", `{"id":"order_test","entity":"order","amount":1.5}`, 200, false, false},
		{"server error", "test-key-secret", 503, false, true},
		{"unauthorized", "test-key-secret", 401, false, false},
		{"timeout", "", 200, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.slow {
					select {
					case <-r.Context().Done():
					case <-time.After(100 * time.Millisecond):
					}
					return
				}
				w.WriteHeader(tc.code)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			c := NewClient(srv.URL, "rzp_test_key", "test-key-secret")
			c.HTTP.Timeout = 20 * time.Millisecond
			_, _, _, err := c.Status(context.Background(), "order_test")
			if err == nil || errors.Is(err, gateway.ErrTransient) != tc.transient || strings.Contains(err.Error(), "test-key-secret") {
				t.Fatalf("unsafe error: %v", err)
			}
		})
	}
}

func TestWebhookEvents(t *testing.T) {
	c := NewClient("", "", "")
	for _, event := range []string{"payment.captured", "payment.failed", "payment.authorized", "order.paid", "refund.created"} {
		body, _ := json.Marshal(map[string]any{"event": event, "payload": map[string]any{"payment": map[string]any{"entity": payment{ID: "pay_test", Entity: "payment", OrderID: "order_test", Amount: 49900, Currency: "INR", Status: "captured"}}, "order": map[string]any{"entity": order{ID: "order_test"}}}})
		e, err := c.ParseWebhook(body, http.Header{})
		if err != nil || e.TxnID != "order_test" || e.EventID == "" || e.Actionable != (event != "refund.created") {
			t.Fatalf("%s: %+v %v", event, e, err)
		}
	}
	e, err := c.ParseWebhook([]byte(`{"event":"unknown.event","payload":{}}`), http.Header{})
	if err != nil || e.Actionable || e.TxnID != "" {
		t.Fatalf("unknown event: %+v %v", e, err)
	}
}
