BEGIN;
ALTER TABLE webhooks ADD COLUMN actionable boolean NOT NULL DEFAULT true;
COMMIT;
