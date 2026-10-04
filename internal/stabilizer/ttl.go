package stabilizer

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/IDEA-Amrita/paystable/internal/config"
	"github.com/IDEA-Amrita/paystable/internal/gateway"
)

// RunTTLScanner periodically finds holds whose TTL has expired while still
// non-terminal and resolves them. It never releases on the timer alone: it runs
// one final verification pass first, because a TTL expiring is an unverified
// signal and acting on it directly is exactly what paystable exists to prevent.
func RunTTLScanner(ctx context.Context, db *sql.DB, cfg *config.Config, clientFactory func(string) gateway.GatewayClient) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("ttl scanner stopping")
			return
		case <-ticker.C:
		}

		expired, err := claimExpiredHolds(ctx, db)
		if err != nil {
			slog.Error("ttl scanner: claim expired failed", "error", err)
			continue
		}
		for _, h := range expired {
			resolveExpiredHold(ctx, db, h, clientFactory)
		}
	}
}

type expiredHold struct {
	TxnID    string
	Gateway  string
	Amount   int64
	Currency string
}

// claimExpiredHolds returns non-terminal holds past their expires_at. SKIP LOCKED
// keeps multiple instances from grabbing the same row.
func claimExpiredHolds(ctx context.Context, db *sql.DB) ([]expiredHold, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT txn_id, gateway, amount, currency
		FROM holds
		WHERE status IN ('PENDING','VERIFYING') AND expires_at <= now()
		ORDER BY expires_at
		LIMIT 10
		FOR UPDATE SKIP LOCKED`)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var out []expiredHold
	for rows.Next() {
		var h expiredHold
		if err := rows.Scan(&h.TxnID, &h.Gateway, &h.Amount, &h.Currency); err != nil {
			slog.Error("ttl scanner: scan failed", "error", err)
			continue
		}
		out = append(out, h)
	}
	return out, nil
}

// resolveExpiredHold runs the final verification pass and routes the hold to a
// terminal state. No gateway answer or an ambiguous one becomes INDETERMINATE
// rather than a silent release.
func resolveExpiredHold(ctx context.Context, db *sql.DB, h expiredHold, clientFactory func(string) gateway.GatewayClient) {
	client := clientFactory(h.Gateway)
	if client == nil {
		if err := markHoldIndeterminate(ctx, db, h.TxnID, 0, h.Amount); err != nil {
			slog.Error("ttl scanner: mark indeterminate (no client)", "error", err, "txn", h.TxnID)
		}
		return
	}

	if gateway.WaitForExpiry(client) && h.Currency != "" && h.Currency != "INR" {
		if err := markHoldIndeterminateReason(ctx, db, h.TxnID, "unsupported_currency", 0, h.Amount); err != nil {
			slog.Error("ttl scanner: currency review failed", "error", err)
		}
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	status, amount, _, err := client.Status(cctx, h.TxnID)
	if err != nil {
		slog.Warn("ttl scanner: final verification failed", "txn", h.TxnID, "error", err)
		if err := markHoldIndeterminate(ctx, db, h.TxnID, 0, h.Amount); err != nil {
			slog.Error("ttl scanner: mark indeterminate", "error", err, "txn", h.TxnID)
		}
		return
	}

	switch {
	case status == "mismatch":
		if err := markHoldMismatch(ctx, db, h.TxnID, amount, h.Amount); err != nil {
			slog.Error("ttl scanner: mark partial payment", "error", err)
		}
	case status == "indeterminate":
		if err := markHoldIndeterminateReason(ctx, db, h.TxnID, "gateway_requires_review", amount, h.Amount); err != nil {
			slog.Error("ttl scanner: mark refund review", "error", err)
		}
	case isSuccessStatus(status) && amount == h.Amount:
		if err := finalizeHold(ctx, db, h.TxnID); err != nil {
			slog.Error("ttl scanner: finalize", "error", err, "txn", h.TxnID)
		}
	case isSuccessStatus(status):
		if err := markHoldMismatch(ctx, db, h.TxnID, amount, h.Amount); err != nil {
			slog.Error("ttl scanner: mark mismatch", "error", err, "txn", h.TxnID)
		}
	case status == "failed" || status == "failure":
		if err := markHoldExhausted(ctx, db, h.TxnID, "ttl_expired_verified_failed"); err != nil {
			slog.Error("ttl scanner: mark failed", "error", err, "txn", h.TxnID)
		}
	default:
		// pending / not_found is not a confirmed failure. We refuse to release
		// on an unverified signal, so this needs a human, not an auto-fail.
		if err := markHoldIndeterminate(ctx, db, h.TxnID, amount, h.Amount); err != nil {
			slog.Error("ttl scanner: mark indeterminate (inconclusive)", "error", err, "txn", h.TxnID)
		}
	}
}
