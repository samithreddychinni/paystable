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

Amount validation and signature ordering are the next bug classes.
They are required only if the first three classes finish early.

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

Scout passes only if it beats both required baselines on held-out program families.
The release will publish a negative result if Scout does not pass this gate.

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
