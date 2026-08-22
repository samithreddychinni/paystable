package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/lib/pq"

	"github.com/IDEA-Amrita/paystable/internal/gateway/razorpay"
	"github.com/IDEA-Amrita/paystable/internal/verification"
)

const schema = `
CREATE TABLE IF NOT EXISTS lab_state (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    scenario text NOT NULL,
    program text NOT NULL,
    order_id text NOT NULL,
    payment_state text NOT NULL DEFAULT '',
    captured_once boolean NOT NULL DEFAULT false,
    pending_effect boolean NOT NULL DEFAULT false,
    effect_attempt integer NOT NULL DEFAULT 0,
    trace_sequence integer NOT NULL DEFAULT 0,
    clock_offset_seconds bigint NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS lab_events (
    event_id text PRIMARY KEY,
    status text NOT NULL,
    claimed_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE IF NOT EXISTS lab_effects (
    effect_key text PRIMARY KEY,
    order_id text NOT NULL
);
CREATE TABLE IF NOT EXISTS lab_trace (
    sequence integer PRIMARY KEY,
    action text NOT NULL,
    detail text NOT NULL,
    state text NOT NULL,
    effect_count integer NOT NULL
);
ALTER TABLE lab_state ADD COLUMN IF NOT EXISTS clock_offset_seconds bigint NOT NULL DEFAULT 0;
ALTER TABLE lab_events ADD COLUMN IF NOT EXISTS claimed_at timestamptz NOT NULL DEFAULT clock_timestamp();`

type app struct {
	db            *sql.DB
	webhookSecret string
}

type state struct {
	scenario      string
	program       string
	orderID       string
	paymentState  string
	capturedOnce  bool
	pendingEffect bool
	effectAttempt int
	clockOffset   int64
}

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		slog.Error("DATABASE_URL is required")
		os.Exit(1)
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		slog.Error("open database", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	for attempt := 0; attempt < 30; attempt++ {
		if err = db.Ping(); err == nil {
			break
		}
		time.Sleep(time.Second)
	}
	if err != nil {
		slog.Error("connect to database", "error", err)
		os.Exit(1)
	}
	if _, err := db.Exec(schema); err != nil {
		slog.Error("create laboratory schema", "error", err)
		os.Exit(1)
	}
	a := &app{db: db, webhookSecret: envOr("LAB_WEBHOOK_SECRET", "lab-webhook-secret")}
	_ = a.record(context.Background(), "restart", "merchant process started")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", a.health)
	mux.HandleFunc("POST /reset", a.reset)
	mux.HandleFunc("POST /deliver", a.deliver)
	mux.HandleFunc("POST /fulfill", a.fulfill)
	mux.HandleFunc("POST /advance", a.advance)
	mux.HandleFunc("GET /result", a.result)

	port := envOr("PORT", "9093")
	slog.Info("lab merchant started", "port", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		slog.Error("lab merchant stopped", "error", err)
		os.Exit(1)
	}
}

func (a *app) health(w http.ResponseWriter, r *http.Request) {
	if err := a.db.PingContext(r.Context()); err != nil {
		http.Error(w, "database is not ready", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) reset(w http.ResponseWriter, r *http.Request) {
	var schedule verification.Schedule
	if !decodeJSON(w, r, &schedule, "schedule") {
		return
	}
	if err := verification.Validate(schedule); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		http.Error(w, "could not start the reset", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(r.Context(), `TRUNCATE lab_events, lab_effects, lab_trace, lab_state`); err != nil {
		http.Error(w, "could not clear the laboratory", http.StatusInternalServerError)
		return
	}
	if _, err := tx.ExecContext(r.Context(), `
		INSERT INTO lab_state (scenario, program, order_id) VALUES ($1, $2, $3)`,
		schedule.Name, schedule.Program, schedule.OrderID); err != nil {
		http.Error(w, "could not create the laboratory state", http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, "could not commit the reset", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) deliver(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, "invalid deliver action", http.StatusBadRequest)
		return
	}
	current, err := a.loadState(r)
	if err != nil {
		http.Error(w, "laboratory state is not ready", http.StatusConflict)
		return
	}
	trusted := razorpay.VerifyWebhookSignature(body, r.Header.Get("X-Lab-Signature"), a.webhookSecret)
	if !trusted && current.program != verification.ProgramAcceptUntrusted {
		http.Error(w, "untrusted payment event", http.StatusUnauthorized)
		return
	}
	var action verification.Action
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&action) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		http.Error(w, "invalid deliver action", http.StatusBadRequest)
		return
	}
	if action.Type != "deliver" || verification.Validate(verification.Schedule{
		Name: current.scenario, Program: current.program, OrderID: current.orderID, Actions: []verification.Action{action},
	}) != nil {
		http.Error(w, "deliver action is not legal", http.StatusBadRequest)
		return
	}
	if verification.HasAmountMismatch(action) && current.program != verification.ProgramAcceptWrongAmount {
		if err := a.record(r.Context(), "reject", "payment amount mismatch rejected"); err != nil {
			http.Error(w, "could not record the amount rejection", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if verification.HasAmountMismatch(action) {
		if err := a.record(r.Context(), "amount_mismatch_accept", "payment amount mismatch accepted"); err != nil {
			http.Error(w, "could not record the amount mismatch", http.StatusInternalServerError)
			return
		}
	}
	if verification.HasCurrencyMismatch(action) && current.program != verification.ProgramAcceptWrongCurrency {
		if err := a.record(r.Context(), "reject", "payment currency mismatch rejected"); err != nil {
			http.Error(w, "could not record the currency rejection", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if verification.HasCurrencyMismatch(action) {
		if err := a.record(r.Context(), "currency_mismatch_accept", "payment currency mismatch accepted"); err != nil {
			http.Error(w, "could not record the currency mismatch", http.StatusInternalServerError)
			return
		}
	}
	if verification.HasOrderMismatch(current.orderID, action) && current.program != verification.ProgramAcceptWrongOrder {
		if err := a.record(r.Context(), "reject", "payment order mismatch rejected"); err != nil {
			http.Error(w, "could not record the order rejection", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if verification.HasOrderMismatch(current.orderID, action) {
		if err := a.record(r.Context(), "order_mismatch_accept", "payment order mismatch accepted"); err != nil {
			http.Error(w, "could not record the order mismatch", http.StatusInternalServerError)
			return
		}
	}
	if verification.FulfillsBeforeDedup(current.program, action) && action.Status == "captured" {
		if err := a.effectBeforeDedup(r, current.orderID); err != nil {
			http.Error(w, "could not record fulfillment", http.StatusInternalServerError)
			return
		}
		if action.CrashAt == "after_fulfillment" {
			_ = a.record(r.Context(), "crash", "merchant crashed after fulfillment")
			os.Exit(86)
		}
	}
	if err := a.storeEvent(r, action, !trusted); err != nil {
		if errors.Is(err, errDuplicate) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, "could not store the payment event", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

var errDuplicate = errors.New("duplicate event")

func (a *app) effectBeforeDedup(r *http.Request, orderID string) error {
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(r.Context(), `
		INSERT INTO lab_effects (effect_key, order_id) VALUES ('unkeyed:' || txid_current()::text, $1)`, orderID); err != nil {
		return err
	}
	if err := recordTx(r.Context(), tx, "fulfill", "fulfillment occurred before durable event storage"); err != nil {
		return err
	}
	return tx.Commit()
}

func (a *app) storeEvent(r *http.Request, action verification.Action, untrusted bool) error {
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current, err := loadStateTx(r, tx)
	if err != nil {
		return err
	}
	var exists bool
	if err := tx.QueryRowContext(r.Context(), `SELECT EXISTS (SELECT 1 FROM lab_events WHERE event_id=$1)`, action.EventID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		if err := recordTx(r.Context(), tx, "deliver", "duplicate event ignored"); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		return errDuplicate
	}
	if untrusted {
		if err := recordTx(r.Context(), tx, "untrusted_accept", "untrusted payment event accepted"); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(r.Context(), `
		INSERT INTO lab_events (event_id, status, claimed_at)
		VALUES ($1, $2, clock_timestamp() + ($3::bigint * interval '1 second'))`,
		action.EventID, action.Status, current.clockOffset); err != nil {
		return err
	}
	if err := recordTx(r.Context(), tx, "checkpoint", "event stored at after_deduplication"); err != nil {
		return err
	}
	newState := current.paymentState
	if current.program == verification.ProgramTerminalRegression || current.paymentState != "captured" {
		newState = action.Status
	}
	pending := current.pendingEffect
	if current.paymentState != "captured" && newState == "captured" && action.Status == "captured" && !verification.FulfillsBeforeDedup(current.program, action) {
		pending = true
	}
	replayProgram := current.program == verification.ProgramExpiringEventClaim || current.program == verification.ProgramDurableEventClaim
	if replayProgram && action.Status == "captured" {
		pending = false
	}
	if _, err := tx.ExecContext(r.Context(), `
		UPDATE lab_state SET payment_state=$1, captured_once=captured_once OR $2, pending_effect=$3`,
		newState, newState == "captured", pending); err != nil {
		return err
	}
	if err := recordTx(r.Context(), tx, "deliver", "payment state updated"); err != nil {
		return err
	}
	if replayProgram && action.Status == "captured" {
		if _, err := tx.ExecContext(r.Context(), `
			INSERT INTO lab_effects (effect_key, order_id) VALUES ('event:' || txid_current()::text, $1)`, current.orderID); err != nil {
			return err
		}
		if err := recordTx(r.Context(), tx, "fulfill", "accepted payment event caused fulfillment"); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (a *app) advance(w http.ResponseWriter, r *http.Request) {
	var action verification.Action
	if !decodeJSON(w, r, &action, "advance action") {
		return
	}
	current, err := a.loadState(r)
	if err != nil {
		http.Error(w, "laboratory state is not ready", http.StatusConflict)
		return
	}
	if action.Type != "advance" || verification.Validate(verification.Schedule{
		Name: current.scenario, Program: current.program, OrderID: current.orderID, Actions: []verification.Action{action},
	}) != nil {
		http.Error(w, "advance action is not legal", http.StatusBadRequest)
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		http.Error(w, "could not start the clock advance", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()
	effectiveAdvance := action.AdvanceSeconds + action.ClockSkewSeconds
	var offset int64
	if err := tx.QueryRowContext(r.Context(), `
		UPDATE lab_state SET clock_offset_seconds=clock_offset_seconds+$1
		WHERE singleton=true RETURNING clock_offset_seconds`, effectiveAdvance).Scan(&offset); err != nil {
		http.Error(w, "could not update the test clock", http.StatusInternalServerError)
		return
	}
	if err := recordTx(r.Context(), tx, "advance", fmt.Sprintf("database clock advanced by %d seconds with %d seconds of skew", action.AdvanceSeconds, action.ClockSkewSeconds)); err != nil {
		http.Error(w, "could not record the clock advance", http.StatusInternalServerError)
		return
	}
	if current.program == verification.ProgramExpiringEventClaim {
		result, err := tx.ExecContext(r.Context(), `
			DELETE FROM lab_events
			WHERE claimed_at < clock_timestamp() + ($1::bigint * interval '1 second')
				- ($2::bigint * interval '1 second')`, offset, verification.EventClaimRetentionSeconds)
		if err != nil {
			http.Error(w, "could not expire event claims", http.StatusInternalServerError)
			return
		}
		count, err := result.RowsAffected()
		if err != nil {
			http.Error(w, "could not count expired event claims", http.StatusInternalServerError)
			return
		}
		if count != 0 {
			if err := recordTx(r.Context(), tx, "expire", fmt.Sprintf("%d event claims expired", count)); err != nil {
				http.Error(w, "could not record expired event claims", http.StatusInternalServerError)
				return
			}
		}
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, "could not commit the clock advance", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) fulfill(w http.ResponseWriter, r *http.Request) {
	var action verification.Action
	if !decodeJSON(w, r, &action, "fulfill action") {
		return
	}
	if action.Response == "db-conflict" || action.Response == "db-deadlock" {
		a.fulfillWithDatabaseFailure(w, r, action)
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		http.Error(w, "could not start fulfillment", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()
	current, err := loadStateTx(r, tx)
	if err != nil || !current.pendingEffect {
		http.Error(w, "payment is not ready for fulfillment", http.StatusConflict)
		return
	}
	if action.Type != "fulfill" || verification.Validate(verification.Schedule{
		Name: current.scenario, Program: current.program, OrderID: current.orderID, Actions: []verification.Action{action},
	}) != nil {
		http.Error(w, "fulfill action is not legal", http.StatusBadRequest)
		return
	}
	attempt := current.effectAttempt + 1
	key := current.orderID
	if verification.UsesNewKey(current.program) {
		key = fmt.Sprintf("%s:%d", key, attempt)
	}
	if _, err := tx.ExecContext(r.Context(), `
		INSERT INTO lab_effects (effect_key, order_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, key, current.orderID); err != nil {
		http.Error(w, "could not record fulfillment", http.StatusInternalServerError)
		return
	}
	pending := action.Response != "ok" && !verification.RetryExhausted(current.program, attempt)
	if _, err := tx.ExecContext(r.Context(), `
		UPDATE lab_state SET effect_attempt=$1, pending_effect=$2`, attempt, pending); err != nil {
		http.Error(w, "could not update fulfillment", http.StatusInternalServerError)
		return
	}
	if err := recordTx(r.Context(), tx, "fulfill", "fulfillment sink accepted key "+key); err != nil {
		http.Error(w, "could not record the fulfillment trace", http.StatusInternalServerError)
		return
	}
	if action.Response != "ok" {
		if err := recordTx(r.Context(), tx, verification.ResponseTraceAction(action.Response), "merchant did not receive a reliable sink response"); err != nil {
			http.Error(w, "could not record the lost response", http.StatusInternalServerError)
			return
		}
		if verification.RetryExhausted(current.program, attempt) {
			if err := recordTx(r.Context(), tx, "retry_exhausted", "fulfillment retry limit reached"); err != nil {
				http.Error(w, "could not record retry exhaustion", http.StatusInternalServerError)
				return
			}
		}
		if verification.RetryOverrun(current.program, attempt) {
			if err := recordTx(r.Context(), tx, "retry_overrun", "fulfillment continued after the retry limit"); err != nil {
				http.Error(w, "could not record retry overrun", http.StatusInternalServerError)
				return
			}
		}
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, "could not commit fulfillment", http.StatusInternalServerError)
		return
	}
	switch action.Response {
	case "lost":
		os.Exit(87)
	case "timeout":
		time.Sleep(4 * time.Second)
		return
	case "connection-reset":
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "connection reset is not available", http.StatusInternalServerError)
			return
		}
		connection, _, err := hijacker.Hijack()
		if err == nil {
			_ = connection.Close()
		}
		return
	case "http-500":
		http.Error(w, "fulfillment response failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) fulfillWithDatabaseFailure(w http.ResponseWriter, r *http.Request, action verification.Action) {
	current, err := a.loadState(r)
	if err != nil || !current.pendingEffect {
		http.Error(w, "payment is not ready for fulfillment", http.StatusConflict)
		return
	}
	if action.Type != "fulfill" || verification.Validate(verification.Schedule{
		Name: current.scenario, Program: current.program, OrderID: current.orderID, Actions: []verification.Action{action},
	}) != nil {
		http.Error(w, "fulfill action is not legal", http.StatusBadRequest)
		return
	}
	key := current.orderID
	if verification.UsesNewKey(current.program) {
		key = fmt.Sprintf("%s:%d", key, current.effectAttempt+1)
	}
	if err := a.recordExternalEffect(r.Context(), key, current.orderID); err != nil {
		http.Error(w, "could not record fulfillment", http.StatusInternalServerError)
		return
	}
	if action.Response == "db-conflict" {
		err = a.causeDatabaseConflict(r.Context())
	} else {
		err = a.causeDatabaseDeadlock(r.Context())
	}
	if err != nil {
		http.Error(w, "could not create a database failure", http.StatusInternalServerError)
		return
	}
	if err := a.record(r.Context(), verification.ResponseTraceAction(action.Response), "database transaction failed after fulfillment"); err != nil {
		http.Error(w, "could not record the database failure", http.StatusInternalServerError)
		return
	}
	http.Error(w, "database transaction failed", http.StatusConflict)
}

func (a *app) recordExternalEffect(ctx context.Context, key, orderID string) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO lab_effects (effect_key, order_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, key, orderID); err != nil {
		return err
	}
	if err := recordTx(ctx, tx, "fulfill", "fulfillment sink accepted key "+key); err != nil {
		return err
	}
	return tx.Commit()
}

func (a *app) causeDatabaseConflict(ctx context.Context) error {
	options := &sql.TxOptions{Isolation: sql.LevelSerializable}
	first, err := a.db.BeginTx(ctx, options)
	if err != nil {
		return err
	}
	defer first.Rollback()
	second, err := a.db.BeginTx(ctx, options)
	if err != nil {
		return err
	}
	defer second.Rollback()
	var firstAttempt, secondAttempt int
	if err := first.QueryRowContext(ctx, `SELECT effect_attempt FROM lab_state WHERE singleton=true`).Scan(&firstAttempt); err != nil {
		return err
	}
	if err := second.QueryRowContext(ctx, `SELECT effect_attempt FROM lab_state WHERE singleton=true`).Scan(&secondAttempt); err != nil {
		return err
	}
	if _, err := first.ExecContext(ctx, `UPDATE lab_state SET effect_attempt=$1 WHERE singleton=true`, firstAttempt+1); err != nil {
		return err
	}
	if err := first.Commit(); err != nil {
		return err
	}
	_, err = second.ExecContext(ctx, `UPDATE lab_state SET effect_attempt=$1 WHERE singleton=true`, secondAttempt+1)
	if err == nil {
		return fmt.Errorf("expected PostgreSQL error 40001")
	}
	if !postgresError(err, "40001") {
		return fmt.Errorf("expected PostgreSQL error 40001, got %w", err)
	}
	return nil
}

func (a *app) causeDatabaseDeadlock(ctx context.Context) error {
	first, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer first.Rollback()
	second, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer second.Rollback()
	for _, tx := range []*sql.Tx{first, second} {
		if _, err := tx.ExecContext(ctx, `SET LOCAL deadlock_timeout = '100ms'`); err != nil {
			return err
		}
	}
	if _, err := first.ExecContext(ctx, `SELECT pg_advisory_xact_lock(7001)`); err != nil {
		return err
	}
	if _, err := second.ExecContext(ctx, `SELECT pg_advisory_xact_lock(7002)`); err != nil {
		return err
	}
	results := make(chan error, 2)
	lock := func(tx *sql.Tx, key int) {
		_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, key)
		if err != nil {
			_ = tx.Rollback()
		}
		results <- err
	}
	go lock(first, 7002)
	go lock(second, 7001)
	deadlocks := 0
	for range 2 {
		err := <-results
		if postgresError(err, "40P01") {
			deadlocks++
		} else if err != nil {
			return err
		}
	}
	if deadlocks != 1 {
		return fmt.Errorf("expected one PostgreSQL error 40P01, got %d", deadlocks)
	}
	_, err = a.db.ExecContext(ctx, `UPDATE lab_state SET effect_attempt=effect_attempt+1 WHERE singleton=true`)
	return err
}

func postgresError(err error, code string) bool {
	var databaseError *pq.Error
	return errors.As(err, &databaseError) && string(databaseError.Code) == code
}

func (a *app) result(w http.ResponseWriter, r *http.Request) {
	current, err := a.loadState(r)
	if err != nil {
		http.Error(w, "laboratory state is not ready", http.StatusConflict)
		return
	}
	var effectCount int
	if err := a.db.QueryRowContext(r.Context(), `SELECT count(*) FROM lab_effects`).Scan(&effectCount); err != nil {
		http.Error(w, "could not count fulfillment effects", http.StatusInternalServerError)
		return
	}
	rows, err := a.db.QueryContext(r.Context(), `SELECT sequence, action, detail, state, effect_count FROM lab_trace ORDER BY sequence`)
	if err != nil {
		http.Error(w, "could not read the trace", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var trace []verification.TraceEntry
	for rows.Next() {
		var entry verification.TraceEntry
		if err := rows.Scan(&entry.Sequence, &entry.Action, &entry.Detail, &entry.State, &entry.EffectCount); err != nil {
			http.Error(w, "could not decode the trace", http.StatusInternalServerError)
			return
		}
		trace = append(trace, entry)
	}
	if err := rows.Err(); err != nil {
		http.Error(w, "could not read the complete trace", http.StatusInternalServerError)
		return
	}
	schedule := verification.Schedule{Name: current.scenario, Program: current.program, OrderID: current.orderID}
	writeJSON(w, verification.ResultFor(schedule, current.paymentState, current.capturedOnce, effectCount, trace))
}

func (a *app) loadState(r *http.Request) (state, error) {
	var current state
	err := a.db.QueryRowContext(r.Context(), `
		SELECT scenario, program, order_id, payment_state, captured_once, pending_effect, effect_attempt, clock_offset_seconds FROM lab_state WHERE singleton=true`).Scan(
		&current.scenario, &current.program, &current.orderID, &current.paymentState,
		&current.capturedOnce, &current.pendingEffect, &current.effectAttempt, &current.clockOffset)
	return current, err
}

func loadStateTx(r *http.Request, tx *sql.Tx) (state, error) {
	var current state
	err := tx.QueryRowContext(r.Context(), `
		SELECT scenario, program, order_id, payment_state, captured_once, pending_effect, effect_attempt, clock_offset_seconds
		FROM lab_state WHERE singleton=true FOR UPDATE`).Scan(
		&current.scenario, &current.program, &current.orderID, &current.paymentState,
		&current.capturedOnce, &current.pendingEffect, &current.effectAttempt, &current.clockOffset)
	return current, err
}

func (a *app) record(ctx context.Context, action, detail string) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := recordTx(ctx, tx, action, detail); err != nil {
		return err
	}
	return tx.Commit()
}

func recordTx(ctx context.Context, tx *sql.Tx, action, detail string) error {
	var sequence int
	var paymentState string
	if err := tx.QueryRowContext(ctx, `
		UPDATE lab_state SET trace_sequence=trace_sequence+1
		WHERE singleton=true RETURNING trace_sequence, payment_state`).Scan(&sequence, &paymentState); err != nil {
		return err
	}
	var effectCount int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM lab_effects`).Scan(&effectCount); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO lab_trace (sequence, action, detail, state, effect_count) VALUES ($1, $2, $3, $4, $5)`,
		sequence, action, detail, paymentState, effectCount)
	return err
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, value any, name string) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		http.Error(w, "invalid "+name, http.StatusBadRequest)
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		http.Error(w, "invalid "+name, http.StatusBadRequest)
		return false
	}
	return true
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
