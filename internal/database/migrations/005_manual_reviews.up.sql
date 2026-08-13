BEGIN;

CREATE TABLE manual_reviews (
    txn_id       text PRIMARY KEY REFERENCES holds(txn_id),
    resolution   text NOT NULL,
    operator     text NOT NULL,
    note         text NOT NULL,
    resolved_at  timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT chk_manual_review_resolution
        CHECK (resolution IN ('confirmed', 'failed', 'no_action'))
);

CREATE INDEX idx_manual_reviews_resolved_at ON manual_reviews (resolved_at DESC);

COMMIT;
