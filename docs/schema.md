# Database Schema

Paystable uses PostgreSQL as both durable storage and job queue. Migrations run automatically when the binary starts.

This document describes the schema in the current release, not an aspirational future design.

## Design Notes

### Why `holds`?

Paystable does not own the payment. The gateway does. Paystable owns the merchant-side hold on a resource while the payment state is being verified.

### Why an append-only ledger?

The current state alone is not enough for payment support. The ledger records what the gateway claimed, what Paystable observed, and why a state changed.

### Why Postgres job queues?

`verification_polls` and `outbox` are claimed with `SELECT ... FOR UPDATE SKIP LOCKED`. This keeps scheduling and business state in the same transaction boundary without Redis, Kafka, or a separate worker queue.

## `holds`

One row per merchant checkout attempt.

| Column | Type | Notes |
|---|---|---|
| `id` | `bigint identity primary key` | Internal only. |
| `txn_id` | `text unique not null` | Merchant-supplied public transaction ID. |
| `gateway` | `text not null` | Supported values: `payu`, `razorpay`. |
| `status` | `text not null default 'PENDING'` | `PENDING`, `VERIFYING`, `CONFIRMED`, `FAILED`, `REFUNDED`, `INDETERMINATE`, `MISMATCH`. |
| `amount` | `bigint not null` | Smallest currency unit, e.g. paise. |
| `currency` | `text not null default 'INR'` | ISO 4217. |
| `read_token` | `text unique not null` | Public token for status/SSE reads. |
| `callback_url` | `text not null` | Merchant callback target. |
| `metadata` | `jsonb not null default '{}'` | Passed through in callbacks. |
| `ttl_seconds` | `int not null default 300` | Must be between 30 and 900. |
| `expires_at` | `timestamptz not null` | TTL deadline. |
| `created_at` | `timestamptz not null default now()` | Creation time. |
| `updated_at` | `timestamptz not null default now()` | Updated by transition trigger. |

Indexes:

- `idx_holds_status` on active states
- `idx_holds_expires_at` for pending TTL scans
- `idx_holds_gateway_status`

## `webhooks`

Every valid gateway webhook.

| Column | Type | Notes |
|---|---|---|
| `id` | `bigint identity primary key` | Internal only. |
| `txn_id` | `text not null` | Not foreign-keyed in the current migration so early/out-of-order webhooks can be preserved. |
| `gateway` | `text not null` | Gateway name. |
| `gateway_event_id` | `text` | Gateway event identifier. |
| `event_type` | `text not null` | Gateway event label. |
| `payload` | `jsonb not null` | Raw parsed payload. |
| `received_at` | `timestamptz not null default now()` | Ingestion time. |

Indexes and constraints:

- `idx_webhooks_txn_id`
- `idx_webhooks_gateway_event_id`
- `idx_webhooks_received_at`
- unique `(gateway, gateway_event_id)`

The table is not partitioned in the current release.

## `webhooks_rejected`

Quarantine for webhooks that failed validation.

| Column | Type | Notes |
|---|---|---|
| `id` | `bigint identity primary key` | Internal only. |
| `gateway` | `text not null` | Gateway name. |
| `rejection_reason` | `text not null` | Example: `hmac_mismatch`, `malformed_payload`. |
| `headers` | `jsonb not null` | Request headers for forensics. |
| `raw_body` | `bytea not null` | Exact body bytes. |
| `source_ip` | `inet` | Remote address if available. |
| `received_at` | `timestamptz not null default now()` | Ingestion time. |

Indexes:

- `idx_rejected_received_at`
- `idx_rejected_gateway`

## `verification_polls`

Gateway status checks and stabilizer job queue.

| Column | Type | Notes |
|---|---|---|
| `id` | `bigint identity primary key` | Internal only. |
| `txn_id` | `text not null references holds(txn_id)` | Hold being verified. |
| `attempt_number` | `int not null` | 1-indexed attempt number. |
| `status` | `text not null default 'pending'` | `pending`, `in_flight`, `completed`, `failed`. |
| `gateway_status` | `text` | Gateway status string. |
| `gateway_amount` | `bigint` | Amount reported by gateway. |
| `raw_response` | `jsonb` | Gateway response body. |
| `scheduled_at` | `timestamptz not null` | Worker eligibility time. |
| `started_at` | `timestamptz` | Worker start time. |
| `completed_at` | `timestamptz` | Poll completion time. |
| `error` | `text` | Transport or gateway error. |

Indexes:

- `idx_polls_job_queue` on `scheduled_at` where `status = 'pending'`
- `idx_polls_txn_id`

## `ledger`

Append-only transaction history.

| Column | Type | Notes |
|---|---|---|
| `id` | `bigint identity primary key` | Internal only. |
| `txn_id` | `text not null references holds(txn_id)` | Related hold. |
| `event_type` | `text not null` | Example: `hold_created`, `state_transition`, `webhook_received`. |
| `source` | `text not null` | Example: `api`, `webhook`, `stabilizer`, `outbox`, `admin`. |
| `from_status` | `text` | Previous hold status. |
| `to_status` | `text` | New hold status. |
| `detail` | `jsonb not null default '{}'` | Event-specific data. |
| `created_at` | `timestamptz not null default now()` | Event time. |

Indexes:

- `idx_ledger_txn_id_created`
- `idx_ledger_event_type` for state transitions

## `outbox`

Signed merchant callback deliveries and delivery job queue.

| Column | Type | Notes |
|---|---|---|
| `id` | `bigint identity primary key` | Internal only. |
| `txn_id` | `text not null references holds(txn_id)` | Related hold. |
| `event_type` | `text not null` | Internal event label. |
| `payload` | `jsonb not null` | Callback body. |
| `idempotency_key` | `text unique not null` | Sent as `X-Paystable-Idempotency-Key`. |
| `status` | `text not null default 'pending'` | `pending`, `in_flight`, `delivered`, `exhausted`. |
| `attempts` | `int not null default 0` | Delivery attempts recorded. |
| `max_attempts` | `int not null default 8` | Exhaustion threshold. |
| `next_attempt_at` | `timestamptz not null default now()` | Worker eligibility time. |
| `last_attempt_at` | `timestamptz` | Last delivery attempt. |
| `last_http_status` | `int` | Last merchant response code. |
| `last_error` | `text` | Last delivery error. |
| `delivered_at` | `timestamptz` | Success time. |
| `created_at` | `timestamptz not null default now()` | Creation time. |

Indexes:

- `idx_outbox_job_queue` on `next_attempt_at` where `status = 'pending'`
- `idx_outbox_txn_id`
- `idx_outbox_status` for exhausted deliveries

## `gateway_secrets`

Encrypted gateway webhook secrets for rotation.

| Column | Type | Notes |
|---|---|---|
| `id` | `bigint identity primary key` | Internal only. |
| `gateway` | `text not null` | Gateway name. |
| `secret_encrypted` | `bytea not null` | AES-GCM encrypted secret. |
| `is_active` | `boolean not null default true` | Whether the key can verify webhooks. |
| `rotation_window_end` | `timestamptz` | End of dual-key acceptance window. |
| `created_at` | `timestamptz not null default now()` | Creation time. |
| `deactivated_at` | `timestamptz` | Future audit field. |

Index:

- `idx_secrets_gateway_active` where `is_active = true`

## Transition Enforcement

Postgres enforces terminal-state safety with a trigger:

```sql
IF OLD.status IN ('CONFIRMED', 'FAILED', 'REFUNDED', 'INDETERMINATE', 'MISMATCH') THEN
    IF NOT (OLD.status = 'CONFIRMED' AND NEW.status = 'REFUNDED') THEN
        RAISE EXCEPTION 'illegal transition from % to %', OLD.status, NEW.status;
    END IF;
END IF;
```

This protects the state machine even if application code tries to move a terminal hold accidentally.
