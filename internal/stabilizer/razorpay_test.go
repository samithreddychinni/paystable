package stabilizer

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/IDEA-Amrita/paystable/internal/config"
	"github.com/IDEA-Amrita/paystable/internal/database"
	"github.com/IDEA-Amrita/paystable/internal/delivery"
	"github.com/IDEA-Amrita/paystable/internal/gateway/razorpay"
	"github.com/IDEA-Amrita/paystable/internal/hold"
	"github.com/IDEA-Amrita/paystable/internal/webhook"
	"github.com/lib/pq"
)

func razorpayDB(t *testing.T) *sql.DB {
	t.Helper()
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("DATABASE_URL not set")
	}
	u, err := url.Parse(os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	admin, err := sql.Open("postgres", u.String())
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("razorpay_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE DATABASE " + pq.QuoteIdentifier(name)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, err := admin.Exec("DROP DATABASE " + pq.QuoteIdentifier(name) + " WITH (FORCE)")
		if err != nil {
			t.Error(err)
		}
		_ = admin.Close()
	})
	u.Path = "/" + name
	db, err := sql.Open("postgres", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func razorpayAPI(t *testing.T, txnID string, statuses []string, amount int64, partial, refund bool) *razorpay.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		items := []map[string]any{}
		paid := false
		for i, status := range statuses {
			p := map[string]any{"id": fmt.Sprintf("pay_%d", i), "entity": "payment", "order_id": txnID, "status": status, "currency": "INR", "amount": amount, "amount_refunded": 0}
			if refund {
				p["amount_refunded"] = 100
			}
			if status == "captured" || status == "refunded" {
				paid = true
			}
			items = append(items, p)
		}
		if strings.HasSuffix(r.URL.Path, "/payments") {
			_ = json.NewEncoder(w).Encode(map[string]any{"entity": "collection", "count": len(items), "items": items})
			return
		}
		status := "attempted"
		amountPaid, amountDue := int64(0), amount
		if len(items) == 0 {
			status = "created"
		}
		if paid {
			status = "paid"
			amountPaid, amountDue = amount, 0
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": txnID, "entity": "order", "status": status, "currency": "INR", "amount": amount, "amount_paid": amountPaid, "amount_due": amountDue, "partial_payment": partial})
	}))
	t.Cleanup(srv.Close)
	return razorpay.NewClient(srv.URL, "rzp_test_key", "test-key-secret")
}

func TestRazorpayExpiry(t *testing.T) {
	for _, tc := range []struct {
		name            string
		statuses        []string
		amount          int64
		partial, refund bool
		want            string
	}{
		{"captured", []string{"captured"}, 49900, false, false, "CONFIRMED"},
		{"amount mismatch", []string{"captured"}, 25000, false, false, "MISMATCH"},
		{"created", []string{"created"}, 49900, false, false, "INDETERMINATE"},
		{"authorized", []string{"authorized"}, 49900, false, false, "INDETERMINATE"},
		{"all failed", []string{"failed", "failed"}, 49900, false, false, "FAILED"},
		{"no attempts", []string{}, 49900, false, false, "FAILED"},
		{"failed then captured", []string{"failed", "captured"}, 49900, false, false, "CONFIRMED"},
		{"failed then authorized", []string{"failed", "authorized"}, 49900, false, false, "INDETERMINATE"},
		{"partial", []string{"captured"}, 49900, true, false, "MISMATCH"},
		{"refunded", []string{"refunded"}, 49900, false, true, "INDETERMINATE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := razorpayDB(t)
			txnID := seedExpiredHold(t, db, 49900)
			orderID := "order_" + strings.TrimPrefix(txnID, "ttl-")
			if _, err := db.Exec(`UPDATE holds SET gateway='razorpay',txn_id=$2 WHERE txn_id=$1`, txnID, orderID); err != nil {
				t.Fatal(err)
			}
			client := razorpayAPI(t, orderID, tc.statuses, tc.amount, tc.partial, tc.refund)
			resolveExpiredHold(context.Background(), db, expiredHold{TxnID: orderID, Gateway: "razorpay", Amount: 49900, Currency: "INR"}, factory(client))
			if got := holdStatus(t, db, orderID); got != tc.want {
				t.Fatalf("status=%s want %s", got, tc.want)
			}
			var callbacks int
			if err := db.QueryRow(`SELECT count(*) FROM outbox WHERE txn_id=$1`, orderID).Scan(&callbacks); err != nil {
				t.Fatal(err)
			}
			if callbacks != 1 {
				t.Fatalf("callbacks=%d", callbacks)
			}
		})
	}
}

func TestRazorpayFailedBeforeExpiry(t *testing.T) {
	db := razorpayDB(t)
	txnID := "order_failed"
	seedHold(t, db, txnID, "razorpay", 49900)
	client := razorpayAPI(t, txnID, []string{"failed"}, 49900, false, false)
	for attempt := 1; attempt <= 4; attempt++ {
		id := seedPoll(t, db, txnID, attempt)
		processPoll(context.Background(), db, defaultCfg(), NewLagEstimator(), pollRow(id, txnID, attempt, "razorpay", 49900), factory(client))
	}
	if got := holdStatus(t, db, txnID); got != "VERIFYING" {
		t.Fatalf("failed before expiry: %s", got)
	}
}

func TestRazorpayTransientErrors(t *testing.T) {
	for _, slow := range []bool{false, true} {
		t.Run(fmt.Sprint(slow), func(t *testing.T) {
			db := razorpayDB(t)
			txnID := "order_transient"
			seedHold(t, db, txnID, "razorpay", 49900)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if slow {
					select {
					case <-r.Context().Done():
					case <-time.After(100 * time.Millisecond):
					}
					return
				}
				w.WriteHeader(503)
			}))
			defer srv.Close()
			client := razorpay.NewClient(srv.URL, "rzp_test_key", "test-key-secret")
			client.HTTP.Timeout = 20 * time.Millisecond
			id := seedPoll(t, db, txnID, 3)
			processPoll(context.Background(), db, defaultCfg(), NewLagEstimator(), pollRow(id, txnID, 3, "razorpay", 49900), factory(client))
			if got := holdStatus(t, db, txnID); got != "VERIFYING" {
				t.Fatalf("transient error became terminal: %s", got)
			}
			if n := countPendingPolls(t, db, txnID); n < 1 {
				t.Fatal("retry missing")
			}
			if _, err := db.Exec(`UPDATE holds SET expires_at=now()-interval '1 second' WHERE txn_id=$1`, txnID); err != nil {
				t.Fatal(err)
			}
			resolveExpiredHold(context.Background(), db, expiredHold{TxnID: txnID, Gateway: "razorpay", Amount: 49900}, factory(client))
			if got := holdStatus(t, db, txnID); got != "INDETERMINATE" {
				t.Fatalf("expiry error=%s", got)
			}
		})
	}
}

func TestRazorpayLateCapture(t *testing.T) {
	t.Setenv("SLACK_WEBHOOK_URL", "")
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	db := razorpayDB(t)
	txnID := "order_late"
	seedHold(t, db, txnID, "razorpay", 49900)
	if err := markHoldExhausted(context.Background(), db, txnID, "ttl_expired_verified_failed"); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	client := razorpayAPI(t, txnID, []string{"captured"}, 49900, false, false)
	for i := 1; i <= 2; i++ {
		id := seedPoll(t, db, txnID, i)
		processPoll(context.Background(), db, defaultCfg(), NewLagEstimator(), pollRow(id, txnID, i, "razorpay", 49900), factory(client))
	}
	if got := holdStatus(t, db, txnID); got != "FAILED" {
		t.Fatalf("late capture changed state: %s", got)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM ledger WHERE txn_id=$1 AND event_type='late_capture'`, txnID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("late ledger=%d", count)
	}
	if strings.Count(logs.String(), "captured after FAILED") != 1 {
		t.Fatalf("late alert missing or duplicated: %s", logs.String())
	}
	if err := db.QueryRow(`SELECT count(*) FROM outbox WHERE txn_id=$1`, txnID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("late capture added callback: %d", count)
	}
}

func sendRazorpayWebhook(t *testing.T, handler http.Handler, event, txnID string, valid bool) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"event": event, "payload": map[string]any{"payment": map[string]any{"entity": map[string]any{"id": "pay_test", "entity": "payment", "order_id": txnID, "amount": 49900, "currency": "INR", "status": "captured"}}}})
	if event == "malformed" {
		body = []byte(`{broken`)
	}
	mac := hmac.New(sha256.New, []byte("test-webhook-secret"))
	_, _ = mac.Write(body)
	sig := hex.EncodeToString(mac.Sum(nil))
	if !valid {
		sig = strings.Repeat("0", 64)
	}
	r := httptest.NewRequest("POST", "/webhooks/razorpay", bytes.NewReader(body))
	r.SetPathValue("gateway", "razorpay")
	r.Header.Set("X-Razorpay-Signature", sig)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("webhook=%d: %s", w.Code, w.Body.String())
	}
}

func TestRazorpayWebhookBeforeHoldAndDuplicateCallback(t *testing.T) {
	db := razorpayDB(t)
	txnID := "order_early"
	handler := webhook.NewHandler(db, &config.Config{Gateway: "razorpay", WebhookSecret: "test-webhook-secret"})
	sendRazorpayWebhook(t, handler, "payment.captured", txnID, true)
	sendRazorpayWebhook(t, handler, "payment.captured", txnID, true)
	var callbacks atomic.Int32
	merchant := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !delivery.Verify(body, r.Header.Get("X-Paystable-Signature"), r.Header.Get("X-Paystable-Idempotency-Key"), r.Header.Get("X-Paystable-Timestamp"), "test-callback-secret") {
			t.Error("callback signature invalid")
		}
		callbacks.Add(1)
	}))
	defer merchant.Close()
	if _, err := hold.NewStore(db).Create(txnID, "razorpay", merchant.URL, "INR", 49900, 300, nil); err != nil {
		t.Fatal(err)
	}
	var polls int
	if err := db.QueryRow(`SELECT count(*) FROM verification_polls WHERE txn_id=$1`, txnID).Scan(&polls); err != nil {
		t.Fatal(err)
	}
	if polls != 1 {
		t.Fatalf("early/duplicate polls=%d", polls)
	}
	client := razorpayAPI(t, txnID, []string{"captured"}, 49900, false, false)
	for i := 1; i <= 3; i++ {
		id := seedPoll(t, db, txnID, i)
		processPoll(context.Background(), db, defaultCfg(), NewLagEstimator(), pollRow(id, txnID, i, "razorpay", 49900), factory(client))
	}
	sendRazorpayWebhook(t, handler, "payment.captured", txnID, true)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		delivery.Run(ctx, db, delivery.Config{CallbackSecret: "test-callback-secret", AllowInsecure: true, TimeoutS: 2, WorkerConcurrency: 1})
	}()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM outbox WHERE txn_id=$1 AND status='delivered'`, txnID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if callbacks.Load() != 1 {
		t.Fatalf("callbacks=%d", callbacks.Load())
	}
	var events int
	if err := db.QueryRow(`SELECT count(*) FROM outbox WHERE txn_id=$1`, txnID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 1 {
		t.Fatalf("outbox=%d", events)
	}
}

func TestRazorpayIgnoredEventsAndVerificationOrder(t *testing.T) {
	db := razorpayDB(t)
	handler := webhook.NewHandler(db, &config.Config{Gateway: "razorpay", WebhookSecret: "test-webhook-secret"})
	sendRazorpayWebhook(t, handler, "unknown.event", "order_ignored", true)
	sendRazorpayWebhook(t, handler, "unknown.event", "", true)
	if _, err := hold.NewStore(db).Create("order_ignored", "razorpay", "http://localhost/cb", "INR", 49900, 300, nil); err != nil {
		t.Fatal(err)
	}
	var polls, events int
	if err := db.QueryRow(`SELECT count(*) FROM verification_polls`).Scan(&polls); err != nil {
		t.Fatal(err)
	}
	if polls != 0 {
		t.Fatal("unknown event scheduled a poll")
	}
	if err := db.QueryRow(`SELECT count(*) FROM webhooks WHERE NOT actionable`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 2 {
		t.Fatalf("stored unknown events=%d", events)
	}
	sendRazorpayWebhook(t, handler, "malformed", "", false)
	var reason string
	if err := db.QueryRow(`SELECT rejection_reason FROM webhooks_rejected`).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if reason != "hmac_mismatch" {
		t.Fatalf("JSON decoded before signature: %s", reason)
	}
}

func TestRazorpayAmountAndReviewStates(t *testing.T) {
	for _, tc := range []struct {
		name            string
		amount          int64
		partial, refund bool
		currency, want  string
	}{
		{"mismatch", 25000, false, false, "INR", "MISMATCH"},
		{"partial", 49900, true, false, "INR", "MISMATCH"},
		{"refund", 49900, false, true, "INR", "INDETERMINATE"},
		{"currency", 49900, false, false, "USD", "INDETERMINATE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := razorpayDB(t)
			txnID := "order_review"
			seedHold(t, db, txnID, "razorpay", 49900)
			if _, err := db.Exec(`UPDATE holds SET currency=$2 WHERE txn_id=$1`, txnID, tc.currency); err != nil {
				t.Fatal(err)
			}
			client := razorpayAPI(t, txnID, []string{"captured"}, tc.amount, tc.partial, tc.refund)
			id := seedPoll(t, db, txnID, 1)
			processPoll(context.Background(), db, defaultCfg(), NewLagEstimator(), pollRow(id, txnID, 1, "razorpay", 49900), factory(client))
			if got := holdStatus(t, db, txnID); got != tc.want {
				t.Fatalf("%s, want %s", got, tc.want)
			}
		})
	}
}

func TestRazorpayConcurrentWebhookAndHold(t *testing.T) {
	db := razorpayDB(t)
	handler := webhook.NewHandler(db, &config.Config{Gateway: "razorpay", WebhookSecret: "test-webhook-secret"})
	for i := 0; i < 8; i++ {
		txnID := fmt.Sprintf("order_concurrent%d", i)
		done := make(chan error, 1)
		go func() {
			_, err := hold.NewStore(db).Create(txnID, "razorpay", "http://localhost/cb", "INR", 49900, 300, nil)
			done <- err
		}()
		sendRazorpayWebhook(t, handler, "payment.captured", txnID, true)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM verification_polls WHERE txn_id=$1`, txnID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("lost or duplicate initial poll: %d", n)
		}
	}
}
