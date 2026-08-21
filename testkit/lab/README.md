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

4. Stop the container laboratory:

   ```bash
   docker compose -f docker-compose.lab.yml down
   ```
