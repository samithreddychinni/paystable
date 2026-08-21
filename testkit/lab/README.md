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

The first version runs inside one process.
The next version will bind these actions to container and PostgreSQL checkpoints.
