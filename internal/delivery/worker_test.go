package delivery

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("db.Ping: %v", err)
	}
	t.Cleanup(func() { db.Close() }) //nolint:errcheck
	return db
}

func seedOutboxRow(t *testing.T, db *sql.DB, txnID, callbackURL string) int64 {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO holds (txn_id, gateway, status, amount, currency, read_token,
		                   callback_url, ttl_seconds, expires_at)
		VALUES ($1, 'payu', 'CONFIRMED', 49900, 'INR', $2, $3, 300, now()+interval '5m')
		ON CONFLICT (txn_id) DO UPDATE SET callback_url=$3`,
		txnID, "tok_"+txnID, callbackURL)
	if err != nil {
		t.Fatalf("seed hold: %v", err)
	}

	payload, _ := json.Marshal(map[string]string{"txn_id": txnID, "status": "CONFIRMED"})
	var id int64
	err = db.QueryRow(`
		INSERT INTO outbox (txn_id, event_type, payload, idempotency_key, status, next_attempt_at)
		VALUES ($1, 'transaction.confirmed', $2::jsonb, $3, 'pending', now())
		RETURNING id`,
		txnID, payload, "evt_"+txnID+"_CONFIRMED").Scan(&id)
	if err != nil {
		t.Fatalf("seed outbox: %v", err)
	}

	t.Cleanup(func() {
		db.Exec("DELETE FROM ledger WHERE txn_id=$1", txnID) //nolint:errcheck
		db.Exec("DELETE FROM outbox WHERE txn_id=$1", txnID) //nolint:errcheck
		db.Exec("DELETE FROM holds WHERE txn_id=$1", txnID)  //nolint:errcheck
	})
	return id
}

func outboxStatus(t *testing.T, db *sql.DB, id int64) string {
	t.Helper()
	var s string
	db.QueryRow("SELECT status FROM outbox WHERE id=$1", id).Scan(&s) //nolint:errcheck
	return s
}

func TestDeliver_Success(t *testing.T) {
	db := openTestDB(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := Config{CallbackSecret: "secret", AllowInsecure: true, TimeoutS: 5, WorkerConcurrency: 1}
	txnID := "test-deliver-success-" + strconv.Itoa(int(time.Now().UnixNano()))
	id := seedOutboxRow(t, db, txnID, server.URL)

	rows, err := claimBatch(context.Background(), db)
	if err != nil || len(rows) == 0 {
		t.Fatalf("claimBatch: %v, rows: %v", err, rows)
	}
	var row outboxRow
	for _, r := range rows {
		if r.ID == id {
			row = r
			break
		}
	}
	if row.ID == 0 {
		t.Fatal("seeded row not in claimed batch")
	}

	deliver(context.Background(), db, cfg, row)

	if got := outboxStatus(t, db, id); got != "delivered" {
		t.Errorf("status = %q, want delivered", got)
	}
}

func TestDeliver_Transient5xx_Reschedules(t *testing.T) {
	db := openTestDB(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	cfg := Config{CallbackSecret: "secret", AllowInsecure: true, TimeoutS: 5, WorkerConcurrency: 1}
	txnID := "test-transient-" + strconv.Itoa(int(time.Now().UnixNano()))
	id := seedOutboxRow(t, db, txnID, server.URL)

	rows, _ := claimBatch(context.Background(), db)
	var row outboxRow
	for _, r := range rows {
		if r.ID == id {
			row = r
		}
	}

	deliver(context.Background(), db, cfg, row)

	if got := outboxStatus(t, db, id); got != "pending" {
		t.Errorf("status = %q, want pending (rescheduled)", got)
	}
}

func TestDeliver_Permanent4xx_Exhausts(t *testing.T) {
	db := openTestDB(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	cfg := Config{CallbackSecret: "secret", AllowInsecure: true, TimeoutS: 5, WorkerConcurrency: 1}
	txnID := "test-4xx-" + strconv.Itoa(int(time.Now().UnixNano()))
	id := seedOutboxRow(t, db, txnID, server.URL)

	rows, _ := claimBatch(context.Background(), db)
	var row outboxRow
	for _, r := range rows {
		if r.ID == id {
			row = r
		}
	}

	deliver(context.Background(), db, cfg, row)

	if got := outboxStatus(t, db, id); got != "exhausted" {
		t.Errorf("status = %q, want exhausted", got)
	}
}

func TestDeliver_SignaturePresent(t *testing.T) {
	db := openTestDB(t)

	var gotHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := Config{CallbackSecret: "mysecret", AllowInsecure: true, TimeoutS: 5, WorkerConcurrency: 1}
	txnID := "test-sig-" + strconv.Itoa(int(time.Now().UnixNano()))
	id := seedOutboxRow(t, db, txnID, server.URL)

	rows, _ := claimBatch(context.Background(), db)
	var row outboxRow
	for _, r := range rows {
		if r.ID == id {
			row = r
		}
	}

	deliver(context.Background(), db, cfg, row)

	if gotHeaders.Get("X-Paystable-Idempotency-Key") != row.IdempotencyKey {
		t.Fatal("callback event key changed")
	}
	if !Verify(row.Payload, gotHeaders.Get("X-Paystable-Signature"), gotHeaders.Get("X-Paystable-Idempotency-Key"), gotHeaders.Get("X-Paystable-Timestamp"), "mysecret") {
		t.Errorf("signature verification failed, got %q", gotHeaders.Get("X-Paystable-Signature"))
	}
}

func TestNextDelay_Bounds(t *testing.T) {
	for attempt := 1; attempt <= 8; attempt++ {
		d := nextDelay(attempt)
		if d <= 0 {
			t.Errorf("attempt %d: delay = %v, want > 0", attempt, d)
		}
		if d > 24*time.Hour {
			t.Errorf("attempt %d: delay = %v, want <= 24h", attempt, d)
		}
	}
}
