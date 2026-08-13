package adminapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"database/sql"

	"github.com/IDEA-Amrita/paystable/internal/config"
)

func openAdminTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("ping: %v", err)
	}
	t.Cleanup(func() { db.Close() }) //nolint:errcheck
	return db
}

func seedHoldForAdmin(t *testing.T, db *sql.DB, status string, amount int64) string {
	t.Helper()
	txnID := fmt.Sprintf("admin-%d", time.Now().UnixNano())
	_, err := db.Exec(`
		INSERT INTO holds (txn_id, gateway, status, amount, currency, read_token,
		                   callback_url, ttl_seconds, expires_at)
		VALUES ($1, 'payu', $2, $3, 'INR', $4, 'http://cb.test/cb', 300, now()+interval '5m')`,
		txnID, status, amount, "tok_"+txnID)
	if err != nil {
		t.Fatalf("seed hold: %v", err)
	}
	t.Cleanup(func() {
		db.Exec("DELETE FROM outbox WHERE txn_id=$1", txnID)             //nolint:errcheck
		db.Exec("DELETE FROM ledger WHERE txn_id=$1", txnID)             //nolint:errcheck
		db.Exec("DELETE FROM manual_reviews WHERE txn_id=$1", txnID)     //nolint:errcheck
		db.Exec("DELETE FROM verification_polls WHERE txn_id=$1", txnID) //nolint:errcheck
		db.Exec("DELETE FROM holds WHERE txn_id=$1", txnID)              //nolint:errcheck
	})
	return txnID
}

func seedOutboxExhausted(t *testing.T, db *sql.DB, txnID string) int64 {
	t.Helper()
	idem := "evt_" + txnID + "_CONFIRMED"
	var id int64
	err := db.QueryRow(`
		INSERT INTO outbox (txn_id, event_type, payload, idempotency_key, status, attempts, max_attempts)
		VALUES ($1, 'transaction.confirmed', '{"txn_id":"x"}'::jsonb, $2, 'exhausted', 8, 8)
		RETURNING id`, txnID, idem).Scan(&id)
	if err != nil {
		t.Fatalf("seed outbox: %v", err)
	}
	return id
}

func newTestHandler(db *sql.DB) *Handler {
	return &Handler{db: db, cfg: &config.Config{
		Gateway: "payu", StabilizationN: 3, MaxBackoffS: 160,
		HoldMaxTTLS: 900, LogLevel: "info",
		WebhookSecret: "test", GatewayAPIKey: "test", MerchantCallbackSecret: "test",
	}}
}

func loopbackReq(method, target string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	req.RemoteAddr = "127.0.0.1:9999"
	return req
}

func TestOverviewStats_Returns200(t *testing.T) {
	db := openAdminTestDB(t)
	h := newTestHandler(db)

	req := loopbackReq("GET", "/api/v1/admin/overview/stats")
	w := httptest.NewRecorder()
	h.overviewStats(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, key := range []string{"active_holds", "pending_deliveries", "exhausted_deliveries", "rejected_webhooks"} {
		if _, ok := resp[key]; !ok {
			t.Errorf("missing key %q", key)
		}
	}
}

func TestTransactions_EmptySearch(t *testing.T) {
	db := openAdminTestDB(t)
	h := newTestHandler(db)

	req := loopbackReq("GET", "/api/v1/admin/transactions")
	w := httptest.NewRecorder()
	h.transactions(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp) //nolint:errcheck
	if _, ok := resp["data"]; !ok {
		t.Error("missing data key")
	}
}

func TestTransactions_FilterByStatus(t *testing.T) {
	db := openAdminTestDB(t)
	seedHoldForAdmin(t, db, "CONFIRMED", 49900)
	h := newTestHandler(db)

	req := loopbackReq("GET", "/api/v1/admin/transactions?status=CONFIRMED")
	w := httptest.NewRecorder()
	h.transactions(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Data  []map[string]any `json:"data"`
		Total int              `json:"total"`
	}
	json.NewDecoder(w.Body).Decode(&resp) //nolint:errcheck
	if resp.Total == 0 {
		t.Error("expected at least one CONFIRMED transaction")
	}
	for _, row := range resp.Data {
		if row["status"] != "CONFIRMED" {
			t.Errorf("got status %q in CONFIRMED filter results", row["status"])
		}
	}
}

func TestTransactionDetail_NotFound(t *testing.T) {
	db := openAdminTestDB(t)
	h := newTestHandler(db)

	req := loopbackReq("GET", "/api/v1/admin/transactions/nonexistent-txn")
	req.SetPathValue("id", "nonexistent-txn")
	w := httptest.NewRecorder()
	h.transactionDetail(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestTransactionDetail_Exists(t *testing.T) {
	db := openAdminTestDB(t)
	txnID := seedHoldForAdmin(t, db, "CONFIRMED", 49900)
	h := newTestHandler(db)

	req := loopbackReq("GET", "/api/v1/admin/transactions/"+txnID)
	req.SetPathValue("id", txnID)
	w := httptest.NewRecorder()
	h.transactionDetail(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp) //nolint:errcheck
	if resp["txn_id"] != txnID {
		t.Errorf("txn_id = %v, want %s", resp["txn_id"], txnID)
	}
}

func TestTransactionDetail_MergedTimelineEvidence(t *testing.T) {
	db := openAdminTestDB(t)
	txnID := fmt.Sprintf("admin-timeline-%d", time.Now().UnixNano())
	readToken := "tok_" + txnID
	base := time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC)
	rejectedBody := []byte("txnid=" + txnID + "&status=failure&hash=bad-signature")

	t.Cleanup(func() {
		db.Exec("DELETE FROM outbox WHERE txn_id=$1", txnID)                     //nolint:errcheck
		db.Exec("DELETE FROM ledger WHERE txn_id=$1", txnID)                     //nolint:errcheck
		db.Exec("DELETE FROM verification_polls WHERE txn_id=$1", txnID)         //nolint:errcheck
		db.Exec("DELETE FROM webhooks WHERE txn_id=$1", txnID)                   //nolint:errcheck
		db.Exec("DELETE FROM webhooks_rejected WHERE raw_body=$1", rejectedBody) //nolint:errcheck
		db.Exec("DELETE FROM holds WHERE txn_id=$1", txnID)                      //nolint:errcheck
	})

	if _, err := db.Exec(`
		INSERT INTO holds (txn_id, gateway, status, amount, currency, read_token,
		                   callback_url, ttl_seconds, expires_at, created_at, updated_at)
		VALUES ($1, 'payu', 'CONFIRMED', 49900, 'INR', $2,
		        'http://cb.test/cb', 300, $3, $4, $5)`,
		txnID, readToken, base.Add(5*time.Minute), base, base.Add(6*time.Second)); err != nil {
		t.Fatalf("seed hold: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO webhooks (txn_id, gateway, gateway_event_id, event_type, payload, received_at)
		VALUES ($1, 'payu', $2, 'payment.failed',
		        '{"status":"failed","amount":"49900","hash":"signed-payload-field"}'::jsonb, $3)`,
		txnID, "mih_"+txnID, base.Add(1*time.Second)); err != nil {
		t.Fatalf("seed webhook: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO webhooks_rejected (gateway, rejection_reason, headers, raw_body, source_ip, received_at)
		VALUES ('payu', 'hmac_mismatch', '{}'::jsonb, $1, '127.0.0.1', $2)`,
		rejectedBody, base.Add(1500*time.Millisecond)); err != nil {
		t.Fatalf("seed rejected webhook: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO verification_polls
			(txn_id, attempt_number, status, gateway_status, gateway_amount,
			 scheduled_at, started_at, completed_at)
		VALUES
			($1, 1, 'completed', 'pending', 49900, $2, $3, $4),
			($1, 2, 'failed', NULL, NULL, $5, $6, $7)`,
		txnID,
		base.Add(2*time.Second), base.Add(2100*time.Millisecond), base.Add(2200*time.Millisecond),
		base.Add(3*time.Second), base.Add(3100*time.Millisecond), base.Add(3200*time.Millisecond)); err != nil {
		t.Fatalf("seed polls: %v", err)
	}
	if _, err := db.Exec(`
		UPDATE verification_polls
		SET error='gateway timeout'
		WHERE txn_id=$1 AND attempt_number=2`, txnID); err != nil {
		t.Fatalf("seed failed poll error: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO ledger (txn_id, event_type, source, from_status, to_status, detail, created_at)
		VALUES ($1, 'state_transition', 'stabilizer', 'VERIFYING', 'CONFIRMED',
		        '{"reason":"gateway_verified"}'::jsonb, $2)`,
		txnID, base.Add(4*time.Second)); err != nil {
		t.Fatalf("seed ledger: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO outbox
			(txn_id, event_type, payload, idempotency_key, status, attempts, max_attempts,
			 last_http_status, delivered_at, last_attempt_at, created_at)
		VALUES
			($1, 'transaction.confirmed', '{"txn_id":"x"}'::jsonb, $2, 'delivered', 1, 8,
			 204, $3, $4, $5)`,
		txnID, "idem_"+txnID, base.Add(5*time.Second), base.Add(4900*time.Millisecond), base); err != nil {
		t.Fatalf("seed outbox: %v", err)
	}

	h := newTestHandler(db)
	req := loopbackReq("GET", "/api/v1/admin/transactions/"+txnID)
	req.SetPathValue("id", txnID)
	w := httptest.NewRecorder()
	h.transactionDetail(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200. body: %s", w.Code, strings.TrimSpace(w.Body.String()))
	}

	var resp struct {
		Events []struct {
			Type    string         `json:"type"`
			Source  string         `json:"source"`
			Detail  string         `json:"detail"`
			Attempt int            `json:"attempt"`
			Data    map[string]any `json:"data"`
		} `json:"events"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	wantTypes := []string{
		"hold_created",
		"webhook_received",
		"webhook_rejected",
		"poll_completed",
		"poll_failed",
		"state_transition",
		"callback_delivered",
	}
	if len(resp.Events) != len(wantTypes) {
		t.Fatalf("events len = %d, want %d: %#v", len(resp.Events), len(wantTypes), resp.Events)
	}
	for i, want := range wantTypes {
		if resp.Events[i].Type != want {
			t.Fatalf("event[%d].type = %q, want %q; events=%#v", i, resp.Events[i].Type, want, resp.Events)
		}
	}
	if resp.Events[1].Data["event_type"] != "payment.failed" {
		t.Errorf("webhook event_type = %v, want payment.failed", resp.Events[1].Data["event_type"])
	}
	if _, leaked := resp.Events[1].Data["hash"]; leaked {
		t.Error("webhook timeline data leaked signed payload hash")
	}
	if resp.Events[2].Data["rejection_reason"] != "hmac_mismatch" {
		t.Errorf("rejected reason = %v, want hmac_mismatch", resp.Events[2].Data["rejection_reason"])
	}
	if resp.Events[3].Attempt != 1 || resp.Events[3].Data["gateway_status"] != "pending" {
		t.Errorf("completed poll event = %#v, want attempt 1 pending", resp.Events[3])
	}
	if resp.Events[4].Attempt != 2 || resp.Events[4].Data["error"] != "gateway timeout" {
		t.Errorf("failed poll event = %#v, want attempt 2 with error", resp.Events[4])
	}
	if resp.Events[5].Data["from"] != "VERIFYING" || resp.Events[5].Data["to"] != "CONFIRMED" {
		t.Errorf("transition data = %#v, want VERIFYING -> CONFIRMED", resp.Events[5].Data)
	}
	if resp.Events[6].Data["callback_http_status"] != float64(204) {
		t.Errorf("callback_http_status = %v, want 204", resp.Events[6].Data["callback_http_status"])
	}
}

func TestDeliveries_ExhaustedList(t *testing.T) {
	db := openAdminTestDB(t)
	txnID := seedHoldForAdmin(t, db, "CONFIRMED", 49900)
	seedOutboxExhausted(t, db, txnID)
	h := newTestHandler(db)

	req := loopbackReq("GET", "/api/v1/admin/deliveries?status=exhausted")
	w := httptest.NewRecorder()
	h.deliveries(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Data  []map[string]any `json:"data"`
		Total int              `json:"total"`
	}
	json.NewDecoder(w.Body).Decode(&resp) //nolint:errcheck
	if resp.Total == 0 {
		t.Error("expected at least one exhausted delivery")
	}
}

func TestReplayDelivery_ResetsToRetry(t *testing.T) {
	db := openAdminTestDB(t)
	txnID := seedHoldForAdmin(t, db, "CONFIRMED", 49900)
	id := seedOutboxExhausted(t, db, txnID)
	h := newTestHandler(db)

	req := loopbackReq("POST", "/api/v1/admin/deliveries/"+strconv.FormatInt(id, 10)+"/replay")
	req.SetPathValue("id", strconv.FormatInt(id, 10))
	w := httptest.NewRecorder()
	h.replayDelivery(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var status string
	db.QueryRow("SELECT status FROM outbox WHERE id=$1", id).Scan(&status) //nolint:errcheck
	if status != "pending" {
		t.Errorf("after replay: status = %q, want pending", status)
	}
}

func TestReplayDelivery_NotFound(t *testing.T) {
	db := openAdminTestDB(t)
	h := newTestHandler(db)

	req := loopbackReq("POST", "/api/v1/admin/deliveries/99999999/replay")
	req.SetPathValue("id", "99999999")
	w := httptest.NewRecorder()
	h.replayDelivery(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestConfig_ReturnsRealValues(t *testing.T) {
	db := openAdminTestDB(t)
	h := newTestHandler(db)

	req := loopbackReq("GET", "/api/v1/admin/config")
	w := httptest.NewRecorder()
	h.config(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp []map[string]any
	json.NewDecoder(w.Body).Decode(&resp) //nolint:errcheck
	found := false
	for _, item := range resp {
		if item["key"] == "GATEWAY" && item["value"] == "payu" {
			found = true
		}
	}
	if !found {
		t.Error("expected GATEWAY=payu in config response")
	}
}

func TestUpdateConfig_ReturnsMethodNotAllowed(t *testing.T) {
	db := openAdminTestDB(t)
	h := newTestHandler(db)

	req := loopbackReq("POST", "/api/v1/admin/config")
	w := httptest.NewRecorder()
	h.configReadOnly(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp) //nolint:errcheck
	if _, ok := resp["error"]; !ok {
		t.Error("expected an error message explaining config is read-only")
	}
}

func TestMismatchStats_Returns200(t *testing.T) {
	db := openAdminTestDB(t)
	h := newTestHandler(db)

	req := loopbackReq("GET", "/api/v1/admin/mismatches/stats")
	w := httptest.NewRecorder()
	h.mismatchStats(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp) //nolint:errcheck
	if _, ok := resp["last_7_days"]; !ok {
		t.Error("missing last_7_days key")
	}
}

func TestLocalhostGate_BlocksNonLoopback(t *testing.T) {
	db := openAdminTestDB(t)
	h := newTestHandler(db)
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest("GET", "/api/v1/admin/config", nil)
	req.RemoteAddr = "203.0.113.42:1234"
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("external IP: got %d, want 403", w.Code)
	}
}

func TestMismatches_Returns200(t *testing.T) {
	db := openAdminTestDB(t)
	h := newTestHandler(db)

	// seed a mismatch: hold CONFIRMED, webhook said failure
	txnID := seedHoldForAdmin(t, db, "CONFIRMED", 49900)
	db.Exec(`INSERT INTO webhooks (txn_id, gateway, event_type, payload) VALUES ($1,'payu','payment.failed','"{}"'::jsonb)`, txnID) //nolint:errcheck

	req := loopbackReq("GET", "/api/v1/admin/mismatches")
	w := httptest.NewRecorder()
	h.mismatches(w, req)

	if w.Code != http.StatusOK {
		body := w.Body.String()
		t.Fatalf("status = %d, want 200. body: %s", w.Code, strings.TrimSpace(body))
	}
}
