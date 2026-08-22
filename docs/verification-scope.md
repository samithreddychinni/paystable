# Learned verification release scope

| Field | Value |
|---|---|
| Status | Approved implementation scope |
| Baseline commit | `dcfd84374b921ebfbf81b267fc3620d781ed450b` |
| Supported gateway | Razorpay Payment Gateway Test Mode |
| Supported merchant stack | Go and PostgreSQL |
| Submission deadline | Not available yet |

## Purpose

Paystable will search for payment failures that require a sequence of events.
It will use deterministic checks to decide if a failure is real.

The model can select the next test schedule.
The model cannot declare that a payment integration is correct or incorrect.

## Existing baseline

The baseline commit contains the existing Paystable reliability service.
This service includes the following functions:

- durable webhook storage
- PayU signature verification
- gateway status polling
- amount checks
- stable payment states
- a transactional outbox
- signed merchant callbacks
- an evidence ledger
- a PostgreSQL test environment.

These functions existed before the learned verification work.

## New work

This release adds the following functions:

1. A Razorpay Payment Gateway adapter.
2. A reference Razorpay merchant service.
3. A deterministic fault schedule executor.
4. Named crash checkpoints and an observable fulfillment sink.
5. Payment invariants and deterministic replay.
6. A 1-minimal trace reducer.
7. A bounded payment-behavior graph.
8. Generated vulnerable and correct merchant programs.
9. Random, bounded-search, and coverage-guided baselines.
10. A small learned schedule-ranking model named Scout.
11. A closed-loop search controller.
12. A reproducible command-line demonstration.

## Required release

The release must provide all of these results:

1. A genuine Razorpay Test Mode payment produces a signed webhook fixture.
2. Paystable verifies the raw webhook body before it accepts the event.
3. The executor can crash and restart the reference merchant at named checkpoints.
4. The executor can replay the same schedule with the same result.
5. Paystable detects at least three payment bug classes.
6. Paystable reduces each reported failure to a 1-minimal schedule.
7. Scout searches only legal schedules.
8. Scout beats random search and the coverage-guided baseline on held-out programs.
9. Correct held-out programs produce no reported invariant failure.
10. The published benchmark includes its programs, splits, seeds, and budgets.

## Supported boundary

The first release supports one bounded payment path:

```text
Razorpay order -> signed payment webhook -> PostgreSQL state -> fulfillment intent
```

The merchant must provide these bindings:

- the webhook handler
- the event identity
- the payment state table and key
- the terminal success state
- the fulfillment call
- the fulfillment idempotency key
- the test command.

Paystable checks the declared bindings before it starts a verification run.

## Required bug classes

The first benchmark will include these bug classes:

1. Fulfillment occurs before durable webhook deduplication.
2. A retry uses a new idempotency key after a lost response.
3. A stale event changes a terminal payment state.
4. An untrusted webhook changes payment state.
5. Concurrent webhook handlers fulfill before one handler claims the event.
6. A database conflict causes a retry with a new idempotency key.
7. A database deadlock causes a retry with a new idempotency key.
8. Fulfillment continues after the retry limit.
9. A captured payment amount differs from the expected order amount.
10. A captured payment belongs to a different order.
11. A captured payment uses a different currency.

The retry class includes lost responses, timeouts, connection resets, HTTP 500 responses, database conflicts, and deadlocks.
The retry-exhaustion class allows two uncertain fulfillment attempts.
Action order represents delayed webhook delivery without wall-clock waits.
The amount class uses the fixed INR test order amount of 49900 paise.
The order class compares the payment order ID with the expected order ID.
The currency class compares the payment currency with INR.

## Evaluation gates

All search methods must use the same schedule grammar and execution budget.

The benchmark will report these measures:

- `Success@10`
- `Success@25`
- `Success@50`
- median executions before the first counterexample
- wall-clock time before the first counterexample
- redundant schedule rate
- deterministic replay rate
- false finding count
- model size
- peak memory
- local inference latency.

Run the local performance report with a fixed budget, seed, and inference repetition count:

```bash
go run ./testkit/lab performance 50 7 100
```

The search timers include candidate ordering and execution. They exclude model training and graph compilation.
The memory fields report the Go heap in use. The command samples this value every millisecond for the full run.
This value is not the process peak resident set size.
Run the command on an idle target machine before you compare results because local load and hardware affect all timing values.

Scout must find every held-out failure within 50 executions.
Its median execution count must beat each required baseline.
If medians tie at one, Scout must have a higher `Success@10` rate.
It must report no false findings and replay every finding.
The release will publish a negative result if Scout does not pass this gate.
All retry transport outcomes use one held-out family.
An independent benchmark must execute merchant code outside the training simulator.
It must include vulnerable and correct implementations for each supported family.
Scout must use fixed payment-risk priors when a fold excludes a known failure family.
The release must also publish a prior-free held-out report.
This report measures learned generalization without the fixed risk priors.
A seeded stress report must shuffle equal-score schedules before ranking.
The stress report must add matched safe schedules for mismatch actions.
The same report must place safe schedules first for an adversarial tie check.
A perfect bounded score does not prove production accuracy.

## Stretch work

The following work must not delay the required release:

- automatic code changes
- more than one repair transformation
- a detailed dashboard
- reinforcement learning
- a model with millions of parameters
- hosted execution
- languages other than Go
- gateways other than Razorpay
- payment products outside the payment-to-fulfillment path.

## Implementation order

Work must occur in this order:

1. Complete the Razorpay Test Mode flow.
2. Build the deterministic reference merchant laboratory.
3. Add the schedule executor and replay artifact.
4. Add the first invariant and trace reducer.
5. Verify three bug classes.
6. Compile the bounded payment-behavior graph.
7. Generate programs and ground-truth schedules.
8. Measure the non-model baselines.
9. Train the smallest useful Scout model.
10. Add closed-loop runtime feedback.
11. Add one constrained repair only if the search gates pass.
12. Build the final command-line demonstration.
13. Run security, reproducibility, and release checks.

No phase can use the model as the correctness authority.
Every reported failure must pass deterministic replay.

## Non-goals

This release will not do the following work:

- process real money
- attack Razorpay infrastructure
- claim formal or universal correctness
- analyze unrestricted repositories
- execute unknown repositories in a hosted service
- use a third-party language model as a payment oracle
- deploy a repair without merchant approval.

## Evidence limits

Scout ranks test schedules. It does not approve payments or declare failures.
Deterministic invariants decide whether each execution found a bug.

The current corpus is synthetic.
The 24 independent benchmark merchants are repository-authored.
Two optional probes execute pinned external Razorpay webhook handlers.
The external results are not part of the Scout performance report.
The first probe reproduces amount, currency, and order binding failures.
The no-prior transfer report ranks each mismatch above its matched control.
This report tests a new implementation of known failure families.
The second probe accepts signed Unicode bytes and rejects tampering.
It also rejects a signed event that has no event ID.
This result is a correct external security control.
The replay-window challenge is absent from Scout training.
It expires an event claim after one day and delivers the same trusted event.
Scout gives its failure and matched safe delay the same score.
This negative result identifies an unmodeled time feature.
Scout v3 adds one normalized replay-delay feature.
It trains this feature on nine invariant-labeled pairs without fixed priors.
Its report uses unseen delay values, clock offsets, and sequence noise.
Scout v2 ties all three pairs, while Scout v3 ranks all three failures higher.
The Scout v3 model is 1,045 bytes, and each correct control has no violation.
The container laboratory stores event claims and fulfillment effects in PostgreSQL.
Its cleanup query uses an injected database clock offset.
The test does not change the operating system clock or run a production scheduler.
One genuine Test Mode webhook proves the signed integration path.
These results do not describe an unknown production traffic distribution.

The local shadow check removes identifiers and customer data from its report.
It checks signed extra fields and rejects changes that reuse the original signature.
One Test Mode fixture does not represent production traffic diversity.

Run the prior-free stress report before you publish benchmark claims.
Use sanitized shadow traffic and external merchant implementations before a production claim.
