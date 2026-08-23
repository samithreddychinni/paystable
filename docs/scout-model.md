# Scout model evidence

Scout is a trained linear schedule ranker with eight fixed risk priors.
It is not a language model, diagnosis system, patch generator, or correctness oracle.

The deterministic invariant checker labels training schedules and decides all findings.
Scout only orders legal schedules within an execution budget.

## Model artifact

| Field | Value |
|---|---|
| Format | Compact JSON |
| Version | 2 |
| Parameters | 24 weights |
| Size | 1,007 bytes |
| Training pairs | 65,114 |
| Epochs | 40 |
| Learning rate | 0.01 |
| Training seed | None |
| Inference | Local deterministic CPU |

Training uses a fixed iteration order and no random operation.
Therefore, training does not use a seed.

Generate the artifact and ablation report:

```bash
go run ./testkit/lab model-evidence
```

The command emits the exact model fields, weights, size, splits, and ablation results.
The release gate compares this output with [the frozen evidence file](evidence/model-evidence.json).

## Features

Scout uses schedule features only.
It does not use source code, program identity, gateway identity, or expected vulnerability labels during inference.

| Group | Features |
|---|---|
| Schedule size | `action_count`, `deliver_count`, `fulfill_count`, `restart_count` |
| Payment state | `captured_count`, `failed_count`, `failed_after_captured` |
| Crash and replay | `crash_count`, `duplicate_event_count`, `deliver_after_restart`, `same_event_after_restart` |
| Effect ambiguity | `uncertain_response_count`, `fulfill_after_uncertain_response`, `timeout_count`, `connection_reset_count`, `server_error_count` |
| Trust | `missing_signature_count`, `invalid_signature_count`, `tampered_body_count`, `untrusted_captured` |
| Concurrency and value | `parallel_delivery_count`, `amount_mismatch_count`, `order_mismatch_count`, `currency_mismatch_count` |

Eight features start with a fixed weight of one:

- `same_event_after_restart`
- `fulfill_after_uncertain_response`
- `failed_after_captured`
- `untrusted_captured`
- `parallel_delivery_count`
- `amount_mismatch_count`
- `order_mismatch_count`
- `currency_mismatch_count`

All other weights start at zero.

## Training objective

The training corpus generates legal schedules for each vulnerable regression program.
The invariant checker divides schedules into positive and negative examples.

A positive schedule exposes the program's declared invariant.
A negative schedule does not expose that invariant.

Training creates every positive-negative pair within each vulnerable program.
It applies a pairwise hinge objective with a margin of one.
An update increases the score difference when the pair does not meet that margin.

Correct controls do not create training pairs.
They test false findings during regression and held-out evaluation.

## Data partitions

| Partition | Contents | Use |
|---|---|---|
| Training | Vulnerable programs from the 25-program synthetic corpus | Fit Scout weights |
| Validation | No separate validation set | No threshold or model selection |
| Regression | 14 vulnerable programs and 11 correct controls | Detect implementation regressions |
| Known-family transfer | 12 vulnerable and 12 correct repository implementations | Test implementation transfer |
| Frozen held-out | 4 vulnerable and 4 correct merchant implementations | Final post-freeze evaluation |

The training and regression sets overlap.
The transfer set was inspected during development, so it is not final held-out evidence.

The final split uses complete merchant implementations.
No held-out program, schedule, or result changed Scout after the feature freeze.

The features and priors froze at commit `b245a558166accacfa13039ffeff2ce0425f5a24`.
The held-out merchant set froze at commit `cd1a75122ecbf38601e2e37fa220719988f11690`.

## Frozen held-out ablation

Every row uses the same eight merchants, candidate schedules, and budget of 50.
All rows report zero false findings and a 100% replay rate.

| Ranker | Trained | Fixed priors | Success@3 | Success@10 | Median rank | MRR |
|---|---:|---:|---:|---:|---:|---:|
| Trained with fixed priors | Yes | Yes | 50% | 75% | 2.5 | 0.577 |
| Fixed priors only | No | Yes | 75% | 100% | 1.0 | 0.800 |
| Trained without priors | Yes | No | 50% | 75% | 2.5 | 0.577 |
| Zero weights | No | No | 25% | 50% | 23.5 | 0.152 |

The batch-trained ranker improves substantially over zero weights.
However, the fixed-prior baseline performs better on this small held-out set.

This result does not satisfy the final AI ablation success criterion.
We must not tune Scout against this held-out result.
A future evaluation needs a replacement held-out set after any model change.

Closed-loop Scout is a separate method.
It uses deterministic runtime observations to update schedule weights.

| Starting ranker | Success@3 | Success@10 | Median rank | MRR |
|---|---:|---:|---:|---:|
| Trained with fixed priors | 100% | 100% | 1.5 | 0.750 |
| Fixed priors only | 100% | 100% | 1.0 | 0.875 |
| Trained without priors | 100% | 100% | 1.5 | 0.750 |
| Zero weights | 75% | 100% | 2.0 | 0.438 |

Closed-loop feedback improves the weak zero-weight start.
However, fixed priors alone still outperform the trained starts.
Therefore, closed-loop Scout also does not pass the learned-versus-prior ablation criterion.

## Local inference measurement

The measured host used an Intel Core Ultra 9 185H with 22 logical CPUs.
It ran Linux amd64 with Go 1.25.12.

The report scored 359,100 schedules in 67,510,629 nanoseconds.
This result is approximately 188 nanoseconds per schedule.

Run the same measurement:

```bash
go run ./testkit/lab performance 50 7 100
```

Timing varies with hardware and system load.
The ranker does not use the NVIDIA GPU because 24 scalar weights do not justify GPU transfer costs.

## Evidence limits

The held-out set contains only eight merchant implementations.
The programs are repository-authored and do not represent production traffic.
The current result does not prove production accuracy or broad model generalization.
