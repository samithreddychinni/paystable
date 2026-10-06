package hold

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

var ErrCreateConflict = errors.New("hold already exists with different create parameters")

type Hold struct {
	ID          int64     `json:"-"`
	TxnID       string    `json:"txn_id"`
	Gateway     string    `json:"gateway"`
	Status      string    `json:"status"`
	Amount      int64     `json:"amount"`
	Currency    string    `json:"currency"`
	ReadToken   string    `json:"read_token"`
	CallbackURL string    `json:"-"`
	TTLSeconds  int       `json:"-"`
	ExpiresAt   time.Time `json:"expires_at"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Create inserts a hold. A duplicated txn_id is idempotent only when the
// create parameters are the same as the original request.
func (s *Store) Create(txnID, gateway, callbackURL, currency string, amount int64, ttl int, metadata []byte) (*Hold, error) {
	if len(metadata) == 0 {
		metadata = []byte(`{}`)
	}

	readToken, err := generateToken()
	if err != nil {
		return nil, fmt.Errorf("generate token: %w", err)
	}

	expiresAt := time.Now().Add(time.Duration(ttl) * time.Second)

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, txnID); err != nil {
		return nil, err
	}
	h := &Hold{}
	err = tx.QueryRow(`
		INSERT INTO holds (txn_id, gateway, amount, currency, read_token, callback_url, ttl_seconds, expires_at, metadata)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (txn_id) DO NOTHING
		RETURNING id, txn_id, gateway, status, amount, currency, read_token, expires_at, created_at, updated_at`,
		txnID, gateway, amount, currency, readToken, callbackURL, ttl, expiresAt, metadata,
	).Scan(&h.ID, &h.TxnID, &h.Gateway, &h.Status, &h.Amount, &h.Currency, &h.ReadToken, &h.ExpiresAt, &h.CreatedAt, &h.UpdatedAt)

	if err == sql.ErrNoRows {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return s.getByTxnIDIfCreateMatches(txnID, gateway, callbackURL, currency, amount, ttl, metadata)
	}
	if err != nil {
		return nil, fmt.Errorf("insert hold: %w", err)
	}

	if _, err := tx.Exec(`INSERT INTO verification_polls (txn_id,attempt_number,scheduled_at,status)
	 SELECT $1,1,now(),'pending' WHERE EXISTS (SELECT 1 FROM webhooks WHERE txn_id=$1 AND gateway=$2 AND actionable)`, txnID, gateway); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return h, nil
}

func (s *Store) getByTxnIDIfCreateMatches(txnID, gateway, callbackURL, currency string, amount int64, ttl int, metadata []byte) (*Hold, error) {
	h := &Hold{}
	err := s.db.QueryRow(`
		SELECT id, txn_id, gateway, status, amount, currency, read_token,
		       callback_url, ttl_seconds, expires_at, created_at, updated_at
		FROM holds
		WHERE txn_id = $1
		  AND gateway = $2
		  AND amount = $3
		  AND currency = $4
		  AND callback_url = $5
		  AND ttl_seconds = $6
		  AND metadata = $7::jsonb`,
		txnID, gateway, amount, currency, callbackURL, ttl, metadata,
	).Scan(
		&h.ID, &h.TxnID, &h.Gateway, &h.Status, &h.Amount, &h.Currency, &h.ReadToken,
		&h.CallbackURL, &h.TTLSeconds, &h.ExpiresAt, &h.CreatedAt, &h.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, ErrCreateConflict
	}
	if err != nil {
		return nil, err
	}
	return h, nil
}

// GetByTxnID returns a hold by transaction id.
func (s *Store) GetByTxnID(txnID string) (*Hold, error) {
	h := &Hold{}
	err := s.db.QueryRow(`
		SELECT id, txn_id, gateway, status, amount, currency, read_token, expires_at, created_at, updated_at
		FROM holds WHERE txn_id = $1`, txnID,
	).Scan(&h.ID, &h.TxnID, &h.Gateway, &h.Status, &h.Amount, &h.Currency, &h.ReadToken, &h.ExpiresAt, &h.CreatedAt, &h.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return h, nil
}

// GetByTxnIDAndToken returns a hold only when the read token matches.
func (s *Store) GetByTxnIDAndToken(txnID, token string) (*Hold, error) {
	h := &Hold{}
	err := s.db.QueryRow(`
		SELECT id, txn_id, gateway, status, amount, currency, read_token, expires_at, created_at, updated_at
		FROM holds WHERE txn_id = $1 AND read_token = $2`, txnID, token,
	).Scan(&h.ID, &h.TxnID, &h.Gateway, &h.Status, &h.Amount, &h.Currency, &h.ReadToken, &h.ExpiresAt, &h.CreatedAt, &h.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return h, nil
}

func generateToken() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "pst_rt_" + hex.EncodeToString(b), nil
}
