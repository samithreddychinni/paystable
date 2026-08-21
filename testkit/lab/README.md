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
family from training. Scout uses deterministic CPU training.
The 13-weight model does not require a GPU.

## Included schedules

| File | Expected invariant |
|---|---|
| `fulfillment-before-dedup.json` | `INV-2` |
| `new-key-after-lost-response.json` | `INV-2` |
| `stale-terminal-regression.json` | `INV-4` |
| `correct.json` | none |

The first version runs inside one process.

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

The merchant stores events, payment state, effects, and traces in PostgreSQL.
Docker restarts the merchant after each named crash or lost response.

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
