# Paystable Scout systems note

**Paystable: learned adversarial verification for payment-webhook integrations**
Chinni Samith Reddy — Razorpay AI Builder Internship 2026, Open Track
Note revision 1.0, 24 August 2026. Frozen evaluation commit lineage: model `b245a55`, held-out set `cd1a751`.

---

## 1. Motivation and threat model

### 1.1 The failure class

Merchant webhook handlers are routinely written as if every gateway event arrives exactly once, in business order, with an immediately knowable outcome. Gateway documentation says otherwise: duplicate delivery, reordering, delay, and stale events are expected operating conditions for Razorpay-class webhooks. The damaging failures are rarely HTTP-level errors. They are legal histories in which individually reasonable actions compose into an irreversible monetary effect:

- a captured payment fulfilled twice because the process crashed between the external effect and the durable commit;
- an already-captured terminal state regressed to `failed` by a stale event;
- fulfillment authorized on a tampered body, or for the wrong order, amount, or currency.

Conventional testing misses these histories. Unit tests cover named examples; integration tests cover the happy path plus a few expected errors; random fault injection spends most of its budget on low-yield schedules. A merchant needs four separate questions answered: which fault histories are *legal* to test, which should be tested *first* under a limited budget, whether an observed history actually *violates* a stated property, and what the *smallest* reproducing history is.

### 1.2 Product thesis

> In payment systems, AI should spend the test budget intelligently; deterministic software should decide whether a correctness property was violated.

Scout, Paystable's verification laboratory, gives the learning component exactly one job: order legal fault schedules so that a real invariant violation is found as early as possible under a fixed execution budget. Everything downstream of that ordering — labeling a run failed, replaying it, minimizing it — is deterministic code. Scout never invents an invariant, waives a finding, labels a program correct from source inspection, or performs a monetary side effect.

### 1.3 Threat model

The adversary is the distributed environment itself, not a human attacker: the environment may deliver any event any number of times in any order, crash the merchant process at named checkpoints, drop or delay responses after an effect may have occurred, and replay stale events. Schedules are drawn from a grammar whose preconditions make every generated history *legal* — the laboratory never asks the merchant to do something impossible, so every observed violation is attributable to merchant logic, not to an illegal test input.

Secondary threats addressed by construction:

- **Oracle threat:** a probabilistic ranker must not become the correctness authority. Labels come only from versioned executable invariants (Section 2).
- **Benchmark leakage:** training must never see held-out implementations. Splits are by complete implementation, frozen at published commits (Section 5).
- **Overstatement threat:** "proof", "exactly once", "model", and "held out" carry bounded definitions defined in this note; the evidence boundary is part of the product, not fine print.

Scope boundary: the runtime stabilisation service underneath the lab (durable ingestion, reconciliation, state machine, transactional outbox, signed callbacks) predates the buildathon and exists because a real payment workflow failed. This note covers the verification laboratory; the service is documented in the repository README and `docs/schema.md`.

## 2. Finite-trace semantics and invariant assumptions

### 2.1 Histories

Scout checks finite, bounded execution histories, not unbounded systems. An observed trace is

```text
τ = ⟨a₁, a₂, …, aₙ⟩
```

over a small observable alphabet: `Receive(event)`, `Verify(event, result)`, `ObserveGateway(payment, state)`, `AcceptEvent(event)`, `CommitState(order, state)`, `Fulfill(order, idempotency_key)`, `EmitCallback(order, logical_callback_id)`, `Crash(process)`, `Restart(process)`, `Fault(kind)`. For predicate `P`, `countτ(P)` counts matching actions; `Before(τ, i, P)` asserts a match before position `i`; `Eligible(o,t)` means verified authoritative evidence shows order `o` ready for fulfillment at position `t`; `Healthy(τ,t,t+H)` means the environment stays available across a declared recovery horizon `H`.

### 2.2 Assumptions

The properties are meaningful only relative to explicit assumptions:

- **A1 isolated runs** — each schedule starts from declared clean or snapshotted state;
- **A2 observable effects** — the logical fulfillment/callback boundary is observable;
- **A3 stable identity** — payment and order identity survive retries and re-observation;
- **A4 authoritative capture evidence** — eligibility requires verified webhook or server-side gateway evidence plus identity and value checks;
- **A5 bounded liveness** — eventual behaviour is judged only within horizon `H` after the environment becomes healthy;
- **A6 idempotent effect boundary** — "exactly once" means one logical effect at the merchant-controlled idempotency boundary, never exactly-once packet delivery.

### 2.3 Executable invariants

Five invariants are implemented as executable checkers over recorded traces (`internal/verification/invariants.go`, contract report via `lab invariants`). Each ships with its statement, checker, assumptions, one passing fixture, one failing fixture, and a stable identifier used by UI and evidence artifacts.

| ID | Statement | Class |
|---|---|---|
| INV-1 | Every `Fulfill` is preceded by verified capture evidence with matching payment/order identity, amount, currency | safety |
| INV-2 | `countτ(Fulfill(o,*)) ≤ 1` for every order | safety |
| INV-3 | `Eligible(o,t) ∧ Healthy(τ,t,t+H) ⇒ ∃j∈[t,t+H]: τ[j]=Fulfill(o,*)` | bounded liveness |
| INV-4 | Consecutive committed states of a payment follow allowed transitions; a captured terminal state is never followed by a committed failure | safety |
| INV-5 | Each event accepted at most once; each logical callback emitted at most once | safety |

INV-2 + INV-3 jointly express *exactly one* logical fulfillment within the declared boundary: INV-3 forbids zero, INV-2 forbids two. Supplemental checkers extend coverage to retry bounds and per-field mismatches. Any single counterexample fails a run; a passing run proves nothing beyond the executed schedules.

## 3. Architecture and fault taxonomy

### 3.1 Two cooperating paths

Gateway-specific logic ends at the adapter/signature boundary. Razorpay (HMAC-SHA256 over the raw body) and PayU (SHA-512 reverse hash) adapters both translate into a normalized payment signal consumed by identical machinery:

```text
gateway webhook/status API ──> adapter ──> normalized signal
                                             │
             ┌───────────────────────────────┴──────────────┐
             v                                              v
    runtime stabilisation path                    Scout verification path
    verify raw signature, durable inbox,          enumerate legal schedules,
    reconcile by polling, validate                rank with local model, execute,
    identity/value, monotonic state               observe full trace, check invariants,
    machine, outbox, signed callback              replay, reduce, render evidence
```

Gateway independence is demonstrated, not asserted: three equivalent normalized schedules run through both adapters producing identical actions, results, and replays without modifying Scout or the invariant engine (`lab cross-adapter`).

### 3.2 Execution pipeline

The deterministic pipeline is:

```text
legal candidates → Scout ranking → execution → invariant checker
                                       │
                                       └→ replay → delta-debug reduction → evidence
```

Execution is in-process and fully deterministic against reference merchants, and separately containerised (`testkit/labmerchant`, `docker-compose.lab.yml`) where crashes, restarts, and PostgreSQL persistence are real. Every schedule is serializable and seeded; wall-clock randomness is captured as explicit inputs so reduction can reproduce results. Replay compares full traces byte-for-byte (`reflect.DeepEqual`); a finding is displayed as verified only after replay succeeds.

The reducer applies classic delta debugging to scheduled actions: remove a candidate action, restore initial state, replay; keep the removal only if the same invariant violation still reproduces. Output is **1-minimal** — removing any single remaining scheduled action dissolves that specific failure. This is not claimed to be a globally shortest counterexample. Evidence records scheduled-action counts and observed-step counts as separate numbers: a reduced counterexample may contain 3 scheduled actions producing 6 observed steps.

### 3.3 Fault taxonomy

Eight families compose along delivery/time, process/storage, network/ambiguity, and trust/identity/state-integrity axes: duplicate delivery, reorder, delayed/replayed events, crash-before-acknowledge, crash-after-effect-before-commit, database conflict/deadlock, retry exhaustion, connection reset, timeout, lost response after effect, uncertain poll, signature tampering, identity mismatch, amount/currency mismatch, terminal regression, replay outside the authenticity window. Each family maps to primary invariants: duplicates → INV-2/INV-5; reorder/stale → INV-4; delay/exhaustion → INV-3; crash/conflict → INV-2/3/5; ambiguity → INV-2/INV-5; tampering/mismatch → INV-1.

## 4. Ranking model and reproducible training method

### 4.1 Why learning, and why this small

Legal schedule space grows combinatorially with composed faults; exhaustive execution is impractical under CI budgets, and random search wastes runs on redundant schedules. The task has structured inputs, an objective label (invariant outcome), tight latency needs, privacy-sensitive code, and a deterministic protocol — conditions that favour a tiny local model over a hosted LLM. Scout is a linear pairwise ranking model: 24 features, 24 weights, 1,007 bytes on disk, local deterministic CPU inference measured at ≈188 ns/schedule (359,100 schedules in 67.5 ms on an Intel Core Ultra 9 185H, Go 1.25.12). No merchant code, payment data, or program identity leaves the machine.

Features are schedule-only counts and order relations (action/deliver/fulfill/restart counts, captured/failed patterns, crash and post-restart replay patterns, uncertain-response patterns, trust-violation indicators, parallel-delivery count, amount/order/currency-mismatch counts). Eight risk features start with fixed prior weights; all other weights start at zero.

### 4.2 Training

Training uses the 25-program regression corpus's vulnerable programs. The invariant checker — never source inspection — labels each generated schedule positive or negative for the program's declared invariant. Training minimises a pairwise hinge objective (margin 1) over all positive-negative pairs within each vulnerable program: 65,114 pairs, 40 epochs, learning rate 0.01, fixed iteration order, no stochastic operation, therefore no seed. Correct controls create no pairs; they exist to measure false findings. Artifact format, weights, sizes, splits, and provenance are emitted by `lab model-evidence` and byte-compared by the release gate against `docs/evidence/model-evidence.json`.

If the trained weights had been hand-authored rather than fitted, the project would be labelled a deterministic ranking heuristic, per the PRD's honesty rule. The artifact is genuinely trained; Section 5 reports the uncomfortable ablation result honestly.

### 4.3 Closed-loop variant

Closed-loop Scout updates schedule weights during search from deterministic runtime observations (no labels beyond invariant outcomes). It is reported as a separate method throughout.

## 5. Evaluation protocol, baselines, and held-out results

### 5.1 Protocol

Four dataset partitions are never merged into one headline number: training (14 vulnerable synthetic programs), regression laboratory (the same corpus plus 11 correct controls), known-family transfer (24 repository-authored implementations, inspected during development, therefore not final evidence), and the frozen held-out benchmark (8 complete merchant implementations: 4 vulnerable, 4 correct). Splits are by complete implementation. The model froze at commit `b245a55`; the held-out set froze at commit `cd1a751`; no held-out code, schedule, or result changed Scout afterwards.

All methods receive identical candidate sets, reset behaviour, timeouts, and budget 50. Baselines: bounded canonical search; uniform random search over seeds 7–106 (100 seeds, Wilson 95% intervals); coverage-guided search; batch-trained Scout; closed-loop Scout; plus ablation arms (priors-only, trained-without-priors, zero weights). Metrics: Success@K (K ∈ {1,3,5,10,25,50}), median rank with misses assigned rank 51, MRR with misses scored zero, false findings on controls, deterministic replay rate, reduction ratio, time-to-first-violation, inference latency, artifact size. Reproduce with `go run ./testkit/lab heldout 50 7`.

### 5.2 Regression snapshot (release signal, not a generalisation claim)

| Method | Found within 10 | Median rank | False findings | Replay |
|---|---:|---:|---:|---:|
| Bounded | 7/14 | 15 | 0 | 100% |
| Random | 12/14 | 2 | 0 | 100% |
| Coverage-guided | 13/14 | 9 | 0 | 100% |
| Scout | 14/14 | 1 | 0 | 100% |

Regression saturation is treated as a release signal, not as evidence of superiority; tuning stopped there deliberately.

### 5.3 Frozen held-out results

Over the 8 merchants (misses at rank 51): bounded S@10 50%, median 23.5, MRR 0.152; random (100 seeds) S@10 82% [Wilson 95%: 77.9–85.5%], median 3.0, MRR 0.509; coverage S@10 50%, median 12.5; Scout S@10 75%, median 2.5, MRR 0.577; closed-loop Scout S@3/S@10 100%, median 1.5. Zero false findings on all four controls; deterministic replay rate 100%. Standard Scout beats random on median rank and MRR but **not** on Success@10; this is stated rather than averaged away.

Ablation arms on the same four vulnerable merchants (all find 4/4 within budget 50):

| Ranker arm | S@3 | S@10 | Median | MRR |
|---|---:|---:|---:|---:|
| Trained with fixed priors | 50% | 75% | 2.5 | 0.577 |
| Fixed priors only | 75% | 100% | 1.0 | 0.800 |
| Trained without priors | 50% | 75% | 2.5 | 0.577 |
| Zero weights | 25% | 50% | 23.5 | 0.152 |

**Per-program decomposition.** First-finding ranks per vulnerable merchant:

| Merchant | Priors only | Trained (w/ priors) | Trained (no priors) | Zero weights |
|---|---:|---:|---:|---:|
| heldout-dedup-unsafe | 5 | 4 | 4 | miss |
| heldout-state-unsafe | 1 | 1 | 1 | 4 |
| heldout-trust-unsafe | 1 | 1 | 1 | 3 |
| heldout-retry-unsafe | 1 | 17 | 17 | 43 |

The honest reading: batch training clearly improves on zero weights, but at four vulnerable programs there is **no measurable difference between batch training and fixed priors alone**. The two rankers tie on two merchants and each leads on exactly one; the entire aggregate MRR gap comes from a single merchant. With n = 4, even a clean sweep would not reach conventional significance (best possible two-sided exact sign-test p-value ≈ 0.125), and here wins are split 1–1 with 2 ties. The claim we stand behind is "no measurable difference at this sample size, fixed priors nominally ahead" — not "training hurts" and not "priors beat the model". We do not tune against this set; any model change requires a fresh replacement held-out set before further claims.

One robust observation survives decomposition: closed-loop feedback drives all four starting points (including zero weights) to near-identical per-program ranks ({2,1,1,2} vs {2,1,1,1}), so runtime observations dominate initial weights once the loop is active. Read as a property of the fault model rather than of any single ranker: discovery on these merchants is robust to how the ranker is initialised — once feedback runs, the shape of the legal schedule space surfaces the decisive histories from every starting bias, trained or not. The same n = 4 caveat applies to this sentence as to every other claim in this section.

**Why the one gap exists (post-hoc single-merchant diagnosis).** The retry merchant's rank-1-vs-17 difference has a checked structural explanation rather than pure variance, verified by ranking its frozen 780-candidate set under both arms and locating every failing schedule. The trained weights reward fulfillment-and-uncertainty volume — `uncertain_response_count` carries the largest learned non-prior weight — because attempt-heavy histories correlated with violations in the synthetic training corpus. On this merchant that preference is cheap to imitate: fulfill-only schedules are legal but vacuous (fulfillment before any capture produces no logical effect and cannot violate INV-2), yet they maximise exactly those volume features, so they occupy sixteen of the seventeen leading positions while producing nothing. The genuine failure shares its score with a 32-candidate tie group of which only half fail, and stable ordering parks it at rank 17. The fixed priors spread equal weight over eight sparse risk markers instead, so a history combining a stale failure-after-capture with a post-ambiguity retry triggers two prior flags where bare fulfillment storms trigger fewer, and it is found immediately. The deduplication difference is *not* analogous — both arms place their first failure within one adjacent position there, the priors' rank 5 being placement luck inside a six-way score tie — so the aggregate comparison reduces to this one mechanism. We report the mechanism because it separates two different stories: not "learned features failed here at random", but "the trained model chases volume correlations that this fault class does not require". It remains a single-merchant observation and is offered as explanation, not as a demonstrated general pattern.

This result does not satisfy the PRD's AI-ablation success criterion, and we publish it anyway, per the pre-registered commitment in `docs/verification-scope.md`: "The release will publish a negative result if Scout does not pass this gate."

## 6. Related work

**Jepsen.** Jepsen combines fault-producing workloads with history checking to evaluate distributed-system consistency. Paystable shares the discipline — execute faults, then judge an observed history — but checks payment-domain safety and bounded-liveness properties rather than claiming general linearizability analysis.

**Lineage-driven fault injection / Molly.** LDFI reasons backward from successful outcomes using data lineage and satisfiability to find fault combinations that violate properties. Scout shares the objective — high-value fault selection under combinatorial growth — but constructs no lineage and performs no such reasoning; it is learned prioritisation over a generated candidate set, and we do not describe it as LDFI.

**Delta debugging.** Zeller and Hildebrandt's minimise/isolate framework is applied directly in the reducer. Our output guarantee matches the literature: 1-minimality, explicitly not global minimality.

**Learned test-case prioritisation.** Scout belongs most directly here: ordering tests under a finite budget using historical features, evaluated through discovery rank, Success@K, held-out behaviour, ablations, and leakage-resistant splits (per the systematic review of Pan et al.). The negative ablation above is a data point within this lineage: on very small program sets, simple fixed heuristics remain competitive with batch-trained rankers.

No claim of being first-of-kind is made or needed; the contribution is the integration around concrete payment-webhook correctness boundaries.

## 7. Threats to validity and limitations

**External validity.** All programs are synthetic or repository-authored; production traffic distribution is unrepresented. Held-out n = 4 vulnerable merchants cannot establish generalisation — the per-program analysis in §5.3 exists precisely because aggregate ranks at this n are unstable under single-program flips. The genuine Razorpay Test Mode webhook proves the signed integration path, not traffic diversity.

**Construct validity.** "Found" means a versioned invariant fired within budget under assumptions A1–A6; different assumption choices could change labels. Bounded liveness depends on the declared horizon and health predicate. "Exactly once" is defined only at the merchant idempotency boundary (A6).

**Statistical validity.** Random-search uncertainty is quantified over 100 seeds; method-vs-method comparisons on the held-out set are not, and cannot meaningfully be, significance-tested at n = 4. Medians average middle ranks; misses assign rank 51 — both rules are frozen in `docs/verification-scope.md` before the held-out run.

**Model validity.** The trained ranker's advantage over fixed priors is undemonstrated on current data (§5.3); the model earns its keep today as a fast, private, auditable baseline-plus-training pipeline, not as a proven improvement. Known blind spot: the replay-window challenge exposed an unmodeled time feature; Scout v3 adds one normalised replay-delay feature trained on nine invariant-labeled pairs, validated only on unseen delay values within the lab.

**Demonstration validity.** Timings are hardware-dependent; determinism claims hold for pinned revisions, seeds, and environments, with nondeterminism required to be reported explicitly when detected. No production-scale throughput, multi-tenant operation, or certification claim is made anywhere in this note.

**Boundary statement (part of the product).**

> Current evidence comes from synthetic and repository-authored programs, narrow pinned integration probes, and Razorpay Test Mode. It demonstrates deterministic discovery and reproduction within these boundaries. It does not establish accuracy on unseen production systems, production-scale throughput, or superiority over every payment platform.
