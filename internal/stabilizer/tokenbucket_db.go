package stabilizer

import (
	"context"
	"database/sql"
	"time"
)

// AcquireToken consumes one gateway token when a token is available.
func AcquireToken(ctx context.Context, db *sql.DB, gateway string, capacity int, refillPerSec float64) (bool, time.Time, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, time.Time{}, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	var tokens float64
	var lastRefill time.Time
	err = tx.QueryRowContext(ctx, "SELECT tokens, last_refill FROM rate_limits WHERE gateway=$1 FOR UPDATE", gateway).Scan(&tokens, &lastRefill)
	if err == sql.ErrNoRows {
		now := time.Now().UTC()
		tokensAfter := float64(capacity - 1)
		_, err = tx.ExecContext(ctx, "INSERT INTO rate_limits (gateway, tokens, last_refill) VALUES ($1, $2, $3)", gateway, tokensAfter, now)
		if err != nil {
			return false, time.Time{}, err
		}
		if err := tx.Commit(); err != nil {
			return false, time.Time{}, err
		}
		return true, time.Time{}, nil
	} else if err != nil {
		return false, time.Time{}, err
	}
	now := time.Now().UTC()
	elapsed := now.Sub(lastRefill).Seconds()
	accrued := elapsed * refillPerSec
	tokens = tokens + accrued
	capf := float64(capacity)
	if tokens > capf {
		tokens = capf
	}
	if tokens >= 1.0 {
		tokens = tokens - 1.0
		_, err = tx.ExecContext(ctx, "UPDATE rate_limits SET tokens=$1, last_refill=$2 WHERE gateway=$3", tokens, now, gateway)
		if err != nil {
			return false, time.Time{}, err
		}
		if err := tx.Commit(); err != nil {
			return false, time.Time{}, err
		}
		return true, time.Time{}, nil
	}
	needed := 1.0 - tokens
	waitSecs := needed / refillPerSec
	// Release the row lock before the caller waits.
	_ = tx.Rollback()
	nextAvailable := time.Now().UTC().Add(time.Duration(waitSecs * float64(time.Second)))
	return false, nextAvailable, nil
}
