package delivery

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"strconv"
	"time"

	"github.com/IDEA-Amrita/paystable/internal/alert"
	"github.com/IDEA-Amrita/paystable/internal/metrics"
)

// Config holds delivery-specific tunables pulled from the parent config
type Config struct {
	CallbackSecret    string
	AllowInsecure     bool
	TimeoutS          int
	WorkerConcurrency int
}

// Run starts the delivery worker and a reaper for stuck in_flight rows
// it blocks until ctx is cancelled
func Run(ctx context.Context, db *sql.DB, cfg Config) {
	sem := make(chan struct{}, cfg.WorkerConcurrency)

	go runReaper(ctx, db)

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("delivery worker stopping")
			return
		case <-ticker.C:
		}

		rows, err := claimBatch(ctx, db)
		if err != nil {
			slog.Error("delivery: claim batch", "error", err)
			continue
		}

		for _, row := range rows {
			row := row
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			go func() {
				defer func() { <-sem }()
				deliver(ctx, db, cfg, row)
			}()
		}
	}
}

type outboxRow struct {
	ID             int64
	TxnID          string
	EventType      string
	Payload        json.RawMessage
	IdempotencyKey string
	Attempts       int
	MaxAttempts    int
	CallbackURL    string
}

func claimBatch(ctx context.Context, db *sql.DB) ([]outboxRow, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT o.id, o.txn_id, o.event_type, o.payload, o.idempotency_key,
		       o.attempts, o.max_attempts, h.callback_url
		FROM outbox o
		JOIN holds h ON o.txn_id = h.txn_id
		WHERE o.status = 'pending' AND o.next_attempt_at <= now()
		ORDER BY o.next_attempt_at
		LIMIT 10
		FOR UPDATE OF o SKIP LOCKED`)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}

	var batch []outboxRow
	for rows.Next() {
		var r outboxRow
		if err := rows.Scan(&r.ID, &r.TxnID, &r.EventType, &r.Payload,
			&r.IdempotencyKey, &r.Attempts, &r.MaxAttempts, &r.CallbackURL); err != nil {
			slog.Error("delivery: scan row", "error", err)
			continue
		}
		batch = append(batch, r)
	}
	if err := rows.Close(); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := rows.Err(); err != nil {
		_ = tx.Rollback()
		return nil, err
	}

	if len(batch) == 0 {
		_ = tx.Rollback()
		return nil, nil
	}

	ids := make([]int64, len(batch))
	for i, r := range batch {
		ids[i] = r.ID
	}
	if err := markInFlight(ctx, tx, ids); err != nil {
		_ = tx.Rollback()
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return batch, nil
}

func markInFlight(ctx context.Context, tx *sql.Tx, ids []int64) error {
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx,
			`UPDATE outbox SET status='in_flight', last_attempt_at=now() WHERE id=$1`, id); err != nil {
			return err
		}
	}
	return nil
}

func deliver(ctx context.Context, db *sql.DB, cfg Config, row outboxRow) {
	if !cfg.AllowInsecure && len(row.CallbackURL) >= 7 && row.CallbackURL[:7] == "http://" {
		slog.Error("delivery: refusing plain HTTP callback", "txn_id", row.TxnID, "url", row.CallbackURL)
		if err := exhaustRow(ctx, db, row, "insecure_url"); err != nil {
			slog.Error("delivery: exhaust on insecure url", "error", err)
		}
		return
	}

	body := row.Payload
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)

	httpClient := &http.Client{
		Timeout:   time.Duration(cfg.TimeoutS) * time.Second,
		Transport: &http.Transport{DisableKeepAlives: true},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, row.CallbackURL, bytes.NewReader(body))
	if err != nil {
		rescheduleOrExhaust(ctx, db, row, 0, err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Paystable-Signature", Sign(body, row.IdempotencyKey, timestamp, cfg.CallbackSecret))
	req.Header.Set("X-Paystable-Idempotency-Key", row.IdempotencyKey)
	req.Header.Set("X-Paystable-Timestamp", timestamp)

	resp, err := httpClient.Do(req)
	if err != nil {
		rescheduleOrExhaust(ctx, db, row, 0, err.Error())
		return
	}
	if err := resp.Body.Close(); err != nil {
		slog.Warn("delivery: close response", "error", err, "id", row.ID)
	}

	code := resp.StatusCode

	if code >= 200 && code < 300 {
		if err := markDelivered(ctx, db, row); err != nil {
			slog.Error("delivery: mark delivered", "error", err, "id", row.ID)
		}
		return
	}

	// 4xx (except 429) is permanent: merchant bug, do not retry
	if code >= 400 && code < 500 && code != 429 {
		slog.Warn("delivery: permanent 4xx, exhausting", "txn_id", row.TxnID, "status", code)
		if err := exhaustRow(ctx, db, row, fmt.Sprintf("http_%d", code)); err != nil {
			slog.Error("delivery: exhaust on 4xx", "error", err)
		}
		return
	}

	rescheduleOrExhaust(ctx, db, row, code, fmt.Sprintf("http_%d", code))
}

func rescheduleOrExhaust(ctx context.Context, db *sql.DB, row outboxRow, httpStatus int, errMsg string) {
	next := row.Attempts + 1
	if next >= row.MaxAttempts {
		if err := exhaustRow(ctx, db, row, errMsg); err != nil {
			slog.Error("delivery: exhaust", "error", err, "id", row.ID)
		}
		return
	}

	delay := nextDelay(next)
	_, err := db.ExecContext(ctx, `
		UPDATE outbox
		SET status='pending', attempts=$1, next_attempt_at=$2,
		    last_http_status=$3, last_error=$4
		WHERE id=$5`,
		next, time.Now().Add(delay), nullableInt(httpStatus), errMsg, row.ID)
	if err != nil {
		slog.Error("delivery: reschedule", "error", err, "id", row.ID)
	}
}

func markDelivered(ctx context.Context, db *sql.DB, row outboxRow) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		UPDATE outbox SET status='delivered', delivered_at=now(), attempts=$1 WHERE id=$2`,
		row.Attempts+1, row.ID); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO ledger (txn_id, event_type, source, detail)
		VALUES ($1, 'callback_delivered', 'delivery', $2::jsonb)`,
		row.TxnID, fmt.Sprintf(`{"idempotency_key":%q}`, row.IdempotencyKey)); err != nil {
		return err
	}

	return tx.Commit()
}

func exhaustRow(ctx context.Context, db *sql.DB, row outboxRow, reason string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		UPDATE outbox SET status='exhausted', attempts=$1, last_error=$2 WHERE id=$3`,
		row.Attempts+1, reason, row.ID); err != nil {
		return err
	}

	detail, _ := json.Marshal(map[string]string{"reason": reason, "idempotency_key": row.IdempotencyKey})
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO ledger (txn_id, event_type, source, detail)
		VALUES ($1, 'callback_failed', 'delivery', $2::jsonb)`,
		row.TxnID, detail); err != nil {
		return err
	}

	slog.Error("delivery: exhausted, ops investigation required",
		"txn_id", row.TxnID, "idempotency_key", row.IdempotencyKey, "reason", reason)
	metrics.OutboxDeliveryFailures.Inc()
	alert.New().Send(ctx, alert.Error, fmt.Sprintf("callback delivery exhausted for %s: %s", row.TxnID, reason))

	return tx.Commit()
}

// runReaper resets in_flight rows that are older than 2 minutes back to pending
// this recovers from worker crashes mid-delivery without leaving events stranded
func runReaper(ctx context.Context, db *sql.DB) {
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		res, err := db.ExecContext(ctx, `
			UPDATE outbox SET status='pending', next_attempt_at=now()
			WHERE status='in_flight' AND last_attempt_at < now() - interval '2 minutes'`)
		if err != nil {
			slog.Error("delivery: reaper", "error", err)
			continue
		}
		if n, _ := res.RowsAffected(); n > 0 {
			slog.Warn("delivery: reaper recovered stuck rows", "count", n)
		}
	}
}

// nextDelay returns a full-jitter exponential delay for attempt n (1-indexed).
// schedule: ~10s, ~1m, ~5m, ~30m, ~2h, ~6h, ~12h
func nextDelay(n int) time.Duration {
	bases := []time.Duration{
		10 * time.Second,
		time.Minute,
		5 * time.Minute,
		30 * time.Minute,
		2 * time.Hour,
		6 * time.Hour,
		12 * time.Hour,
	}
	idx := n - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(bases) {
		idx = len(bases) - 1
	}
	base := bases[idx]
	return time.Duration(rand.Int63n(int64(base))) + base/2
}

func nullableInt(v int) interface{} {
	if v == 0 {
		return nil
	}
	return v
}
