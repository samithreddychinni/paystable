# Paystable Product Requirements

| Field | Value |
|---|---|
| Version | 2.0 |
| Status | Current public direction |
| Last updated | 2026-06-24 |

Paystable is a payment state stabilizer. It is designed for merchants that already use a gateway such as PayU and need a safer way to decide when a checkout is truly safe to fulfill, fail, or escalate.

Paystable is not a payment gateway, not a payment router, not a checkout SDK, and not a full reconciliation platform. The product scope is deliberately smaller: durable gateway signals, controlled verification, a strict state machine, signed merchant callbacks, and an audit trail.

## Core Invariant

Never take an irreversible action on one unverified gateway signal.

That means:

- no hard failure from one failure webhook
- no inventory release on TTL expiry alone
- no fulfillment when the amount returned by the gateway differs from the hold
- no silent drop when Paystable cannot reach a safe answer

## Problem

Small teams using payment gateways usually wire gateway webhooks directly to order fulfillment. That is fragile because:

- webhooks are best-effort and can be late, duplicated, missing, or contradictory
- gateway status APIs can lag or return intermediate data
- app downtime during a webhook can lose the only notification the merchant expected
- support teams need a timeline, not a vague "payment failed" log line

The dangerous case is a false failure: the user was debited, but the merchant released the seat/order because the first gateway signal said failure.

## Goals

| ID | Goal | Current approach |
|---|---|---|
| G1 | Avoid false-negative fulfillment decisions | Stable verification before `FAILED`; unresolved cases become `INDETERMINATE`. |
| G2 | Preserve gateway evidence | Valid webhooks, rejected webhooks, polls, transitions, and callback attempts are persisted. |
| G3 | Keep deployment small | One Go binary plus PostgreSQL. No Redis, Kafka, or worker fleet required. |
| G4 | Give merchants an integration contract | Hold API, read token, SSE/polling, signed callbacks, idempotency key. |
| G5 | Support real ops workflows | Dashboard, timeline, mismatch view, ledger export, delivery replay, secret rotation. |

## Non-Goals

| Item | Reason |
|---|---|
| Payment routing across PSPs | This is Hyperswitch/payment-orchestration territory. |
| Card vaulting or PCI card data handling | Paystable should never see card numbers. |
| Automatic refunds | Paystable reports state. Merchants decide refund policy. |
| Multi-tenant SaaS hosting | Current design is single-merchant deployment. |
| Complete bank statement reconciliation | Useful later, but separate from online checkout stabilization. |

## Primary Users

| User | Need |
|---|---|
| Fest or event tech team | Avoid selling the same seat twice when gateway signals lag. |
| Indie SaaS or small merchant | Stop treating every failed webhook as final. |
| Ops/support person | Explain exactly what happened to a disputed transaction. |
| Developer integrating payments | Get a simple state machine and signed callback contract instead of hand-rolled retry logic. |

## Product Flow

1. Merchant backend calls `POST /api/v1/hold` before redirecting to the gateway.
2. Paystable creates a `PENDING` hold and returns a `read_token`.
3. Gateway webhooks are sent to `POST /webhooks/{gateway}`.
4. Paystable validates and stores gateway webhooks.
5. Verification polls are scheduled and processed by the stabilizer.
6. Paystable transitions the hold to a terminal state only when it has enough evidence.
7. Paystable sends a signed callback to the merchant app through the outbox.
8. Merchant fulfills, releases, or escalates based on the callback state.

## State Machine

| Status | Description | Automatic? |
|---|---|---|
| `PENDING` | Hold created. Waiting for webhook, poll, or TTL final check. | Yes |
| `VERIFYING` | Gateway signal received or verification started. | Yes |
| `CONFIRMED` | Stable success with matching amount. | Yes |
| `FAILED` | Stable failure or TTL final check verified failure. | Yes |
| `MISMATCH` | Gateway success amount did not match the hold amount. | Yes, then human review |
| `INDETERMINATE` | Paystable could not reach a safe decision. | Yes, then human review |
| `REFUNDED` | Reserved in schema for future reversal handling. | Not a complete product flow yet |

Terminal states are `CONFIRMED`, `FAILED`, `MISMATCH`, `INDETERMINATE`, and `REFUNDED`. The database trigger blocks accidental automatic movement out of terminal states, except the reserved `CONFIRMED -> REFUNDED` transition.

## Functional Requirements

### Hold API

- `POST /api/v1/hold` requires `txn_id`, `gateway`, `amount`, and `callback_url`.
- `currency` defaults to `INR`.
- `ttl_seconds` defaults to 300 and is capped by `HOLD_MAX_TTL_S`.
- The response includes a public `read_token`.
- Duplicate `txn_id` requests are handled idempotently by the store.

### Status Reads

- Frontend reads use `GET /api/v1/transactions/{txn_id}/status?token={read_token}`.
- Frontend streaming uses `GET /api/v1/transactions/{txn_id}/stream?token={read_token}`.
- Backend status reads may use `Authorization: Bearer <ADMIN_API_KEY>`.
- Timeline reads are available at `GET /api/v1/transactions/{id}/timeline`.

### Webhooks

- PayU and Razorpay are supported adapters. Razorpay has Test Mode evidence.
- Gateway webhook signatures must validate before insertion into the main `webhooks` table.
- Rejected webhooks are stored in `webhooks_rejected`.
- Duplicate gateway events are deduplicated with `(gateway, gateway_event_id)`.
- Webhooks can be stored even if the hold was not created yet; this preserves early/out-of-order gateway events.

### Stabilization

- Poll jobs live in `verification_polls`.
- Workers claim poll jobs with `FOR UPDATE SKIP LOCKED`.
- A per-gateway token bucket limits gateway status API pressure.
- Success requires success/captured/completed status and amount equality.
- Failure requires enough stable failure observations.
- Amount mismatch becomes `MISMATCH`.
- Gateway errors, no consensus, no client, or inconclusive TTL final checks become `INDETERMINATE`.

### Outbox Delivery

- Final outcomes are sent to the hold `callback_url`.
- Payloads are HMAC-SHA256 signed with `MERCHANT_CALLBACK_SECRET`.
- `X-Paystable-Idempotency-Key` is stable for the same event and must be treated as opaque by merchants.
- Delivery succeeds on HTTP `2xx`.
- HTTP `4xx` except `429` is treated as permanent.
- HTTP `5xx`, `429`, timeout, or connection failure is retried with backoff.
- Exhausted deliveries remain available for dashboard replay.

### Dashboard

The embedded dashboard is served from the Go binary at `/dashboard`. Admin API routes are loopback-only. Current dashboard responsibilities:

- overview metrics
- transaction list and detail timeline
- mismatch view
- delivery status and replay
- config visibility/update
- secret rotation
- ledger export

Do not expose the dashboard directly to the public internet. Use SSH tunnel, VPN, or a separately authenticated reverse proxy.

## Frontend UX Guidance

Paystable's backend state can take longer than a normal checkout redirect. That does not mean the customer should wait on a single blocking spinner.

Recommended copy:

| Paystable status | First-screen copy | Longer wait copy |
|---|---|---|
| `PENDING` | "Processing your payment..." | "We are waiting for the payment gateway to respond." |
| `VERIFYING` | "Payment received. Verifying with the bank..." | "You can close this page. We will update your order automatically." |
| `CONFIRMED` | "Payment confirmed." | Show ticket/order/access. |
| `FAILED` | "Payment did not go through." | Offer retry. Avoid implying the user was charged. |
| `MISMATCH` | "Payment needs review." | Show support reference and prevent automatic fulfillment. |
| `INDETERMINATE` | "We are checking this manually." | Show order ID and support SLA. |

Rules:

- Disable "Pay again" while the hold is `PENDING` or `VERIFYING`.
- After 8-15 seconds, let the user leave the page.
- Send email/SMS or update the merchant account page after final callback.
- Fulfillment should be triggered by the signed backend callback, not by frontend polling alone.

## Operational Requirements

| Area | Requirement |
|---|---|
| Runtime | Single Go binary. |
| Database | PostgreSQL 16+ recommended. |
| Build artifacts | GitHub release binaries for linux/darwin amd64/arm64 and `checksums.txt`. |
| Health | `/healthz`. |
| Metrics | `/metrics`. |
| Logs | Structured logs through Go `slog`. |
| Local testkit | `docker-compose.testkit.yml` with mock gateway and merchant. |

## Current Gaps

| Gap | Impact |
|---|---|
| Limited gateway coverage | Cashfree and PhonePe need adapters before broad adoption. |
| No official SDKs | Merchants must implement HMAC verification and idempotency themselves. |
| Single-tenant config | Not suitable for hosting many merchants in one shared service. |
| Dashboard auth is loopback-only | Good for local ops, not enough for public admin hosting. |
| Manual resolution workflow is incomplete | `MISMATCH` and `INDETERMINATE` require better dashboard actions. |
| Refund flow is reserved, not complete | `REFUNDED` exists in schema but should not be marketed as finished. |

## Success Metrics

| Metric | Target |
|---|---|
| False failure fulfillment | 0 known cases. |
| Valid webhook durability | 100 percent persisted or quarantined. |
| Callback delivery | 99.9 percent delivered within the retry window. |
| Mismatch visibility | Every amount/status contradiction visible in dashboard and ledger. |
| Install success | Public install script downloads and checksum-verifies latest release. |
