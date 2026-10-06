# paystable

[![CI](https://github.com/samithreddychinni/paystable/actions/workflows/ci.yml/badge.svg)](https://github.com/samithreddychinni/paystable/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/samithreddychinni/paystable)](https://github.com/samithreddychinni/paystable/releases/latest)

Consider a customer who pays ₹499 for a fest seat.
The gateway sends `failed`, so the app releases the seat.
The bank debit succeeds later, but another customer now owns the seat.
Support receives a complaint about the missing ticket.

Paystable checks gateway evidence before a merchant acts on a payment result.
Paystable runs as one Go binary with PostgreSQL between the gateway and the merchant's fulfillment.

- Paystable verifies and stores gateway webhooks.
- Paystable checks the gateway status API before it confirms or fails a hold.
- Paystable sends signed callbacks and flags amount conflicts or unresolved results for review.

**Core rule:** Never take an irreversible action on one unverified payment signal.

Paystable is early.
It does not replace a gateway, route payments, or reconcile bank statements.

## which gateways work today

| Gateway | Status |
|---|---|
| PayU | Supported. The adapter verifies response hashes and calls the payment status API. |
| Razorpay | Supported from source. Requires auto-capture. The v0.3.0 binaries support PayU only. |
| Cashfree | Not supported. No adapter exists. |

## quickstart

Install the latest release:

```bash
curl -fsSL https://paystable.vercel.app | sh
cd paystable
```

The installer checks the binary against the release checksums.
It runs `paystable init` to create the configuration with local secrets.
The command refuses to overwrite an existing configuration.

For Razorpay, use a source build.
The v0.3.0 release binaries support PayU only.
For a source build, install Go 1.23 or later.
Clone the repository:

```bash
git clone --branch main https://github.com/samithreddychinni/paystable.git
cd paystable
go build -o paystable ./cmd/paystable
./paystable init
```

Set the database password in `.env` to match your PostgreSQL user.
Replace `WEBHOOK_SECRET` with the PayU test salt.
Set `GATEWAY_API_KEY` and `PAYU_STATUS_URL` for PayU test mode.
For Razorpay, use the configuration section below instead of the PayU fields.

Create a local database:

```bash
sudo -u postgres psql
```

```sql
CREATE USER paystable WITH PASSWORD 'change-this-password';
CREATE DATABASE paystable OWNER paystable;
```

Then set:

```env
DATABASE_URL=postgres://paystable:change-this-password@localhost:5432/paystable?sslmode=disable
```

If `doctor` reports an ident or peer error, inspect the local PostgreSQL authentication rules.
Find the file:

```bash
sudo -u postgres psql -c "SHOW hba_file;"
```

Add these rules before broader `ident` or `peer` rules:

```text
host    paystable    paystable    127.0.0.1/32    scram-sha-256
host    paystable    paystable    ::1/128         scram-sha-256
```

Reload PostgreSQL after you change these rules.

Run `./paystable doctor` to check the environment, database connection, and migrations.
The command applies pending migrations.
Gateway credential gaps produce warnings.
They do not prove that PayU access works.

Start the service after you set the required values:

```bash
./paystable doctor
./paystable
```

Dashboard:

```text
http://localhost:8080/dashboard
```

**Warning:** Do not expose the dashboard to the internet.
Admin routes have no login.
The default configuration restricts access to loopback traffic.

In the Docker testkit, port 8080 binds to host loopback.
The testkit also allows the bridge gateway through `ADMIN_ALLOWED_SOURCES`.

If the bridge conflicts, set `PAYSTABLE_TESTKIT_SUBNET` and `PAYSTABLE_TESTKIT_GATEWAY`.

For the local mock testkit, use test values in `.env.testkit`:

```bash
cp .env.testkit.example .env.testkit
docker compose -f docker-compose.testkit.yml --env-file .env.testkit up --build
```

---

## what paystable is

Paystable supports one merchant per deployment:

- Verifies gateway webhooks.
- Stores valid webhooks before it schedules a status check.
- Checks the gateway status API on a controlled schedule.
- Requires matching status checks before the normal poll path reaches `CONFIRMED` or `FAILED`.
- Marks amount conflicts as `MISMATCH`.
- Marks unresolved cases as `INDETERMINATE`.
- Sends signed callbacks from a PostgreSQL outbox. Your app must deduplicate them.
- Records events in a ledger for support and gateway disputes.

Paystable does not provide checkout, payment routes, or bank statement reconciliation.

---

## the user experience

Let the customer leave the result page while Paystable checks the payment.

Recommended flow:

1. Create a hold from your backend before you redirect the customer to the gateway.
2. Configure the gateway redirect to your payment result page.
3. Read the status through SSE or the status endpoint with the `read_token`.
4. Show the payment status for the first few seconds.
5. If the status remains `VERIFYING` after 8–15 seconds, show this text:

   "We received your payment attempt. We will check its status with the gateway. You can close this page. We will update your order automatically."

6. Fulfill only after your backend verifies a `CONFIRMED` callback.

Keep the inventory reserved until the hold resolves.
Do not credit a wallet or deliver digital goods before `CONFIRMED`.
Define a manual review process for `MISMATCH` and `INDETERMINATE`.

---

## states

| Status | Meaning | Merchant action |
|---|---|---|
| `PENDING` | Hold exists. No terminal evidence yet. | Reserve inventory. Show a neutral status message. |
| `VERIFYING` | A webhook or scheduled check triggered gateway verification. | Keep the hold. Do not show a hard failure. |
| `CONFIRMED` | Gateway checks agree on success and the amount matches, or the TTL final check confirms success. | Fulfill once after you verify the callback. |
| `FAILED` | Gateway checks agree on failure, or the TTL final check verifies failure. | Release inventory or offer retry. |
| `MISMATCH` | Gateway reported success but the verified amount did not match the hold. | Stop automation. Review manually. |
| `INDETERMINATE` | Paystable could not reach safe consensus before the verification window ended. | Request manual review. |
| `REFUNDED` | The schema reserves this state for a future refund flow. | Do not rely on this as a complete refund workflow yet. |

---

## how it works

### Gateway webhooks

Gateway webhooks hit:

```http
POST /webhooks/{gateway}
```

Paystable verifies the gateway signature. Paystable stores valid webhooks in PostgreSQL.
Paystable stores invalid webhooks in `webhooks_rejected` for review.

### Stabilizer

The stabilizer stores poll jobs in `verification_polls`.
It claims jobs with `SELECT ... FOR UPDATE SKIP LOCKED`.
It varies the check times and uses a token bucket to limit gateway requests.

Success requires:

- A success status from the gateway API.
- An amount that matches the hold.
- Enough consecutive matching completed polls, as set by `STABILIZATION_N`.

The normal poll path also requires matching failure results before `FAILED`.
Unresolved checks produce `INDETERMINATE` for manual review.

### TTL scanner

When a hold expires, Paystable checks the gateway once more.
The timer alone does not fail the hold:

- success + matching amount -> `CONFIRMED`
- success + wrong amount -> `MISMATCH`
- verified failure -> `FAILED`
- no client, timeout, pending, not found, or inconclusive result -> `INDETERMINATE`

### Callback delivery

Paystable sends final states to your backend with signed HTTP callbacks.
Paystable can send the same callback more than once.
Deduplicate callbacks with `X-Paystable-Idempotency-Key`.

---

## create a hold

```http
POST /api/v1/hold
Authorization: Bearer <ADMIN_API_KEY>
Content-Type: application/json
```

```json
{
  "txn_id": "order_abc123",
  "gateway": "payu",
  "amount": 49900,
  "currency": "INR",
  "ttl_seconds": 300,
  "callback_url": "https://merchant.example/paystable/callback",
  "metadata": {
    "order_id": "order_abc123",
    "customer_email": "student@example.com"
  }
}
```

Response:

```json
{
  "txn_id": "order_abc123",
  "status": "PENDING",
  "read_token": "pst_rt_...",
  "expires_at": "2026-06-24T12:05:00Z",
  "created_at": "2026-06-24T12:00:00Z"
}
```

The frontend can read status with:

```http
GET /api/v1/transactions/{txn_id}/status?token={read_token}
GET /api/v1/transactions/{txn_id}/stream?token={read_token}
```

The backend can read status with:

```http
GET /api/v1/transactions/{txn_id}/status
Authorization: Bearer <ADMIN_API_KEY>
```

---

## callback payload

Paystable sends final outcomes to the hold `callback_url`:

```http
POST <callback_url>
Content-Type: application/json
X-Paystable-Signature: v2=<hmac>
X-Paystable-Idempotency-Key: <opaque-key>
X-Paystable-Timestamp: <unix-seconds>
```

```json
{
  "txn_id": "order_abc123",
  "event": "transaction.confirmed",
  "status": "CONFIRMED",
  "amount": 49900,
  "currency": "INR",
  "gateway": "payu",
  "verified_at": "2026-06-24T12:00:19Z",
  "metadata": {
    "order_id": "order_abc123",
    "customer_email": "student@example.com"
  }
}
```

Verify the v2 signature over the delivery timestamp, idempotency key, and raw body before you decode JSON.
Reject timestamps more than five minutes from your current time.
Fulfill once for each verified `CONFIRMED` transaction, with deduplication and fulfillment in one database transaction.

**Upgrade requirement:** Update your merchant verifier before upgrading Paystable.
The v2 callback signature replaces the body-only `sha256=` format.
See the [callback contract](docs/callback-contract.md) for the exact signed bytes and verifier.

---

## configuration

Required:

| Variable | Purpose |
|---|---|
| `DATABASE_URL` | PostgreSQL connection string. |
| `GATEWAY` | Active gateway: `payu` or `razorpay`. |
| `WEBHOOK_SECRET` | Gateway webhook secret. For PayU, use the salt. |
| `GATEWAY_API_KEY` | PayU merchant key. Required for `payu`. |
| `PAYU_STATUS_URL` | PayU status API endpoint. Required for `payu`. |
| `RAZORPAY_KEY_ID` | Razorpay key ID. Required for `razorpay`. |
| `RAZORPAY_KEY_SECRET` | Razorpay key secret. Required for `razorpay`. |
| `MERCHANT_CALLBACK_SECRET` | Secret that signs callbacks to your app. |
| `ADMIN_API_KEY` | Bearer token for hold creation and backend status reads. |

Optional:

| Variable | Default | Purpose |
|---|---:|---|
| `RAZORPAY_API_URL` | `https://api.razorpay.com/v1` | Razorpay API endpoint. |
| `PORT` | `8080` | HTTP port. |
| `STABILIZATION_N` | `3` | Consecutive matching polls required for terminal success/failure. |
| `MAX_BACKOFF_S` | `160` | Legacy cap used by older scheduler paths. |
| `HOLD_MAX_TTL_S` | `900` | Maximum hold TTL accepted by the API. |
| `DELIVERY_TIMEOUT_S` | `10` | Merchant callback timeout. |
| `DELIVERY_WORKER_CONCURRENCY` | `20` | Concurrent outbox deliveries. |
| `DELIVERY_ALLOW_INSECURE_CALLBACK` | `false` | Allows `http://` callbacks for local development only. |
| `SECRET_ENCRYPTION_KEY` | empty | Required for encrypted webhook secret rotation. |
| `LOG_LEVEL` | `info` | Log level. |

For Razorpay, set `GATEWAY=razorpay` and enable auto-capture in the gateway dashboard.
Set `WEBHOOK_SECRET` to the Razorpay webhook secret.
Use the Razorpay `order_id` as the hold `txn_id`.
Keep the merchant order ID in the hold metadata and the gateway receipt.
Paystable checks all payment attempts for that order.

A failed attempt stays pending before expiry.
At expiry, all failed attempts or no attempts produce `FAILED`.
A created or authorized attempt produces `INDETERMINATE`.
A captured payment and paid order produce `CONFIRMED` when the amount matches.

A different amount or partial payment produces `MISMATCH`.
A refund before confirmation produces `INDETERMINATE`.
A late capture after `FAILED` creates a ledger event and an alert without a state change.

Read the [Razorpay guide](docs-site/src/content/docs/guides/razorpay.md) for the event and configuration details.

---

## secret rotation

Secret rotation is available through the localhost-only admin API:

```bash
curl -X POST http://localhost:8080/api/v1/admin/config/rotate-secret \
  -H 'content-type: application/json' \
  -d '{"gateway":"payu","new_secret":"NEW_SECRET","window_hours":24}'
```

Set `SECRET_ENCRYPTION_KEY` before you rotate a secret.
During the rotation window, Paystable accepts webhooks with the old or new secret.

---

## faq

### Why not only verify the webhook signature?

A valid signature proves who sent the event.
It does not prove that the event is final or correct.
Paystable checks gateway status and amount before it sends a final callback.
A gateway API can also lag.
Paystable cannot guarantee that a later gateway result will never change.

---

## docs

- [Product requirements](docs/prd.md)
- [Database schema](docs/schema.md)
- [Callback contract](docs/callback-contract.md)
- [Lag estimator](docs/lag-estimator.md)
- [Frontend UX guide](docs/frontend-ux.md)
- [Testkit](testkit/README.md)

---

## license

MIT.
