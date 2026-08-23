# Deterministic merchant laboratory

The laboratory runs a payment schedule without network calls or real money.
The invariant checks decide if the schedule found a bug.

## Run a schedule

1. Select a file from `testkit/lab/scenarios`.

2. Run the file:

   ```bash
   go run ./testkit/lab testkit/lab/scenarios/fulfillment-before-dedup.json
   ```

3. Check the `violations` and `trace` fields in the JSON result.

Run the same file again to verify deterministic replay.

## Check the five PRD invariants

Run one passing and one failing fixture for each invariant:

```bash
go run ./testkit/lab invariants
```

See [Executable invariant contracts](../../docs/invariants.md) for the contract boundaries.

## Compile the payment-behavior graph

Compile a program graph with a maximum schedule length:

```bash
go run ./testkit/lab graph correct 4
```

The graph contains only legal actions. Each edge increases the schedule depth
by one. Matching payment behavior uses one node at each depth.
The maximum schedule length is eight actions.

## Generate the program corpus

Generate the executable programs and their ground-truth schedules:

```bash
go run ./testkit/lab corpus
```

Each program includes its family, expected final state, effect count, and
expected invariant violation. The correct program has no expected violation.

## Measure the search baselines

Run all non-model baselines with the same budget and seed:

```bash
go run ./testkit/lab baselines 50 7
```

The report includes success, replay, false-finding, and redundant-schedule
measures. Search executions exclude deterministic replay checks. The coverage
baseline keeps shorter schedules first and favors unseen graph states.

## Train and run Scout

Train the linear Scout ranker and run it with the shared budget:

```bash
go run ./testkit/lab scout 50
```

The report includes the final model, evaluation fold models, search runs, and
the same summary measures as the baselines. Each fold excludes its target
family from training. Scout starts with payment-risk priors and uses deterministic CPU training.
The 24-weight model does not require a GPU.

## Measure prior-free generalization

Run held-out evaluation without fixed payment-risk priors:

```bash
go run ./testkit/lab prior-free 50
```

This report measures how the learned weights rank each held-out failure family.
It does not use the fixed risk priors.
Compare this report with the standard Scout report to measure the prior gap.
A perfect standard score does not prove production accuracy.

Shuffle equal-score schedules across 20 seeded trials:

```bash
go run ./testkit/lab prior-free-stress 50 7
```

This report exposes results that depend on a favorable graph order.
It includes a Wilson 95% interval for `Success@10`.
It adds matched safe amount and currency schedules with the same event IDs.
It also puts safe schedules first when their scores tie with failing schedules.
This adversarial result shows failures that Scout cannot distinguish by score.

## Run closed-loop Scout

Run Scout with deterministic trace feedback:

```bash
go run ./testkit/lab closed-loop 50
```

After each non-finding run, Scout records new trace states and updates its
weights. The invariant checks remain the only finding authority.
Scout also ranks unseen input profiles before repeated profiles.
This rule uses schedule features only. It does not use hidden result labels.

## Measure an unseen replay window

Run the replay-window challenge:

```bash
go run ./testkit/lab replay-window
```

The report trains Scout without fixed priors before it creates the challenge.
An expired event claim permits a delayed replay and causes duplicate fulfillment.
A durable event claim rejects the same replay.
Scout gives the failure and its matched safe delay the same score.
This negative result identifies a feature gap before retraining.

Train and test Scout v3:

```bash
go run ./testkit/lab replay-window-v3
```

Scout v3 keeps the 24 Scout v2 weights.
It learns one replay-delay weight from nine matched training pairs.
The deterministic invariant labels each training schedule.
The report uses three unseen delay pairs.
One pair includes an unrelated failed event as sequence noise.
Scout v2 ties all three pairs.
Scout v3 ranks all three failure delays above their controls.
The Scout v3 model is 1,045 bytes.
The deterministic invariant remains the finding authority.
The Scout report uses the in-process test clock and one retention policy.
The PostgreSQL schedules below test cleanup with injected clock offsets.

## Run the independent benchmark

Evaluate all methods against merchant implementations outside the training simulator:

```bash
go run ./testkit/lab independent 50 7
```

The benchmark contains twelve vulnerable implementations and twelve correct implementations.
The random baseline uses the 100 frozen seeds from 7 through 106.
The report includes Wilson 95% intervals for `Success@10`.
The random summary aggregates all 100 seeds.
Two amount implementations parse raw webhook JSON in a separate package.
They do not import simulator code.
They are repository-authored implementations, not third-party code.
Two order implementations use the same independent boundary.
Two currency implementations use the same independent boundary.
This benchmark tests new implementations of known bug families.
It is not the final PRD version 3 held-out benchmark.

Run the optional [external implementation checks](../external/README.md) separately.
The external results are not part of the Scout performance report.
Run `go run ./testkit/lab external-transfer` after that check.

## Run the frozen held-out benchmark

Run the post-freeze merchant set with the frozen budget and random seeds:

```bash
go run ./testkit/lab heldout 50 7
```

The set contains four vulnerable merchants and four correct controls.
The split unit is a complete merchant implementation.
No held-out code or result can change Scout features, priors, or thresholds.

## Verify the constrained repair

Verify the terminal-state repair after the Scout search gate passes:

```bash
go run ./testkit/lab repair
```

The repair preserves `captured` after a stale failure. The command checks every
legal schedule with four actions or fewer. It does not change merchant files.

## Run the complete demonstration

Run the deterministic verification story:

```bash
go run ./testkit/lab demo
```

The command checks Razorpay signature handling, replay, reduction, search,
runtime feedback, and repair. It does not call Razorpay or use real money.

## Run the release checks

Install the dashboard packages before the first check:

```bash
npm --prefix dashboard ci
```

Run all local release checks:

```bash
./scripts/release-check.sh
```

The script checks credentials, formatting, dependencies, Go code, deterministic
output, Compose files, and the dashboard.

## Included schedules

| File | Expected invariant |
|---|---|
| `fulfillment-before-dedup.json` | `INV-2` |
| `new-key-after-lost-response.json` | `INV-2` |
| `new-key-after-timeout.json` | `INV-2` |
| `new-key-after-connection-reset.json` | `INV-2` |
| `new-key-after-server-error.json` | `INV-2` |
| `new-key-after-db-conflict.json` | `INV-2` |
| `new-key-after-db-deadlock.json` | `INV-2` |
| `retry-overrun.json` | `INV-RETRY-1` |
| `amount-mismatch.json` | `INV-AMOUNT-1` |
| `order-mismatch.json` | `INV-ORDER-1` |
| `currency-mismatch.json` | `INV-CURRENCY-1` |
| `stale-terminal-regression.json` | `INV-4` |
| `correct-terminal.json` | none |
| `invalid-signature.json` | `INV-SEC-1` |
| `tampered-body.json` | `INV-SEC-1` |
| `concurrent-claim.json` | `INV-2` |
| `correct-concurrency.json` | none |
| `correct-db-conflict.json` | none |
| `correct-db-deadlock.json` | none |
| `correct-retry-exhaustion.json` | none |
| `correct-amount.json` | none |
| `correct-order.json` | none |
| `correct-currency.json` | none |
| `correct-security.json` | none |
| `correct.json` | none |
| `expired-event-replay.json` | `INV-2` |
| `correct-durable-replay.json` | none |
| `expired-event-clock-skew.json` | `INV-2` |
| `active-event-clock-skew.json` | none |

The command-line search runs inside one process.
Docker runs the same fault contract over HTTP and PostgreSQL.

## Run against PostgreSQL and process crashes

1. Start the container laboratory:

   ```bash
   docker compose -f docker-compose.lab.yml up --build -d
   ```

2. Run a schedule twice and save its replay artifact:

   ```bash
   go run ./testkit/labexec \
     testkit/lab/scenarios/fulfillment-before-dedup.json \
     artifacts/lab/fulfillment-before-dedup.json
   ```

3. Check that the artifact contains `"deterministic": true`.

The merchant verifies each raw delivery body before it decodes the JSON.
The merchant rejects a captured payment when its explicit amount differs from 49900 paise.
The merchant rejects a payment when its explicit order ID differs from the schedule order.
The merchant rejects a payment when its explicit currency differs from INR.
The merchant stores events, payment state, effects, and traces in PostgreSQL.
The laboratory creates real response timeouts, connection resets, and HTTP 500 responses.
It also sends concurrent webhooks through competing PostgreSQL transactions.
It creates a real PostgreSQL serialization failure after a fulfillment effect.
It also creates a real PostgreSQL deadlock after a fulfillment effect.
The retry-limit schedules use one stable fulfillment key.
Docker restarts the merchant after each named crash or lost response.
The replay schedules store event claims and effects in PostgreSQL.
The advance action changes a database clock offset without waiting one day.
The cleanup query expires event claims against the effective database clock.

Run the PostgreSQL replay and clock-skew schedules:

```bash
go run ./testkit/labexec \
  testkit/lab/scenarios/expired-event-replay.json \
  artifacts/lab/expired-event-replay.json \
  INV-2
go run ./testkit/labexec \
  testkit/lab/scenarios/expired-event-clock-skew.json \
  artifacts/lab/expired-event-clock-skew.json \
  INV-2
go run ./testkit/labexec \
  testkit/lab/scenarios/active-event-clock-skew.json \
  artifacts/lab/active-event-clock-skew.json
```

4. Reduce a failing schedule to a 1-minimal schedule:

   ```bash
   go run ./testkit/labexec \
     testkit/lab/scenarios/stale-terminal-regression.json \
     artifacts/lab/reduced-stale-terminal-regression.json \
     INV-4
   ```

5. Compare `original_action_count` and `reduced_action_count` in the artifact.

A 1-minimal schedule has no single action that you can remove while preserving
the selected invariant violation. It is not necessarily the shortest schedule.

6. Stop the container laboratory:

   ```bash
   docker compose -f docker-compose.lab.yml down
   ```
