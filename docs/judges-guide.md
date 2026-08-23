# Paystable: a guide for judges

## Start here

Paystable is a payment state stabilizer.
It helps a merchant decide when a checkout result is safe to act on.

A payment gateway signal is evidence, not always a final answer.
A webhook can arrive late, twice, out of order, or with a state that later changes.
A status API can return stale data during gateway reconciliation.
If a merchant trusts one early failure signal, it can release a seat or order even when the customer was charged.

Paystable sits between the payment gateway and merchant fulfillment.
It records the evidence, verifies the state over time, and sends one signed final callback when it reaches a safe answer.

Its central rule is simple:

> Never take an irreversible action on one unverified gateway signal.

Paystable does not replace Razorpay, PayU, or any other gateway.
It does not process card data or move money.
It gives a merchant a safer payment state and evidence for the action that follows.

## A concrete failure

Consider a ticket sale.

1. A customer pays through a gateway.
2. The bank debits the customer.
3. The gateway first sends a failure webhook.
4. The merchant releases the ticket seat.
5. Gateway reconciliation later reports that the payment was captured.
6. The seat has already been sold to another customer.

The problem is not only a failed webhook.
The problem is an irreversible merchant action based on one unverified signal.

Paystable receives the webhook, verifies its signature, and stores it.
It then polls the gateway at controlled intervals.
It waits for stable evidence before it sends a final state to the merchant.
If it cannot reach a safe answer, it reports `INDETERMINATE` instead of silently treating the payment as failed.

The customer does not need to wait forever on a checkout page.
The merchant can show a neutral verification state and complete the order after the signed callback arrives.

## What the merchant integrates

The merchant adds Paystable before it sends a customer to checkout.

```text
merchant backend -> Paystable hold -> payment gateway checkout
                                     |
gateway webhook and status API ------+
                                     |
                                     v
                               Paystable state
                                     |
                                     v
                         signed final merchant callback
                                     |
                                     v
                         fulfill, release, or review
```

The merchant creates a hold with its transaction ID, expected amount, currency, and callback URL.
Paystable returns a read token for safe frontend status checks.

When Paystable reaches a final state, it sends the merchant a signed callback.
The callback has a stable idempotency key.
The merchant verifies the signature and deduplicates the key before it fulfills an order or releases inventory.

The merchant should not fulfill from frontend text or a browser redirect.
It should fulfill from the signed backend callback.

## The runtime state machine

Paystable uses one explicit state machine.

| State | Meaning | Merchant action |
|---|---|---|
| `PENDING` | The hold exists. Paystable has no final evidence. | Keep inventory reserved. |
| `VERIFYING` | A webhook or poll started verification. | Show a neutral payment-checking state. |
| `CONFIRMED` | Stable success matched the hold value. | Fulfill through the verified callback. |
| `FAILED` | Stable failure evidence supports failure. | Release inventory or offer a retry. |
| `MISMATCH` | The gateway value differs from the hold. | Stop automatic action and review. |
| `INDETERMINATE` | Paystable cannot reach a safe final answer. | Escalate for support review. |
| `REFUNDED` | Reserved for a later reversal workflow. | Do not treat this as a complete refund product. |

Two rules matter most.

1. One failure observation cannot release an order.
2. A late event cannot regress a confirmed terminal state.

The database stores these boundaries, not only application memory.
This matters when a process restarts or several workers process events at once.

## What runs inside Paystable

Paystable is a Go service backed by PostgreSQL.
It keeps the deployment small: one binary and one database.

### Gateway adapters

Razorpay and PayU adapters verify gateway-specific signatures and translate gateway events into Paystable's internal payment vocabulary.
The core state machine does not depend on a gateway event name.

Razorpay Test Mode provides genuine integration evidence.
Test Mode uses no real money.
The project also uses PayU as a cross-adapter control.

### Durable webhook intake

Paystable verifies the raw webhook body before it accepts the event.
It stores valid events durably and quarantines rejected events for review.
It deduplicates a repeated gateway event by gateway and event ID.

This protects the system from duplicate delivery and from trusting altered input.
It also preserves evidence when a webhook arrives before the merchant creates its hold.

### Stabilization and reconciliation

Gateway signals can disagree.
Paystable schedules controlled status checks and requires stable agreement before it changes to a terminal state.
It checks payment state, amount, currency, payment identity, and order identity.

When evidence remains ambiguous, it chooses `INDETERMINATE`.
This is deliberate fail-closed behavior.
It is safer than marking a customer payment as failed without enough evidence.

### Transactional outbox

Final merchant callbacks use a PostgreSQL outbox.
Paystable signs every callback and retries transient failures with bounded backoff.
The callback is at-least-once by design.
The idempotency key lets the merchant make the business effect exactly once at its own boundary.

An exhausted callback remains visible for operator replay.
This avoids a hidden failure where Paystable reaches the right state but the merchant never receives it.

### Evidence and operations

Paystable stores an append-only ledger of webhook evidence, verification polls, state transitions, and callback attempts.
The dashboard provides transaction timelines, mismatch visibility, delivery status, replay controls, review queues, and export paths.

This gives a support or finance user an answer to a useful question:

> What did the gateway say, what did Paystable verify, and why did the merchant receive this final state?

## Security boundary

Payment state is a trust boundary.
Paystable applies the following controls before it can authorize merchant action.

- It verifies raw gateway webhook bytes before it parses accepted data.
- It preserves invalid webhook attempts separately from accepted evidence.
- It validates gateway, payment, order, amount, and currency bindings.
- It prevents terminal-state regression.
- It signs outbound callbacks with HMAC-SHA256.
- It uses a stable callback idempotency key across delivery retries.
- It keeps the admin dashboard loopback-only by default.

Paystable does not claim that a network can deliver a message exactly once.
It controls one logical effect boundary with durable state and idempotency.

## Scout: the buildathon verification layer

Scout is not Paystable's payment authority.
Scout is the laboratory that tests whether Paystable and a merchant integration keep their stated safety rules under legal failure histories.

The test space grows quickly when it combines duplicate delivery, delay, reordering, crashes, timeouts, lost responses, database conflicts, tampering, identity mismatch, and concurrent requests.
Running every schedule is expensive.

Scout has one limited AI task:

> Rank legal fault schedules so a real invariant violation appears earlier within a fixed execution budget.

Scout is a local deterministic linear ranker with 24 weights.
It does not upload merchant code or payment data.
It does not decide whether a run passed.
It does not change merchant code.

The deterministic pipeline is:

```text
legal schedules -> Scout ranking -> execution -> invariant checker
                                            -> replay -> trace reduction -> evidence
```

The model chooses the next experiment.
Executable code decides whether the experiment exposed a payment failure.

## What Scout checks

The laboratory records a bounded execution trace.
It then checks five named contracts.

| ID | Contract | Example failure |
|---|---|---|
| `INV-1` | No unauthorized fulfillment | A merchant fulfills without verified, matching capture evidence. |
| `INV-2` | At-most-once logical fulfillment | One order creates two fulfillment effects. |
| `INV-3` | Bounded fulfillment liveness | An eligible order remains unfulfilled through a healthy time horizon. |
| `INV-4` | Monotonic legal payment state | A stale failure changes a captured payment to failed. |
| `INV-5` | Idempotent event and callback acceptance | One logical event or callback is accepted twice. |

Every finding must replay from the same schedule and initial state.
The reducer then removes scheduled actions until no one remaining action can be removed without losing that same failure.
The result is 1-minimal, not necessarily globally shortest.

This produces something a developer can turn into a regression test.
It also gives a reviewer a causal history instead of a vague anomaly score.

## Evaluation evidence

The project separates its evidence types.

| Evidence set | Purpose | Current result |
|---|---|---|
| Regression laboratory | Detect regressions in known implementations. | Scout finds 14 known vulnerable programs within ten schedules. Eleven controls stay clean. |
| Known-family transfer | Test separate repository-authored implementations. | Twenty-four implementations test transfer beyond the simulator. |
| Frozen held-out set | Test after feature freeze. | Four vulnerable and four correct complete merchant implementations. |
| Razorpay Test Mode | Test the real adapter path. | Genuine signed webhook and checkout evidence. |
| Docker and PostgreSQL lab | Test process, HTTP, and database behavior. | Real timeout, reset, conflict, deadlock, and concurrency schedules. |

The held-out set uses the same candidate schedules and budget for every method.
The random baseline aggregates 100 fixed seeds.

| Method | Success@10 | Median rank | Mean reciprocal rank |
|---|---:|---:|---:|
| Bounded search | 50.0% | 23.5 | 0.152 |
| Random search | 82.0% | 3.0 | 0.509 |
| Coverage-guided search | 50.0% | 12.5 | 0.166 |
| Scout | 75.0% | 2.5 | 0.577 |
| Scout closed-loop | 100% | 1.5 | 0.750 |

All four held-out controls stayed clean and every reported finding replayed.
Standard Scout improved median rank and mean reciprocal rank over random search.
It did not beat random search on Success@10.

The ablation result is also important.
Fixed payment-risk priors alone performed better than the trained ranker on this small held-out set.
The current results do not prove that the learned weights add value beyond those priors.
The repository documents this negative result rather than hiding it.

## A short end-to-end demonstration

Use the runtime product first.

1. Start the local service and database.
2. Create a hold before checkout.
3. Complete a Razorpay Test Mode payment.
4. Show the signed webhook evidence and transaction timeline.
5. Show Paystable reaching a stable final state.
6. Show the signed callback path to the merchant.

Then show Scout as proof work.

1. Open `/dashboard/verification`.
2. Run the verification report.
3. Show the held-out comparison before the regression comparison.
4. Open the duplicate-fulfillment trace.
5. Show `INV-2`, deterministic replay, and the 1-minimal schedule.
6. Show the Razorpay and PayU normalized control.
7. End with the evidence limits.

For the complete deterministic lab report, run:

```bash
go run ./testkit/lab demo
```

The command uses no real money and does not call a gateway.

## How to judge the project

The central question is not whether a model can guess that a payment looks risky.
The central question is whether a merchant has a safe, auditable way to act after gateway uncertainty.

Paystable should be judged on these points.

1. It turns uncertain gateway signals into explicit payment states.
2. It prevents unsafe action when evidence is incomplete or contradictory.
3. It preserves a durable audit trail for disputes and support.
4. It gives the merchant a signed, idempotent final callback.
5. It tests its own runtime promise with replayable fault schedules.
6. It keeps the AI role narrow, measurable, local, and subordinate to deterministic correctness rules.

## Scope and limits

Paystable is a single-merchant service.
It is not a multi-tenant hosted product, payment router, card vault, refund system, or bank-statement reconciler.
It does not certify a production system as universally correct.

The verification results use bounded, synthetic, and repository-authored programs.
Razorpay Test Mode provides real integration evidence but not production traffic diversity or production throughput evidence.

Those limits are part of the product claim.
Paystable does not promise more certainty than its evidence supports.

## Where to explore next

- [Product requirements](prd.md)
- [Callback contract](callback-contract.md)
- [Database schema](schema.md)
- [Frontend guidance](frontend-ux.md)
- [Verification scope](verification-scope.md)
- [Scout model evidence](scout-model.md)
- [Submission demonstration](submission-demo.md)
- [Testkit](../testkit/README.md)
