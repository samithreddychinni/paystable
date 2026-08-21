package razorpay

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestClientStatusPrefersCapturedPayment(t *testing.T) {
	client := NewClient("https://api.razorpay.test/v1/", "rzp_test_key", "secret")
	client.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/orders/order_123/payments" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("rzp_test_key:secret"))
		if r.Header.Get("Authorization") != wantAuth {
			t.Fatal("missing Basic authentication")
		}
		body := `{"entity":"collection","items":[{"status":"failed","amount":49900},{"status":"captured","amount":49900,"amount_captured":49900}]}`
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader(body))}, nil
	})}

	status, amount, raw, err := client.Status(context.Background(), "order_123")
	if err != nil {
		t.Fatal(err)
	}
	if status != "captured" || amount != 49900 || len(raw) == 0 {
		t.Fatalf("got status=%q amount=%d raw=%q", status, amount, raw)
	}
}

func TestClientStatusNoPayments(t *testing.T) {
	client := NewClient("https://api.razorpay.test/v1", "key", "secret")
	client.HTTP = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader(`{"items":[]}`))}, nil
	})}

	status, amount, _, err := client.Status(context.Background(), "order_123")
	if err != nil {
		t.Fatal(err)
	}
	if status != "not_found" || amount != 0 {
		t.Fatalf("got status=%q amount=%d", status, amount)
	}
}
