# paystable

> a payment state stabilizer for teams that cannot afford to trust one webhook too early.

> [!NOTE]
> Most code in this branch was drafted with AI and thoroughly reviewed and tested by people.
> If you find a bug, please open an issue. We prefer issue reports to production surprises.

Paystable is a small open-source Go service that sits after checkout and before fulfillment. It does not replace your payment gateway, route payments, vault cards, or compete with payment orchestrators. You keep using your existing gateway. Paystable gives your app a safer state machine around the messy part that happens after a customer pays: webhook delivery, gateway status lag, conflicting signals, callback retries, and audit trails.

The core rule is simple:

> never take an irreversible action on one unverified payment signal.

That matters when a gateway says `failed` while the bank debit is still reconciling, when a webhook arrives late, or when your app is down for the one request that mattered.

---

## what paystable is

Paystable is a truth/stabilization layer for a single merchant deployment:

- accepts gateway webhooks and verifies their signature
- stores valid webhooks before doing any processing
- polls the gateway status API on a controlled schedule
- requires stable agreement before `CONFIRMED` or `FAILED`
- marks amount disagreements as `MISMATCH`
- marks unresolved cases as `INDETERMINATE`
- sends signed, idempotent callbacks to your app from a Postgres outbox
- keeps an append-only ledger for support, finance, and gateway disputes

Paystable is intentionally narrow. It is not a PSP, not a checkout SDK, not a Hyperswitch-style router, and not a reconciliation product for every bank statement format.

---

## the complete flow

Paystable has two connected parts.

| Part | When it runs | Job |
|---|---|---|
| Runtime service | After checkout | Verify payment evidence, stabilize state, and deliver one trusted result. |
| Scout laboratory | Before release | Test payment code against legal failure histories and produce replayable evidence. |

```text
before release:   legal schedules -> Scout -> isolated execution -> invariant check -> replay -> 1-minimal proof
during operation: gateway signal  -> verify -> durable evidence  -> reconcile -> stable state -> signed callback
```

The runtime service protects live decisions. Scout tests the assumptions behind those decisions before deployment.

---

## the user experience

The customer should not stare at a spinner for a minute.

Recommended flow:

1. Your backend creates a hold in Paystable before redirecting the user to the gateway.
2. The gateway redirects the user back to your payment result page.
3. The page opens Paystable SSE or polls the status endpoint with the `read_token`.
4. For the first few seconds, show a normal verifying state.
5. If Paystable is still `VERIFYING` after roughly 8-15 seconds, let the user leave:

   "We received your payment attempt and are verifying it with the bank. You can close this page. We will update your order automatically."

6. Only fulfill on the signed backend callback, not on frontend text.

For physical goods, tickets, and seat reservations, keep the order reserved until the hold resolves. For wallet credits or digital goods, do not credit balance until `CONFIRMED`. For low-risk products, merchants can choose their own provisional-access policy, but Paystable's trusted final state remains the callback.

---

## states

| Status | Meaning | Merchant action |
|---|---|---|
| `PENDING` | Hold exists. No terminal evidence yet. | Reserve inventory. Show neutral processing copy. |
| `VERIFYING` | A webhook or scheduled check triggered gateway verification. | Keep the hold. Do not show a hard failure. |
| `CONFIRMED` | Gateway success was observed consistently and amount matched. | Fulfill safely. |
| `FAILED` | Gateway failure was observed consistently, or TTL final check verified failure. | Release inventory or offer retry. |
| `MISMATCH` | Gateway reported success but the verified amount did not match the hold. | Stop automation. Review manually. |
| `INDETERMINATE` | Paystable could not reach safe consensus before the verification window ended. | Escalate to ops/support. |
| `REFUNDED` | Reserved in the schema for post-confirmation reversal flows. | Do not rely on this as a complete refund workflow yet. |

---

## how the runtime works

### Webhook ingestion

Gateway webhooks hit:

```http
POST /webhooks/{gateway}
```

Paystable verifies the gateway signature. Valid webhooks are persisted in Postgres. Invalid webhooks are stored in `webhooks_rejected` for forensics and metrics.

Razorpay and PayU adapters convert verified gateway data into one normalized payment signal. The state machine does not depend on gateway event names.

### Stabilizer

The stabilizer stores poll jobs in `verification_polls` and claims them with `SELECT ... FOR UPDATE SKIP LOCKED`. It checks gateway status with jittered scheduling and a per-gateway token bucket.

Success requires:

- a captured/success status
- amount equality with the hold
- enough consecutive matching completed polls, controlled by `STABILIZATION_N`

Failure also requires stable failure observations. Ambiguous, missing, inconsistent, or exhausted checks go to `INDETERMINATE`, not silent release.

### TTL scanner

When a hold expires, Paystable does not fail it on the timer alone. It runs one final gateway verification:

- success + matching amount -> `CONFIRMED`
- success + wrong amount -> `MISMATCH`
- verified failure -> `FAILED`
- no client, timeout, pending, not found, or inconclusive result -> `INDETERMINATE`

### Outbox delivery

Final states are delivered to your backend using signed HTTP callbacks. Delivery is at-least-once, so merchants must deduplicate with `X-Paystable-Idempotency-Key`.

## how Scout works

Payment code often fails only after several valid conditions occur together. A duplicate webhook can be harmless. The same webhook after a crash can cause a second fulfillment.

The number of possible schedules grows quickly as tests add delay, reordering, crashes, timeouts, storage faults, tampering, and identity mismatches. Running every schedule is too expensive for a normal CI budget.

Scout gives the local model one narrow job:

> rank legal fault schedules so the laboratory can find a real invariant violation earlier.

Scout does not decide whether a payment flow is correct. It does not approve payments, change merchant code, or send code to an external model. Versioned invariant checkers decide every finding.

### one Scout run

1. The generator creates legal schedules from supported fault actions.
2. Scout scores each schedule with 24 schedule features and 24 trained weights.
3. The isolated runner executes the highest-ranked schedules within a fixed budget.
4. The recorder stores the scheduled inputs and observed execution steps separately.
5. The invariant checker judges the complete trace.
6. A finding must replay from the same initial state and schedule.
7. The reducer removes actions while the same failure still reproduces.

The final schedule is 1-minimal. Removing any one remaining input action removes that specific failure. The result is not necessarily the shortest possible schedule.

The deterministic runner tests reference merchants in-process. The Docker laboratory also tests real HTTP behavior, process restarts, and PostgreSQL faults.

Standard Scout keeps its ranking fixed during a run. Closed-loop Scout changes schedule priority from deterministic runtime observations. Evaluation reports show it as a separate method.

### the payment invariants

| ID | Required behavior | Example failure |
|---|---|---|
| `INV-1` | Fulfillment requires verified capture evidence with matching identity, amount, and currency. | A merchant fulfills an unverified or mismatched payment. |
| `INV-2` | One order creates at most one logical fulfillment. | A crash and redelivery create two fulfillment effects. |
| `INV-3` | An eligible order completes within a declared healthy time limit. | Retry exhaustion loses a valid fulfillment. |
| `INV-4` | Committed payment states follow legal forward transitions. | A stale failure changes a captured payment to failed. |
| `INV-5` | Each event and logical callback is accepted at most once. | A transport retry creates a repeated business effect. |

These checks cover bounded execution histories. A passing run applies only to the executed schedules, environment, assumptions, and time limits.

### what a finding contains

Each verified finding records:

- the program, run seed, execution mode, and execution budget
- the serialized schedule and reduced schedule
- the complete observed trace
- the failed invariant
- the deterministic replay result
- the 1-minimal reduction result

This evidence shows the exact history that caused the failure. A developer can inspect it, reproduce it, and keep it as a regression case.

### why this is useful

A normal happy-path test checks one expected history. Scout uses the test budget on legal histories that include faults and uncertain outcomes.

A verified counterexample gives a team:

- the exact event order that caused the failure
- the named payment rule that failed
- a deterministic way to reproduce the failure
- a small schedule that is practical to inspect and retain

Scout does not certify a passing program as production-safe. It makes each detected failure concrete, reviewable, and repeatable.

## verification evidence

Run the deterministic demonstration:

```bash
go run ./testkit/lab demo
```

The current Scout artifact is a 1,007-byte compact JSON file. Training fits 24 weights from 65,114 invariant-labeled schedule pairs. Inference runs locally on the CPU.

Scout uses schedule features only. It does not use merchant identity, program identity, gateway identity, source code, or expected vulnerability labels during inference.

The regression laboratory has 14 vulnerable programs and 11 correct controls.
Scout finds all 14 known failures within ten schedules.

The frozen held-out set has four vulnerable merchants and four correct controls.
Standard Scout finds three failures within ten schedules and all four within 25 schedules.
Scout and the fixed-prior ablation tie on two merchants. Each leads on one merchant. This sample shows no measurable difference between them.

These bounded results do not prove production accuracy.
See [Scout model evidence](docs/scout-model.md), [Verification scope](docs/verification-scope.md), and [Submission demo](docs/submission-demo.md).

---

## quickstart

Install the latest release:

```bash
curl -fsSL https://paystable.vercel.app | sh
cd paystable
# edit .env
./paystable doctor
./paystable
```

The installer prints each step with `[INFO]` messages, downloads the correct binary for your OS/arch, and verifies it against the release `checksums.txt`.
The example `DATABASE_URL` expects a local Postgres database with user `paystable`, password `change-this-password`, and database `paystable`; change the password before production.

Create a local database before starting the binary:

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

If `./paystable doctor` reports `Ident authentication failed` or `Peer authentication failed`, your Postgres `pg_hba.conf` is not allowing password auth for this local connection. Find the file:

```bash
sudo -u postgres psql -c "SHOW hba_file;"
```

Add these rules before broader `ident` or `peer` rules, then reload Postgres:

```text
host    paystable    paystable    127.0.0.1/32    scram-sha-256
host    paystable    paystable    ::1/128         scram-sha-256
```

Run `./paystable doctor` to check the `.env`, connect to Postgres, and apply pending migrations before starting the server.

Dashboard:

```text
http://localhost:8080/dashboard
```

Admin dashboard APIs are loopback-only. Put Paystable behind your own reverse proxy or SSH tunnel if you need remote access.

For local end-to-end testing:

```bash
cp .env.testkit.example .env.testkit
docker compose -f docker-compose.testkit.yml --env-file .env.testkit up --build
```

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
X-Paystable-Signature: sha256=<hmac>
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

Verify `X-Paystable-Signature` before parsing or fulfilling anything.

---

## configuration

Required:

| Variable | Purpose |
|---|---|
| `DATABASE_URL` | PostgreSQL connection string. |
| `GATEWAY` | Active gateway: `payu` or `razorpay`. |
| `MERCHANT_CALLBACK_SECRET` | Secret used to sign callbacks to your app. |
| `ADMIN_API_KEY` | Bearer token for hold creation and backend status reads. |

PayU requires `WEBHOOK_SECRET`, `GATEWAY_API_KEY`, and `PAYU_STATUS_URL`. Razorpay requires `RAZORPAY_KEY_ID`, `RAZORPAY_KEY_SECRET`, and `RAZORPAY_WEBHOOK_SECRET`.

Optional:

| Variable | Default | Purpose |
|---|---:|---|
| `PORT` | `8080` | HTTP port. |
| `STABILIZATION_N` | `3` | Consecutive matching polls required for terminal success/failure. |
| `MAX_BACKOFF_S` | `160` | Legacy cap used by older scheduler paths. |
| `HOLD_MAX_TTL_S` | `900` | Maximum hold TTL accepted by the API. |
| `DELIVERY_TIMEOUT_S` | `10` | Merchant callback timeout. |
| `DELIVERY_WORKER_CONCURRENCY` | `20` | Concurrent outbox deliveries. |
| `DELIVERY_ALLOW_INSECURE_CALLBACK` | `false` | Allows `http://` callbacks for local development only. |
| `SECRET_ENCRYPTION_KEY` | empty | Required for encrypted webhook secret rotation. |
| `RAZORPAY_API_BASE_URL` | `https://api.razorpay.com/v1` | Razorpay API endpoint. |
| `LOG_LEVEL` | `info` | Log level. |

---

## secret rotation

Secret rotation is available through the localhost-only admin API:

```bash
curl -X POST http://localhost:8080/api/v1/admin/config/rotate-secret \
  -H 'content-type: application/json' \
  -d '{"gateway":"payu","new_secret":"NEW_SECRET","window_hours":24}'
```

Set `SECRET_ENCRYPTION_KEY` before using rotation. During the rotation window, Paystable accepts webhooks signed with either the old or new secret.

---

## docs

- [Guide for judges](docs/judges-guide.md)
- [Scout systems note](docs/systems-note.md)
- [Product requirements](docs/prd.md)
- [Database schema](docs/schema.md)
- [Callback contract](docs/callback-contract.md)
- [Lag estimator](docs/lag-estimator.md)
- [Frontend UX guide](docs/frontend-ux.md)
- [Learned verification scope and performance command](docs/verification-scope.md)
- [Submission demonstration](docs/submission-demo.md)
- [Testkit](testkit/README.md)

---

## license

MIT.
