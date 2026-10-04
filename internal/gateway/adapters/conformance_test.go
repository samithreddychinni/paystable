package adapters

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/IDEA-Amrita/paystable/internal/config"
)

func TestAdapterConformance(t *testing.T) {
	for _, name := range []string{"payu", "razorpay"} {
		t.Run(name, func(t *testing.T) {
			cfg := &config.Config{Gateway: name, WebhookSecret: "test-secret", GatewayAPIKey: "test-key", RazorpayKeyID: "rzp_test_key", RazorpayKeySecret: "test-key-secret"}
			adapter := New(cfg)[name]
			headers := http.Header{}
			body := []byte(`{"event":"payment.captured","payload":{"payment":{"entity":{"id":"pay_test","entity":"payment","order_id":"order_test","amount":49900,"currency":"INR","status":"captured"}}}}`)
			if name == "payu" {
				fields := append([]string{"test-secret", "success"}, make([]string, 13)...)
				fields = append(fields, "499.00", "order_test", "test-key")
				sum := sha512.Sum512([]byte(strings.Join(fields, "|")))
				body = []byte(url.Values{"key": {"test-key"}, "txnid": {"order_test"}, "status": {"success"}, "amount": {"499.00"}, "hash": {hex.EncodeToString(sum[:])}}.Encode())
				headers.Set("Content-Type", "application/x-www-form-urlencoded")
			} else {
				mac := hmac.New(sha256.New, []byte("test-secret"))
				_, _ = mac.Write(body)
				headers.Set("X-Razorpay-Signature", hex.EncodeToString(mac.Sum(nil)))
			}
			if err := adapter.VerifyWebhook(body, headers, "test-secret"); err != nil {
				t.Fatal(err)
			}
			if err := adapter.VerifyWebhook(body, headers, "wrong-secret"); err == nil {
				t.Fatal("invalid secret accepted")
			}
			tampered := strings.Replace(string(body), "499", "498", 1)
			if err := adapter.VerifyWebhook([]byte(tampered), headers, "test-secret"); err == nil {
				t.Fatal("tampered body accepted")
			}
			event, err := adapter.ParseWebhook(body, headers)
			if err != nil || event.Status != "success" || event.Amount != 49900 || event.TxnID != "order_test" {
				t.Fatalf("normalization: %+v %v", event, err)
			}
			for _, tc := range []struct {
				name, status, want string
				malformed, slow    bool
			}{
				{"success", "success", "success", false, false}, {"failed", "failed", "failed", false, false}, {"pending", "pending", "pending", false, false}, {"unknown", "unknown", "pending", false, false}, {"malformed", "", "", true, false}, {"timeout", "", "", false, true},
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
						if tc.malformed {
							_, _ = w.Write([]byte(`{bad`))
							return
						}
						if name == "payu" {
							_, _ = fmt.Fprintf(w, `{"status":1,"transaction_details":{"order_test":{"status":%q,"amt":"499.00"}}}`, tc.status)
							return
						}
						status := tc.status
						paid, due := 0, 49900
						orderStatus := "attempted"
						if status == "success" {
							status = "captured"
							paid, due = 49900, 0
							orderStatus = "paid"
						}
						if strings.HasSuffix(r.URL.Path, "/payments") {
							_, _ = fmt.Fprintf(w, `{"entity":"collection","count":1,"items":[{"id":"pay_test","entity":"payment","order_id":"order_test","amount":49900,"currency":"INR","status":%q}]}`, status)
						} else {
							_, _ = fmt.Fprintf(w, `{"id":"order_test","entity":"order","status":%q,"amount":49900,"amount_paid":%d,"amount_due":%d,"currency":"INR"}`, orderStatus, paid, due)
						}
					}))
					defer srv.Close()
					cfg.PayuStatusURL = srv.URL
					cfg.RazorpayAPIURL = srv.URL
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
					defer cancel()
					status, amount, _, err := New(cfg)[name].Status(ctx, "order_test")
					if tc.malformed || tc.slow {
						if err == nil {
							t.Fatal("invalid response accepted")
						}
						return
					}
					if err != nil || status != tc.want || amount != 49900 {
						t.Fatalf("status=%s amount=%d err=%v", status, amount, err)
					}
				})
			}
		})
	}
}
