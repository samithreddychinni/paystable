# Executable invariant contracts

Paystable checks bounded finite traces. It does not prove an unbounded system correct.

Run all five contracts and their fixtures:

```bash
go run ./testkit/lab invariants
```

| ID | Contract | Executable failure condition |
|---|---|---|
| `INV-1` | No unauthorized fulfillment | Fulfillment has no prior verified identity and value match. |
| `INV-2` | At-most-once logical fulfillment | One order has more than one logical fulfillment. |
| `INV-3` | Bounded fulfillment liveness | An eligible order remains unfulfilled through a healthy horizon. |
| `INV-4` | Monotonic legal payment state | A committed terminal state changes to an illegal state. |
| `INV-5` | Idempotent event and callback acceptance | One logical event or callback is accepted more than once. |

Each contract includes one passing trace and one failing trace.
The report includes the assumptions for each contract.
The default liveness horizon is two observed steps after eligibility.
An incomplete or unhealthy observation window does not fail `INV-3`.

The regression laboratory also reports early rejection failures.
These supplemental checks cover invalid signatures, identity mismatches, value mismatches, and retry overruns.
They do not replace the five PRD contracts.
