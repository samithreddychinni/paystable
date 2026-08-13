package adminapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResolveReview_WritesResolutionAndLedger(t *testing.T) {
	db := openAdminTestDB(t)
	txnID := seedHoldForAdmin(t, db, "MISMATCH", 5000)
	h := newTestHandler(db)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/reviews/"+txnID,
		bytes.NewBufferString(`{"resolution":"no_action","operator":"sam","note":"verified with merchant"}`))
	req.SetPathValue("id", txnID)
	w := httptest.NewRecorder()
	h.resolveReview(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resolution, operator, note string
	if err := db.QueryRow(`SELECT resolution, operator, note FROM manual_reviews WHERE txn_id=$1`, txnID).
		Scan(&resolution, &operator, &note); err != nil {
		t.Fatalf("load review: %v", err)
	}
	if resolution != "no_action" || operator != "sam" || note != "verified with merchant" {
		t.Fatalf("review = (%q, %q, %q)", resolution, operator, note)
	}

	var detail []byte
	if err := db.QueryRow(`SELECT detail FROM ledger WHERE txn_id=$1 AND event_type='manual_review'`, txnID).Scan(&detail); err != nil {
		t.Fatalf("load ledger entry: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(detail, &got); err != nil {
		t.Fatalf("decode ledger detail: %v", err)
	}
	if got["operator"] != "sam" || got["note"] != "verified with merchant" {
		t.Fatalf("ledger detail = %#v", got)
	}
	var deliveries int
	if err := db.QueryRow(`SELECT count(*) FROM outbox WHERE txn_id=$1`, txnID).Scan(&deliveries); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if deliveries != 0 {
		t.Fatalf("no_action queued %d deliveries, want 0", deliveries)
	}
}

func TestResolveReview_QueuesFinalCallbackOnce(t *testing.T) {
	for _, resolution := range []string{"confirmed", "failed"} {
		t.Run(resolution, func(t *testing.T) {
			db := openAdminTestDB(t)
			txnID := seedHoldForAdmin(t, db, "INDETERMINATE", 5000)
			h := newTestHandler(db)
			body := `{"resolution":"` + resolution + `","operator":"sam","note":"checked gateway"}`

			for attempt := 0; attempt < 2; attempt++ {
				req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/reviews/"+txnID, bytes.NewBufferString(body))
				req.SetPathValue("id", txnID)
				w := httptest.NewRecorder()
				h.resolveReview(w, req)
				if w.Code != http.StatusOK {
					t.Fatalf("attempt %d status = %d: %s", attempt+1, w.Code, w.Body.String())
				}
			}

			var eventType, idempotencyKey, state string
			var payload []byte
			if err := db.QueryRow(`
				SELECT event_type, payload, idempotency_key, status
				FROM outbox WHERE txn_id=$1`, txnID).
				Scan(&eventType, &payload, &idempotencyKey, &state); err != nil {
				t.Fatalf("load outbox: %v", err)
			}
			wantEvent := "transaction." + map[string]string{"confirmed": "CONFIRMED", "failed": "FAILED"}[resolution]
			if eventType != wantEvent || idempotencyKey != "evt_"+txnID+"_MANUAL_REVIEW" || state != "pending" {
				t.Fatalf("outbox = (%q, %q, %q), want (%q, manual-review key, pending)", eventType, idempotencyKey, state, wantEvent)
			}
			var callback map[string]any
			if err := json.Unmarshal(payload, &callback); err != nil {
				t.Fatalf("decode callback: %v", err)
			}
			if callback["event"] != "transaction."+resolution || callback["status"] != map[string]string{"confirmed": "CONFIRMED", "failed": "FAILED"}[resolution] || callback["resolution_source"] != "manual_review" {
				t.Fatalf("callback = %#v", callback)
			}

			var outboxCount, ledgerCount int
			if err := db.QueryRow(`SELECT count(*) FROM outbox WHERE txn_id=$1`, txnID).Scan(&outboxCount); err != nil {
				t.Fatalf("count outbox: %v", err)
			}
			if err := db.QueryRow(`SELECT count(*) FROM ledger WHERE txn_id=$1 AND event_type='manual_review'`, txnID).Scan(&ledgerCount); err != nil {
				t.Fatalf("count ledger: %v", err)
			}
			if outboxCount != 1 || ledgerCount != 1 {
				t.Fatalf("counts = (outbox %d, ledger %d), want (1, 1)", outboxCount, ledgerCount)
			}
		})
	}
}

func TestResolveReview_RejectsChangedDecision(t *testing.T) {
	db := openAdminTestDB(t)
	txnID := seedHoldForAdmin(t, db, "MISMATCH", 5000)
	h := newTestHandler(db)

	for i, body := range []string{
		`{"resolution":"confirmed","operator":"sam","note":"checked gateway"}`,
		`{"resolution":"failed","operator":"sam","note":"changed mind"}`,
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/reviews/"+txnID, bytes.NewBufferString(body))
		req.SetPathValue("id", txnID)
		w := httptest.NewRecorder()
		h.resolveReview(w, req)
		want := []int{http.StatusOK, http.StatusConflict}[i]
		if w.Code != want {
			t.Fatalf("attempt %d status = %d, want %d: %s", i+1, w.Code, want, w.Body.String())
		}
	}
}

func TestReviews_ShowsDeliveryState(t *testing.T) {
	db := openAdminTestDB(t)
	txnID := seedHoldForAdmin(t, db, "MISMATCH", 5000)
	h := newTestHandler(db)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/reviews/"+txnID,
		bytes.NewBufferString(`{"resolution":"confirmed","operator":"sam","note":"checked gateway"}`))
	req.SetPathValue("id", txnID)
	h.resolveReview(httptest.NewRecorder(), req)

	w := httptest.NewRecorder()
	h.reviews(w, httptest.NewRequest(http.MethodGet, "/api/v1/admin/reviews", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var response struct {
		Data []struct {
			TxnID         string `json:"txn_id"`
			Resolution    string `json:"resolution"`
			DeliveryState string `json:"delivery_state"`
			DeliveryID    int64  `json:"delivery_id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, review := range response.Data {
		if review.TxnID == txnID {
			if review.Resolution != "confirmed" || review.DeliveryState != "pending" || review.DeliveryID == 0 {
				t.Fatalf("review = %#v", review)
			}
			return
		}
	}
	t.Fatalf("review %q missing from response", txnID)
}
