package stabilizer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"log/slog"

	"github.com/IDEA-Amrita/paystable/internal/alert"
	"github.com/IDEA-Amrita/paystable/internal/config"
	"github.com/IDEA-Amrita/paystable/internal/gateway"
	"github.com/IDEA-Amrita/paystable/internal/metrics"
)

// 1)Main stabilizer loop
// A)Run starts the stabilizer worker loop. clientFactory should return a GatewayClient for the given gateway name.
func Run(ctx context.Context, db *sql.DB, cfg *config.Config, lag *LagEstimator, clientFactory func(string) gateway.GatewayClient) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	sem := make(chan struct{}, 20) // 1.6: limit to max 20 concurrent polls globally

	for {
		select {
		case <-ctx.Done(): // the caller asked us to stop → log and exit.
			slog.Info("stabilizer stopping")
			return
		case <-ticker.C: // a tick arrived → we run the polling cycle.
		}

		// 1.2: wrap batch query and in_flight update in a single transaction
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			slog.Error("stabilizer: begin tx failed", "error", err)
			continue
		}

		// B)Pull a small batch of pending polls - grabs up to 10 rows whose status='pending' and scheduled_at <= now().
		// 2.2: scope lock with FOR UPDATE OF vp to avoid locking holds table unnecessarily
		rows, err := tx.QueryContext(ctx, `
		SELECT vp.id, vp.txn_id, vp.attempt_number, h.gateway, h.amount, h.created_at
		FROM verification_polls vp
		JOIN holds h ON vp.txn_id = h.txn_id
		WHERE vp.status = 'pending' AND vp.scheduled_at <= now()
		ORDER BY vp.scheduled_at
		LIMIT 10
		FOR UPDATE OF vp SKIP LOCKED
		`)
		if err != nil {
			slog.Error("stabilizer: fetch pending polls failed", "error", err)
			_ = tx.Rollback()
			continue
		}

		// C)Scan rows into a slice - iterates and updates recent poll results in polls
		var polls []struct {
			ID         int64
			TxnID      string
			Attempt    int
			Gateway    string
			HoldAmount int64
			CreatedAt  time.Time
		}
		for rows.Next() {
			var p struct {
				ID         int64
				TxnID      string
				Attempt    int
				Gateway    string
				HoldAmount int64
				CreatedAt  time.Time
			}
			if err := rows.Scan(&p.ID, &p.TxnID, &p.Attempt, &p.Gateway, &p.HoldAmount, &p.CreatedAt); err != nil {
				slog.Error("stabilizer: scan poll failed", "error", err)
				continue
			}
			polls = append(polls, p)
		}
		if err := rows.Close(); err != nil {
			_ = tx.Rollback()
			slog.Error("stabilizer: close poll rows failed", "error", err)
			continue
		}
		if err := rows.Err(); err != nil {
			_ = tx.Rollback()
			slog.Error("stabilizer: poll rows failed", "error", err)
			continue
		}

		if len(polls) == 0 {
			_ = tx.Rollback()
			continue
		}

		// 1.2: mark status='in_flight' and set started_at=now() inside the same transaction
		var updateErr error
		for _, p := range polls {
			if _, err := tx.ExecContext(ctx, `UPDATE verification_polls SET status='in_flight', started_at=now() WHERE id=$1`, p.ID); err != nil {
				updateErr = err
				break
			}
		}
		if updateErr != nil {
			slog.Error("stabilizer: update status to in_flight failed", "error", updateErr)
			_ = tx.Rollback()
			continue
		}

		if err := tx.Commit(); err != nil {
			slog.Error("stabilizer: commit transaction failed", "error", err)
			continue
		}

		for _, p := range polls {
			p := p
			// 1.6: Acquire semaphore slot before spawning goroutine
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				slog.Info("stabilizer stopping during semaphore acquire")
				return
			}

			// Launch a goroutine per poll for concurrency, isolation and throughput
			go func() {
				defer func() { <-sem }()
				processPoll(ctx, db, cfg, lag, p, clientFactory)
			}()
		}
	}
}

// 2)core per‑poll logic
func processPoll(ctx context.Context, db *sql.DB, cfg *config.Config, lag *LagEstimator, p struct {
	ID         int64
	TxnID      string
	Attempt    int
	Gateway    string
	HoldAmount int64
	CreatedAt  time.Time
}, clientFactory func(string) gateway.GatewayClient) {
	// A)Rate‑limit via token bucket
	acquired, nextAvailable, err := AcquireToken(ctx, db, p.Gateway, 10, 1.0)
	if err != nil {
		slog.Error("AcquireToken error", "error", err, "gateway", p.Gateway)
		// 1.2 safety: reset status back to pending on error so it isn't stuck in_flight
		if _, err2 := db.ExecContext(ctx, `UPDATE verification_polls SET status='pending', error=$1 WHERE id=$2`, "acquire_token_error: "+err.Error(), p.ID); err2 != nil {
			slog.Error("reset poll status to pending failed", "error", err2, "id", p.ID)
		}
		return
	}
	if !acquired {
		jitter := time.Duration(rand.Int63n(int64(time.Second)))
		scheduled := nextAvailable.Add(jitter)
		// 1.2 safety: update status back to pending since it was marked in_flight in Run()
		if _, err := db.ExecContext(ctx, `UPDATE verification_polls SET scheduled_at=$1, error=$2, status='pending' WHERE id=$3`, scheduled, "rate_limited", p.ID); err != nil {
			slog.Error("reschedule rate limited", "error", err, "id", p.ID)
		}
		return
	}

	// C)obtain a gateway client
	client := clientFactory(p.Gateway)
	if client == nil {
		if _, err := db.ExecContext(ctx, `UPDATE verification_polls SET status='failed', error=$1 WHERE id=$2`, "no_client", p.ID); err != nil {
			slog.Error("mark no_client failed", "error", err, "id", p.ID)
		}
		return
	}
	if gateway.WaitForExpiry(client) {
		var currency string
		if err := db.QueryRowContext(ctx, `SELECT currency FROM holds WHERE txn_id=$1`, p.TxnID).Scan(&currency); err != nil {
			return
		}
		if currency != "INR" {
			if err := markHoldIndeterminateReason(ctx, db, p.TxnID, "unsupported_currency", 0, p.HoldAmount); err != nil {
				slog.Error("currency review failed", "error", err)
			}
			return
		}
	}
	// D)Call the external gateway
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	pollStarted := time.Now()
	gatewayStatus, gatewayAmount, raw, err := client.Status(cctx, p.TxnID) //returns gatewayStatus, gatewayAmount, raw JSON, and err
	metrics.PollLatency.WithLabelValues(p.Gateway).Observe(time.Since(pollStarted).Seconds())
	if err != nil {
		if _, err2 := db.ExecContext(ctx, `UPDATE verification_polls SET status='failed', error=$1, completed_at=now() WHERE id=$2`, err.Error(), p.ID); err2 != nil {
			slog.Error("update poll failed status failed", "error", err2, "id", p.ID)
		}
		if errors.Is(err, gateway.ErrTransient) && gateway.WaitForExpiry(client) {
			if err := scheduleUntilExpiry(ctx, db, p.TxnID, p.Attempt); err != nil {
				slog.Error("schedule transient retry failed", "error", err)
			}
			return
		}
		if p.Attempt < cfg.StabilizationN {
			if err3 := scheduleNextPoll(ctx, db, lag, p); err3 != nil {
				slog.Error("schedule next attempt failed", "error", err3)
			}
		} else {
			if err := markHoldIndeterminateReason(ctx, db, p.TxnID, "polling_exhausted_gateway_error", 0, p.HoldAmount); err != nil {
				slog.Error("markHoldIndeterminate failed", "error", err, "txn", p.TxnID)
			}
		}
		return
	}

	// E)Record a successful gateway response
	var rawJSON json.RawMessage
	if raw != nil {
		rawJSON = raw
	}
	if _, err := db.ExecContext(ctx, `UPDATE verification_polls SET gateway_status=$1, gateway_amount=$2, raw_response=$3, completed_at=now(), status='completed' WHERE id=$4`, gatewayStatus, gatewayAmount, rawJSON, p.ID); err != nil {
		slog.Error("update poll success failed", "error", err, "id", p.ID)
		return
	}

	if observer, ok := client.(gateway.CaptureObserver); ok && observer.CaptureObserved(raw) {
		late, err := recordLateCapture(ctx, db, p.TxnID, gatewayAmount, raw)
		if err != nil {
			slog.Error("record late capture failed", "error", err)
			return
		}
		if late {
			return
		}
	}
	if gatewayStatus == "mismatch" {
		if err := markHoldMismatch(ctx, db, p.TxnID, gatewayAmount, p.HoldAmount); err != nil {
			slog.Error("mark mismatch failed", "error", err)
		}
		return
	}
	if gatewayStatus == "indeterminate" {
		if err := markHoldIndeterminateReason(ctx, db, p.TxnID, "gateway_requires_review", gatewayAmount, p.HoldAmount); err != nil {
			slog.Error("mark review failed", "error", err)
		}
		return
	}
	if gateway.WaitForExpiry(client) && !isSuccessStatus(gatewayStatus) {
		if err := scheduleUntilExpiry(ctx, db, p.TxnID, p.Attempt); err != nil {
			slog.Error("schedule order retry failed", "error", err)
		}
		return
	}

	if isSuccessStatus(gatewayStatus) {
		if gatewayAmount == p.HoldAmount {
			streak := completedStreak(ctx, db, p.TxnID, func(status string, amount int64) bool {
				return isSuccessStatus(status) && amount == p.HoldAmount
			})
			if streak >= cfg.StabilizationN {
				lag.Record(p.Gateway, time.Since(p.CreatedAt))
				if err := finalizeHold(ctx, db, p.TxnID); err != nil {
					slog.Error("finalize hold failed", "error", err, "txn", p.TxnID)
				}
			} else if err := scheduleNextPoll(ctx, db, lag, p); err != nil {
				slog.Error("schedule next attempt failed", "error", err)
			}
		} else {
			slog.Warn("amount mismatch on success", "txn", p.TxnID,
				"hold_amount", p.HoldAmount, "gateway_amount", gatewayAmount)
			if err := markHoldMismatch(ctx, db, p.TxnID, gatewayAmount, p.HoldAmount); err != nil {
				slog.Error("markHoldMismatch failed", "error", err, "txn", p.TxnID)
			}
		}
		return
	}

	if isFailureStatus(gatewayStatus) {
		streak := completedStreak(ctx, db, p.TxnID, func(status string, _ int64) bool {
			return isFailureStatus(status)
		})
		if streak >= cfg.StabilizationN {
			if err := markHoldExhausted(ctx, db, p.TxnID, "stable_gateway_failure"); err != nil {
				slog.Error("markHoldExhausted failed", "error", err, "txn", p.TxnID)
			}
			return
		}
	}

	if p.Attempt < cfg.StabilizationN {
		if err := scheduleNextPoll(ctx, db, lag, p); err != nil {
			slog.Error("schedule next attempt failed", "error", err)
		}
	} else {
		if err := markHoldIndeterminateReason(ctx, db, p.TxnID, "polling_exhausted_no_consensus", gatewayAmount, p.HoldAmount); err != nil {
			slog.Error("markHoldIndeterminate failed", "error", err, "txn", p.TxnID)
		}
	}
}

func scheduleNextPoll(ctx context.Context, db *sql.DB, lag *LagEstimator, p struct {
	ID         int64
	TxnID      string
	Attempt    int
	Gateway    string
	HoldAmount int64
	CreatedAt  time.Time
}) error {
	schedule := lag.ScheduleFor(p.Gateway)
	idx := p.Attempt - 1
	delay := schedule.FailAfter
	if idx >= 0 && idx < len(schedule.CatchPolls) {
		delay = schedule.CatchPolls[idx]
	}
	scheduledAt := p.CreatedAt.Add(delay)
	if scheduledAt.Before(time.Now().UTC()) {
		scheduledAt = time.Now().UTC()
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO verification_polls (txn_id, attempt_number, scheduled_at, status)
		 VALUES ($1, $2, $3, 'pending')`,
		p.TxnID, p.Attempt+1, scheduledAt)
	return err
}

func completedStreak(ctx context.Context, db *sql.DB, txnID string, match func(string, int64) bool) int {
	rows, err := db.QueryContext(ctx, `
		SELECT coalesce(gateway_status,''), coalesce(gateway_amount,0)
		FROM verification_polls
		WHERE txn_id=$1 AND status='completed'
		ORDER BY completed_at DESC
		LIMIT 20`, txnID)
	if err != nil {
		return 0
	}
	defer func() {
		_ = rows.Close()
	}()

	count := 0
	for rows.Next() {
		var status string
		var amount int64
		if err := rows.Scan(&status, &amount); err != nil {
			break
		}
		if !match(status, amount) {
			break
		}
		count++
	}
	return count
}

// isSuccessStatus returns true when the gateway status means the payment is captured/settled.
func isSuccessStatus(s string) bool {
	switch normalizeStatus(s) {
	case "success", "captured", "completed":
		return true
	}
	return false
}

func isFailureStatus(s string) bool {
	switch normalizeStatus(s) {
	case "failed", "failure":
		return true
	}
	return false
}

func normalizeStatus(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func isTerminalStatus(s string) bool {
	switch s {
	case "CONFIRMED", "FAILED", "REFUNDED", "INDETERMINATE", "MISMATCH":
		return true
	}
	return false
}

type holdSnapshot struct {
	Status   string
	Gateway  string
	Amount   int64
	Currency string
	Metadata json.RawMessage
}

func loadHoldForUpdate(ctx context.Context, tx *sql.Tx, txnID string) (holdSnapshot, error) {
	var h holdSnapshot
	if err := tx.QueryRowContext(ctx, `
		SELECT status, gateway, amount, currency, metadata
		FROM holds WHERE txn_id=$1 FOR UPDATE`, txnID).
		Scan(&h.Status, &h.Gateway, &h.Amount, &h.Currency, &h.Metadata); err != nil {
		return h, err
	}
	return h, nil
}

func callbackPayload(txnID, status, reason string, h holdSnapshot, extra map[string]any) (json.RawMessage, error) {
	body := map[string]any{
		"txn_id":      txnID,
		"event":       "transaction." + strings.ToLower(status),
		"status":      status,
		"amount":      h.Amount,
		"currency":    h.Currency,
		"gateway":     h.Gateway,
		"verified_at": time.Now().UTC().Format(time.RFC3339),
		"metadata":    json.RawMessage(h.Metadata),
	}
	if reason != "" {
		body["reason"] = reason
	}
	for k, v := range extra {
		body[k] = v
	}
	b, err := json.Marshal(body)
	return json.RawMessage(b), err
}

func markHoldExhausted(ctx context.Context, db *sql.DB, txnID, reason string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	hold, err := loadHoldForUpdate(ctx, tx, txnID)
	if err != nil {
		return err
	}
	if isTerminalStatus(hold.Status) {
		return tx.Commit()
	}

	if _, err := tx.ExecContext(ctx, `UPDATE holds SET status='FAILED' WHERE txn_id=$1`, txnID); err != nil {
		return err
	}

	detailBytes, err := json.Marshal(struct {
		Reason string `json:"reason"`
	}{Reason: reason})
	if err != nil {
		return fmt.Errorf("marshal detail failed: %w", err)
	}
	detail := json.RawMessage(detailBytes)

	if _, err := tx.ExecContext(ctx, `INSERT INTO ledger (txn_id, event_type, source, from_status, to_status, detail) VALUES ($1, 'state_transition', 'stabilizer', $2, 'FAILED', $3)`, txnID, hold.Status, detail); err != nil {
		return err
	}

	payload, err := callbackPayload(txnID, "FAILED", reason, hold, nil)
	if err != nil {
		return fmt.Errorf("marshal payload failed: %w", err)
	}

	idempotency := "evt_" + txnID + "_FAILED_" + reason
	if _, err := tx.ExecContext(ctx, `INSERT INTO outbox (txn_id, event_type, payload, idempotency_key, next_attempt_at) VALUES ($1, 'transaction.FAILED', $2, $3, now())`, txnID, payload, idempotency); err != nil {
		return err
	}
	return tx.Commit()
}

func markHoldIndeterminate(ctx context.Context, db *sql.DB, txnID string, gatewayAmount, holdAmount int64) error {
	return markHoldIndeterminateReason(ctx, db, txnID, "amount_mismatch", gatewayAmount, holdAmount)
}

func markHoldMismatch(ctx context.Context, db *sql.DB, txnID string, gatewayAmount, holdAmount int64) error {
	return markHoldWithAmountReview(ctx, db, txnID, "MISMATCH", "amount_mismatch", gatewayAmount, holdAmount)
}

func markHoldIndeterminateReason(ctx context.Context, db *sql.DB, txnID, reason string, gatewayAmount, holdAmount int64) error {
	return markHoldWithAmountReview(ctx, db, txnID, "INDETERMINATE", reason, gatewayAmount, holdAmount)
}

func markHoldWithAmountReview(ctx context.Context, db *sql.DB, txnID, status, reason string, gatewayAmount, holdAmount int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	hold, err := loadHoldForUpdate(ctx, tx, txnID)
	if err != nil {
		return err
	}
	if isTerminalStatus(hold.Status) {
		return tx.Commit()
	}

	if _, err := tx.ExecContext(ctx, `UPDATE holds SET status=$1 WHERE txn_id=$2`, status, txnID); err != nil {
		return err
	}
	switch status {
	case "INDETERMINATE":
		metrics.TxnIndeterminate.Inc()
		alert.New().Send(ctx, alert.Error, fmt.Sprintf("transaction %s became INDETERMINATE: %s", txnID, reason))
	case "MISMATCH":
		metrics.VerificationMismatches.Inc()
		alert.New().Send(ctx, alert.Error, fmt.Sprintf("transaction %s has amount mismatch: gateway=%d hold=%d", txnID, gatewayAmount, holdAmount))
	}

	detailBytes, err := json.Marshal(struct {
		Reason        string `json:"reason"`
		GatewayAmount int64  `json:"gateway_amount"`
		HoldAmount    int64  `json:"hold_amount"`
	}{Reason: reason, GatewayAmount: gatewayAmount, HoldAmount: holdAmount})
	if err != nil {
		return fmt.Errorf("marshal detail failed: %w", err)
	}
	detail := json.RawMessage(detailBytes)

	if _, err := tx.ExecContext(ctx, `INSERT INTO ledger (txn_id, event_type, source, from_status, to_status, detail) VALUES ($1, 'state_transition', 'stabilizer', $2, $3, $4)`, txnID, hold.Status, status, detail); err != nil {
		return err
	}

	payload, err := callbackPayload(txnID, status, reason, hold, map[string]any{
		"gateway_amount": gatewayAmount,
		"hold_amount":    holdAmount,
	})
	if err != nil {
		return fmt.Errorf("marshal payload failed: %w", err)
	}

	idempotency := "evt_" + txnID + "_" + status
	eventType := "transaction." + status
	if _, err := tx.ExecContext(ctx, `INSERT INTO outbox (txn_id, event_type, payload, idempotency_key, next_attempt_at) VALUES ($1, $2, $3, $4, now())`, txnID, eventType, payload, idempotency); err != nil {
		return err
	}
	return tx.Commit()
}

func finalizeHold(ctx context.Context, db *sql.DB, txnID string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	hold, err := loadHoldForUpdate(ctx, tx, txnID)
	if err != nil {
		return err
	}
	if isTerminalStatus(hold.Status) {
		return tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `UPDATE holds SET status='CONFIRMED' WHERE txn_id=$1`, txnID); err != nil {
		return err
	}
	detail := json.RawMessage(`{"reason":"success_observed"}`)
	if _, err := tx.ExecContext(ctx, `INSERT INTO ledger (txn_id, event_type, source, from_status, to_status, detail) VALUES ($1, 'state_transition', 'stabilizer', $2, 'CONFIRMED', $3)`, txnID, hold.Status, detail); err != nil {
		return err
	}

	payload, err := callbackPayload(txnID, "CONFIRMED", "", hold, nil)
	if err != nil {
		return fmt.Errorf("marshal payload failed: %w", err)
	}

	idempotency := "evt_" + txnID + "_CONFIRMED"
	if _, err := tx.ExecContext(ctx, `INSERT INTO outbox (txn_id, event_type, payload, idempotency_key, next_attempt_at) VALUES ($1, 'transaction.CONFIRMED', $2, $3, now())`, txnID, payload, idempotency); err != nil {
		return err
	}
	return tx.Commit()
}

func scheduleUntilExpiry(ctx context.Context, db *sql.DB, txnID string, attempt int) error {
	_, err := db.ExecContext(ctx, `INSERT INTO verification_polls (txn_id,attempt_number,scheduled_at,status)
	 SELECT txn_id,$2,LEAST(expires_at,now()+interval '10 seconds'),'pending'
	 FROM holds WHERE txn_id=$1 AND status IN ('PENDING','VERIFYING') AND expires_at > now()`, txnID, attempt+1)
	return err
}

func recordLateCapture(ctx context.Context, db *sql.DB, txnID string, amount int64, raw json.RawMessage) (bool, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	h, err := loadHoldForUpdate(ctx, tx, txnID)
	if err != nil {
		return false, err
	}
	if h.Status != "FAILED" {
		return false, tx.Commit()
	}
	if raw == nil {
		raw = json.RawMessage(`{}`)
	}
	detail, err := json.Marshal(map[string]any{"gateway_amount": amount, "evidence": raw})
	if err != nil {
		return true, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO ledger (txn_id,event_type,source,from_status,to_status,detail)
	 SELECT $1,'late_capture','stabilizer','FAILED','FAILED',$2::jsonb
	 WHERE NOT EXISTS (SELECT 1 FROM ledger WHERE txn_id=$1 AND event_type='late_capture')`, txnID, detail)
	if err != nil {
		return true, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return true, err
	}
	if err := tx.Commit(); err != nil {
		return true, err
	}
	if rows > 0 {
		alert.New().Send(ctx, alert.Error, fmt.Sprintf("transaction %s captured after FAILED; manual review is required", txnID))
	}
	return true, nil
}
