package main

import (
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

	_ "github.com/lib/pq"

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
    trace_sequence integer NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS lab_events (
    event_id text PRIMARY KEY,
    status text NOT NULL
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
);`

type app struct{ db *sql.DB }

type state struct {
	scenario      string
	program       string
	orderID       string
	paymentState  string
	capturedOnce  bool
	pendingEffect bool
	effectAttempt int
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
	a := &app{db: db}
	_ = a.record(context.Background(), "restart", "merchant process started")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", a.health)
	mux.HandleFunc("POST /reset", a.reset)
	mux.HandleFunc("POST /deliver", a.deliver)
	mux.HandleFunc("POST /fulfill", a.fulfill)
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
	var action verification.Action
	if !decodeJSON(w, r, &action, "deliver action") {
		return
	}
	current, err := a.loadState(r)
	if err != nil {
		http.Error(w, "laboratory state is not ready", http.StatusConflict)
		return
	}
	if action.Type != "deliver" || verification.Validate(verification.Schedule{
		Name: current.scenario, Program: current.program, OrderID: current.orderID, Actions: []verification.Action{action},
	}) != nil {
		http.Error(w, "deliver action is not legal", http.StatusBadRequest)
		return
	}
	if current.program == verification.ProgramFulfillBeforeDedup && action.Status == "captured" {
		if err := a.effectBeforeDedup(r, current.orderID); err != nil {
			http.Error(w, "could not record fulfillment", http.StatusInternalServerError)
			return
		}
		if action.CrashAt == "after_fulfillment" {
			_ = a.record(r.Context(), "crash", "merchant crashed after fulfillment")
			os.Exit(86)
		}
	}
	if err := a.storeEvent(r, action); err != nil {
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
	var count int
	if err := tx.QueryRowContext(r.Context(), `SELECT count(*) FROM lab_effects`).Scan(&count); err != nil {
		return err
	}
	if _, err := tx.ExecContext(r.Context(), `INSERT INTO lab_effects (effect_key, order_id) VALUES ($1, $2)`, fmt.Sprintf("unkeyed:%d", count+1), orderID); err != nil {
		return err
	}
	if err := recordTx(r.Context(), tx, "fulfill", "fulfillment occurred before durable event storage"); err != nil {
		return err
	}
	return tx.Commit()
}

func (a *app) storeEvent(r *http.Request, action verification.Action) error {
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
	if _, err := tx.ExecContext(r.Context(), `INSERT INTO lab_events (event_id, status) VALUES ($1, $2)`, action.EventID, action.Status); err != nil {
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
	if current.paymentState != "captured" && newState == "captured" && action.Status == "captured" && current.program != verification.ProgramFulfillBeforeDedup {
		pending = true
	}
	if _, err := tx.ExecContext(r.Context(), `
		UPDATE lab_state SET payment_state=$1, captured_once=captured_once OR $2, pending_effect=$3`,
		newState, newState == "captured", pending); err != nil {
		return err
	}
	if err := recordTx(r.Context(), tx, "deliver", "payment state updated"); err != nil {
		return err
	}
	return tx.Commit()
}

func (a *app) fulfill(w http.ResponseWriter, r *http.Request) {
	var action verification.Action
	if !decodeJSON(w, r, &action, "fulfill action") {
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
	if current.program == verification.ProgramNewKeyOnRetry {
		key = fmt.Sprintf("%s:%d", key, attempt)
	}
	if _, err := tx.ExecContext(r.Context(), `
		INSERT INTO lab_effects (effect_key, order_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, key, current.orderID); err != nil {
		http.Error(w, "could not record fulfillment", http.StatusInternalServerError)
		return
	}
	if _, err := tx.ExecContext(r.Context(), `
		UPDATE lab_state SET effect_attempt=$1, pending_effect=$2`, attempt, action.Response == "lost"); err != nil {
		http.Error(w, "could not update fulfillment", http.StatusInternalServerError)
		return
	}
	if err := recordTx(r.Context(), tx, "fulfill", "fulfillment sink accepted key "+key); err != nil {
		http.Error(w, "could not record the fulfillment trace", http.StatusInternalServerError)
		return
	}
	if action.Response == "lost" {
		if err := recordTx(r.Context(), tx, "response_lost", "merchant did not receive the sink response"); err != nil {
			http.Error(w, "could not record the lost response", http.StatusInternalServerError)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, "could not commit fulfillment", http.StatusInternalServerError)
		return
	}
	if action.Response == "lost" {
		os.Exit(87)
	}
	w.WriteHeader(http.StatusNoContent)
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
		SELECT scenario, program, order_id, payment_state, captured_once, pending_effect, effect_attempt FROM lab_state WHERE singleton=true`).Scan(
		&current.scenario, &current.program, &current.orderID, &current.paymentState,
		&current.capturedOnce, &current.pendingEffect, &current.effectAttempt)
	return current, err
}

func loadStateTx(r *http.Request, tx *sql.Tx) (state, error) {
	var current state
	err := tx.QueryRowContext(r.Context(), `
		SELECT scenario, program, order_id, payment_state, captured_once, pending_effect, effect_attempt
		FROM lab_state WHERE singleton=true FOR UPDATE`).Scan(
		&current.scenario, &current.program, &current.orderID, &current.paymentState,
		&current.capturedOnce, &current.pendingEffect, &current.effectAttempt)
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
