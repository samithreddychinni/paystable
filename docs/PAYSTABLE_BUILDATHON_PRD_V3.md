# Product Requirements Document

## Paystable — Learned Adversarial Verification for Payment Integrations

**Program:** Razorpay AI Builder Internship 2026  
**Track:** Open Track  
**Author:** Chinni Samith Reddy  
**PRD version:** 3.0  
**Date:** 23 August 2026  
**Final submission target:** No later than 5 September 2026  
**Repository:** [IDEA-Amrita/paystable](https://github.com/IDEA-Amrita/paystable)

> **One line:** Paystable learns which legal payment-failure histories are most likely to expose a merchant bug, executes them against the real integration, and uses deterministic invariants to produce a replayable, 1-minimal proof of the failure.

---

## 1. Executive summary

Payment webhooks are delivered through a distributed system. Duplicate delivery, reordering, delay, process crashes, ambiguous network failures, and stale events are expected operating conditions—not exceptional edge cases. Yet many merchant integrations are tested as if every event arrives once, in order, and with an immediately knowable outcome.

This mismatch can cause irreversible business effects: an order fulfilled twice, a captured payment treated as failed, a callback emitted before authenticity is established, or a payout retried after the first attempt actually succeeded.

Paystable is a working Go and PostgreSQL payment-correctness service created from a real webhook failure, not invented for the buildathon. It already provides secure webhook ingestion, durable evidence, controlled gateway reconciliation, state and identity validation, idempotent callbacks, retries, metrics, and operational visibility for Razorpay and PayU.

The buildathon contribution is **Scout**, Paystable's adversarial verification laboratory. Scout does not use AI to decide whether money is correct. It uses a small local ranking model to decide **which valid fault schedule should be tested first when execution time is limited**. The selected schedules are executed deterministically. Explicit invariants determine pass or failure. Replays confirm reproducibility, and a delta-debugging reducer turns a long failing run into a 1-minimal counterexample.

The current regression laboratory contains 25 programs: 14 intentionally vulnerable implementations and 11 correct controls. In the current snapshot, Scout finds all 14 known vulnerable programs within ten schedules, with a median discovery rank of one, while all 11 controls remain clean. This is promising controlled evidence, not a claim of universal production accuracy. The final evaluation must distinguish training, regression, and held-out repository programs and report uncertainty honestly.

The product thesis is simple:

> In payment systems, AI should spend the test budget intelligently; deterministic software should decide whether a correctness property was violated.

---

## 2. Problem

### 2.1 The incorrect assumption

A common merchant flow is:

1. Receive a payment webhook.
2. Read its event type.
3. Update an order.
4. Perform an irreversible effect such as fulfillment.
5. Return HTTP 200.

The implementation often silently assumes:

- delivery occurs exactly once;
- events arrive in business order;
- a request timeout means no effect occurred;
- event state is always newer than stored state;
- a process cannot crash between the external effect and the database commit;
- deduplicating a webhook is equivalent to deduplicating the business effect;
- authenticity, payment identity, amount, currency, and order identity were checked before action.

Those assumptions are unsafe under normal distributed-systems behavior. Razorpay explicitly documents duplicate and out-of-order webhook delivery, recommends idempotent handling, and requires verification against the raw request body. The difficult failure is rarely “the webhook endpoint returned 500.” It is the valid but adversarial history in which individually reasonable actions compose into an incorrect monetary outcome.

### 2.2 Why existing testing misses it

Unit tests generally cover named examples. Conventional integration tests exercise the happy path and a small number of expected errors. Random fault injection can generate many schedules but waste most of its budget on low-value combinations. Logs may expose symptoms, but they do not state which correctness property failed or which smallest set of inputs caused it.

A merchant therefore needs answers to four different questions:

1. **Generate:** Which failures and interleavings are legal enough to test?
2. **Prioritise:** Which schedule should run first under a limited budget?
3. **Judge:** Did the resulting history violate an explicit property?
4. **Explain:** What is the smallest deterministic history that still reproduces the violation?

Paystable treats these as separate mechanisms so that a probabilistic ranking decision can never become the correctness oracle.

---

## 3. Product thesis and positioning

Paystable is not:

- another webhook receiver;
- a chatbot for logs;
- an LLM wrapper that guesses whether a payment flow looks suspicious;
- a claim that exactly-once network delivery is possible;
- a replacement for Razorpay's payment platform;
- a production certification authority.

Paystable is:

- a payment-stabilisation layer for runtime handling;
- an adversarial verification laboratory for pre-production payment code;
- a learned test-prioritisation system whose findings are accepted only by executable invariants;
- a generator of reproducible evidence rather than confidence scores;
- a gateway-independent method demonstrated primarily through Razorpay and secondarily through PayU.

The novelty is not any single primitive. Fault injection, temporal properties, learned test prioritisation, replay, and delta debugging all have established research lineages. The contribution is their integration around the concrete correctness boundaries of payment-webhook consumers, with a real stabilisation service and real Razorpay Test Mode evidence underneath the laboratory.

---

## 4. Users and jobs to be done

### Primary user

A backend engineer or product team that owns a merchant payment integration but does not have a dedicated distributed-systems verification team.

### Secondary users

- payment infrastructure engineers reviewing merchant integration patterns;
- QA and reliability engineers building release gates;
- security engineers checking authenticity and replay boundaries;
- technical reviewers evaluating the correctness of agentic financial actions.

### Jobs to be done

- “Before I ship a payment handler, show me whether duplicates, crashes, stale events, or ambiguous failures can create an incorrect business effect.”
- “If a failure exists, give me a deterministic reproduction small enough to understand and turn into a regression test.”
- “Spend my limited execution budget on the schedules most likely to reveal a bug.”
- “Tell me exactly what was observed, what invariant failed, and what the evidence does not prove.”

---

## 5. Current product baseline

The following capabilities are implemented and form the base on which Scout operates.

### 5.1 Runtime payment stabilisation

- Go service backed by PostgreSQL
- Razorpay and PayU adapters
- raw-body webhook signature verification
- durable webhook storage and deduplication
- controlled gateway status polling
- payment, order, amount, and currency validation
- strict terminal-state and indeterminate-state handling
- signed, idempotent merchant callbacks
- transactional outbox and bounded retries
- append-only audit evidence and operational metrics
- PostgreSQL worker coordination and Docker fault tests
- an operational dashboard

### 5.2 Scout v3 verification laboratory

- deterministic generation and replay of legal fault schedules
- local fault-schedule ranking
- executable invariants
- deterministic run seeds and bounded execution budgets
- 1-minimal trace reduction
- proof-oriented judge UI
- comparison against bounded, random, and coverage-guided search
- coverage of delivery, time, process, storage, network, security, identity, and state-transition failures

### 5.3 Existing evidence

- 25-program regression laboratory: 14 vulnerable and 11 correct controls
- all 14 known regression failures found within ten schedules in the current Scout snapshot
- median Scout discovery rank of one in that snapshot
- all 11 correct controls clean in that snapshot
- a separate repository-authored benchmark of 24 implementations: 12 vulnerable and 12 correct
- genuine Razorpay Test Mode webhook evidence
- pinned third-party integration probes at narrow, declared boundaries
- PostgreSQL and Docker fault testing
- automated release gate and public documentation

The held-out benchmark's final Scout-versus-baseline results must be reported separately from regression results. Dataset size alone is not performance evidence.

---

## 6. System architecture

Paystable has two cooperating products: the runtime correctness service and the pre-production verification laboratory.

```text
Razorpay webhook/status API ─┐
                            ├─> Gateway adapters
PayU webhook/status API ────┘         │
                                      v
                         Normalized payment signal
                                      │
                 ┌────────────────────┴────────────────────┐
                 │                                         │
                 v                                         v
      Runtime stabilisation path                 Scout verification path
      - verify raw signature                     - enumerate legal schedules
      - durable inbox                            - rank with local model
      - reconcile by polling                     - execute in sandbox/lab
      - validate identity/value                  - observe complete trace
      - monotonic state machine                  - check invariants
      - transactional outbox                     - replay and reduce
      - signed callback                          - render evidence
```

### 6.1 Gateway independence

Gateway-specific code ends at the adapter and signature boundary. Both Razorpay and PayU inputs are translated into a normalized payment signal consumed by the same state, scheduling, invariant, replay, and reduction machinery.

The buildathon centres Razorpay because that is the program context and the primary live evidence. PayU is not presented as a breadth feature; it is an architectural control demonstrating that the core method does not depend on Razorpay event names.

The final cross-adapter experiment must execute equivalent normalized duplicate, reordering, and terminal-regression schedules through both adapters without modifying Scout or the invariant engine.

---

## 7. Formal finite-trace model

Paystable checks finite, bounded execution histories. It does not claim to prove an unbounded distributed system correct.

Let an observed trace be:

```text
τ = ⟨a₁, a₂, …, aₙ⟩
```

where each action belongs to a small observable alphabet:

```text
Receive(event)
Verify(event, result)
ObserveGateway(payment, state)
AcceptEvent(event)
CommitState(order, state)
Fulfill(order, idempotency_key)
EmitCallback(order, logical_callback_id)
Crash(process)
Restart(process)
Fault(kind)
```

For a predicate `P`, `countτ(P)` denotes the number of matching actions in `τ`. `Before(τ, i, P)` means a matching action appears before position `i`. `Eligible(o,t)` means the system has verified authoritative evidence that order `o` is ready for fulfillment at trace position `t`. `Healthy(τ,t,t+H)` means the environment remains available for a declared bounded recovery horizon `H`.

### 7.1 Assumptions

The properties below are meaningful only with explicit assumptions:

- **A1 — isolated runs:** each schedule starts from a declared clean or snapshotted state;
- **A2 — observable effects:** the lab can observe the logical fulfillment/callback boundary being checked;
- **A3 — stable identity:** payment and order identities remain stable across retries and gateway observations;
- **A4 — authoritative capture evidence:** fulfillment eligibility requires verified webhook or server-side gateway evidence plus identity and value checks;
- **A5 — bounded liveness:** eventual behavior is evaluated only within a declared horizon `H` after the environment becomes healthy;
- **A6 — idempotent effect boundary:** “exactly once” means one logical effect at the merchant-controlled idempotency boundary, not exactly-once packet delivery.

### 7.2 Executable invariants

#### INV-1 — No unauthorized fulfillment

Every fulfillment must be preceded by verified capture evidence and matching payment identity, order identity, amount, and currency.

```text
∀i. τ[i] = Fulfill(o,k)
  ⇒ VerifiedCaptureBefore(τ,i,payment(o))
   ∧ IdentityMatchedBefore(τ,i,o)
   ∧ ValueMatchedBefore(τ,i,o)
```

This is a safety property: one counterexample is sufficient to fail the run.

#### INV-2 — At-most-once logical fulfillment

```text
∀o. countτ(Fulfill(o,*)) ≤ 1
```

This catches duplicate effects caused by duplicate delivery, crash-before-commit, network ambiguity, or incorrect idempotency placement.

#### INV-3 — Bounded fulfillment liveness

```text
Eligible(o,t) ∧ Healthy(τ,t,t+H)
  ⇒ ∃j ∈ [t,t+H] : τ[j] = Fulfill(o,*)
```

This is bounded liveness, not an unqualified promise that fulfillment eventually occurs under permanent outage. The run must record `H` and the health predicate used.

Together, INV-2 and INV-3 express exactly one logical fulfillment within the declared verification boundary.

#### INV-4 — Monotonic legal payment state

For successive committed states of the same payment:

```text
∀sᵢ,sⱼ. ConsecutiveCommittedStates(p,sᵢ,sⱼ)
  ⇒ AllowedTransition(sᵢ,sⱼ)
```

In particular, a stale failure or authorization event must not regress an already captured terminal state.

```text
Committed(p, captured) → Gτ ¬Committed(p, failed)
```

Here `Gτ` means “for the remainder of this finite trace.”

#### INV-5 — Idempotent event and callback acceptance

```text
∀e. countτ(AcceptEvent(e.id)) ≤ 1
∀c. countτ(EmitCallback(*,c.id)) ≤ 1
```

Transport retries may occur; repeated logical acceptance or callback effects may not.

### 7.3 Property-to-code requirement

Every invariant shipped in the demo must include:

- the finite-trace statement;
- the executable checker that implements it;
- its assumptions and observation boundary;
- one passing trace fixture;
- one failing trace fixture;
- a stable invariant identifier used by the UI and evidence artifact.

The formal notation is a precise contract for the checker, not a claim that Paystable is a complete model checker.

---

## 8. Fault model and taxonomy

The laboratory's fault space is structured rather than presented as a flat list.

### 8.1 Delivery and time faults

- duplicate webhook delivery
- event reordering
- delayed delivery
- replay-window expiry
- clock skew
- stale event replay

### 8.2 Process and storage faults

- crash before acknowledgement
- crash after external effect but before durable commit
- database conflicts
- deadlocks
- retry exhaustion
- restart and recovery

### 8.3 Network and effect-ambiguity faults

- connection reset
- request timeout
- response lost after an effect may have occurred
- uncertain polling result

### 8.4 Trust, identity, and state-integrity faults

- signature tampering
- payment/order identity mismatch
- amount or currency mismatch
- terminal-state regression
- replay outside the accepted authenticity window

### 8.5 Fault-to-invariant coverage

| Fault family | Primary properties exercised | Typical failure evidence |
|---|---|---|
| Duplicate/replay | INV-2, INV-5 | repeated fulfillment or logical callback |
| Reorder/stale state | INV-4 | terminal regression or false final state |
| Delay/retry exhaustion | INV-3 | eligible order never fulfilled within `H` |
| Crash/storage conflict | INV-2, INV-3, INV-5 | duplicated effect or lost progress |
| Reset/timeout ambiguity | INV-2, INV-5 | blind retry after an effect occurred |
| Signature tampering | INV-1 | effect without valid authenticity evidence |
| Identity/value mismatch | INV-1 | effect for the wrong order, amount, or currency |

Each schedule is serializable, seeded, and replayable. Wall-clock randomness must be captured as explicit inputs so reduction can reproduce the same result.

---

## 9. Scout: the AI component

### 9.1 Why learning is useful

The legal schedule space grows combinatorially as faults, event orders, crash points, and retries are composed. Exhaustive execution is impractical under a build, CI, or demo budget. Random search often spends runs on redundant or low-yield schedules. Scout's task is therefore narrow and measurable:

> Given a merchant program, its current execution context, a set of legal candidate schedules, and a fixed budget, rank the schedules so that a real invariant violation is discovered as early as possible.

This is learned test-case prioritisation, not generative diagnosis.

### 9.2 Model boundary

Scout outputs an ordering or score over legal schedules. It does not:

- invent an invariant;
- waive a failed invariant;
- label a run correct from source code alone;
- perform a monetary side effect;
- change merchant code;
- require an external LLM provider;
- send merchant code or payment data to a third party.

The deterministic pipeline remains:

```text
legal candidates → Scout ranking → execution → invariant checker
                                      │
                                      └→ replay → reducer → evidence
```

### 9.3 Small local model requirement

The build should prefer a small, locally executed ranking model over a hosted general-purpose LLM because the task has structured inputs, an objective label, tight latency requirements, privacy-sensitive code, and a deterministic evaluation protocol.

For Scout to be described as a **trained model**, the repository must include or precisely document:

- the schedule/program features;
- the prediction target or ranking loss;
- training, validation, regression, and held-out splits;
- training seed and reproducible command;
- model artifact format, parameter or node count, and on-disk size;
- inference latency and hardware;
- ablation against an untrained or hand-authored ranking function;
- safeguards against benchmark leakage.

If the current ranking weights are hand-authored rather than learned by a reproducible training process, the UI and submission must call Scout a **deterministic ranking heuristic**, not a trained model. In that case it remains a strong baseline but cannot carry the AI claim by itself.

### 9.4 User-facing terminology

The UI must replace ambiguous labels such as `Scout model: 1,007 B` with explicit measurements, for example:

```text
Scout ranker
1,007 bytes on disk · N parameters/nodes · local deterministic inference
```

Only values emitted from the actual artifact are permitted. No parameter count or model family should be inferred from file size.

### 9.5 Optional explanation layer

A natural-language explanation may be generated from the already verified, minimized trace, but it is optional and subordinate. It must be clearly presented as an explanation of evidence, never as the source of the finding. Automated patch generation is outside the core submission because it adds demo risk without strengthening the central scientific claim.

---

## 10. Execution, checking, replay, and reduction

### 10.1 Rank

Scout receives the same legal candidate schedules available to every comparison method. It ranks them without access to the test program's vulnerability label or expected failing trace.

### 10.2 Execute

The laboratory runs schedules in an isolated, resettable environment against the actual merchant program. It records both scheduled input actions and resulting execution steps.

The UI must distinguish these quantities. A reduced counterexample may correctly contain “3 scheduled actions” while producing “6 observed execution steps.” Presenting both avoids the appearance of an internal inconsistency.

### 10.3 Prove

“Prove” means the following bounded claim only:

- a specified executable invariant failed;
- under a specified environment and observation boundary;
- for a fully recorded finite input schedule;
- the same failure replayed deterministically;
- the reducer could not remove another scheduled action while preserving that failure.

It does not mean the merchant system is universally incorrect under every environment, or that a passing run proves production correctness.

### 10.4 Reduce

The reducer applies delta debugging to the failing schedule. It repeatedly removes candidate actions, restores the initial state, replays the reduced schedule, and retains the reduction only when the same invariant failure reproduces.

The output is **1-minimal**: removing any one remaining scheduled action causes that specific failure to disappear. This is not necessarily a globally shortest possible counterexample, and the documentation must not claim otherwise.

### 10.5 Evidence artifact

Every finding must include:

- run and seed identifiers;
- program and revision identifier;
- adapter and gateway mode;
- execution budget and environment configuration;
- original and reduced scheduled action counts;
- observed execution steps;
- invariant identifier and checker version;
- replay result;
- reduction result;
- causal timeline;
- evidence boundary and known limitations.

---

## 11. Evaluation methodology

### 11.1 Evaluation questions

The evaluation must answer:

1. Does Scout find known violations earlier than non-learned search strategies under the same budget?
2. Does it avoid findings on correct controls?
3. Do failures replay?
4. How much does reduction shrink the input schedule, and how long does it take?
5. Does performance persist on repository implementations excluded from training and tuning?
6. Does the same core machinery work across Razorpay and PayU after normalization?

### 11.2 Dataset partitions

Results must never merge these groups into one headline number:

- **Training set:** programs or schedules used to fit Scout;
- **Validation set:** used for model and feature selection;
- **Regression laboratory:** known vulnerable and correct programs used to prevent regressions;
- **Held-out repository benchmark:** entire merchant implementations excluded from model fitting, feature tuning, thresholds, and manual schedule tuning.

Splits should occur by complete implementation or bug family where possible, not by individual schedule. Schedule-level random splits can leak the structure of the same program into both training and evaluation.

### 11.3 Baselines

All methods receive the identical candidate schedule set, reset behavior, timeout, and execution budget:

- bounded canonical search;
- uniform random search;
- coverage-guided search;
- Scout;
- optional Scout ablations, including untrained weights and feature-group removal.

### 11.4 Metrics

- Success@1, Success@3, Success@5, and Success@10
- median rank with misses assigned `budget + 1`
- mean reciprocal rank
- vulnerable programs found within budget
- false findings on correct controls
- deterministic replay rate
- original-to-minimized schedule ratio
- time to first violation
- time to 1-minimal trace
- ranker inference time and model artifact size

Random search must be evaluated across at least 100 fixed, published seeds and reported with uncertainty. If Scout training is stochastic, report results across multiple training seeds as well.

### 11.5 Current regression snapshot

The following values are current repository evidence, not held-out production claims:

| Method | Regression failures found within 10 | Current median rank | False findings | Replay rate |
|---|---:|---:|---:|---:|
| Bounded search | 7/14 | 15 | 0 | 100% |
| Random search | 12/14 | 2 | 0 | 100% |
| Coverage-guided | 13/14 | 9 | 0 | 100% |
| Scout | 14/14 | 1 | 0 | 100% |

Before final submission, the table must state exactly how misses affect median rank and whether the random-search row is a single run or an aggregate. The judge-facing headline should use the held-out benchmark once its protocol is frozen and results are available. Regression saturation is a release signal, not a reason to keep tuning against the same 14 failures.

### 11.6 Evidence boundary

The final UI, documentation, video, and spoken pitch must state:

> Current evidence comes from synthetic and repository-authored programs, narrow pinned integration probes, and Razorpay Test Mode. It demonstrates deterministic discovery and reproduction within these boundaries. It does not establish accuracy on unseen production systems, production-scale throughput, or superiority over every payment platform.

This statement is part of the product, not fine print.

---

## 12. Functional requirements

### FR-1 — Normalize gateway evidence

Razorpay and PayU adapters must convert verified gateway inputs into a stable internal event vocabulary without leaking gateway-specific event names into the invariant engine.

### FR-2 — Preserve authenticity evidence

The exact raw webhook body, relevant headers, verification result, gateway identity, and receipt time must be durably attributable to the accepted event.

### FR-3 — Generate legal schedules

The engine must compose supported faults while respecting declared preconditions and serialize every resulting schedule for replay.

### FR-4 — Rank within a budget

Scout and every baseline must rank the same candidate set. The execution budget and ranking inputs must be recorded.

### FR-5 — Execute in isolation

Each run must restore or create a declared initial state and capture all observable business effects needed by the invariants.

### FR-6 — Check explicit properties

Every result must be produced by a versioned executable invariant. The ranking model cannot emit a finding directly.

### FR-7 — Replay deterministically

A finding must replay from its serialized schedule and declared initial state before it is shown as verified.

### FR-8 — Produce a 1-minimal counterexample

The reducer must test removability of scheduled actions and record the reduction procedure and outcome.

### FR-9 — Render a causal evidence view

The judge UI must show:

- the product problem in one sentence;
- Scout-versus-baseline results with dataset labels;
- scheduled actions versus observed steps;
- the failed invariant;
- the replayed 1-minimal trace;
- the evidence boundary.

The completion banner should read **“Scout evaluation complete”**, not **“Verification passed,”** when the page contains a detected failure.

### FR-10 — Export a regression artifact

The minimized schedule and expected invariant failure must be exportable as a stable regression fixture or machine-readable evidence file.

---

## 13. Non-functional requirements

- **Determinism:** identical revision, seed, environment, and schedule must reproduce the same observable trace or explicitly report nondeterminism.
- **Auditability:** every metric shown in the UI must be derivable from stored run artifacts.
- **Isolation:** faults must target the laboratory environment; real monetary effects must not be possible.
- **Privacy:** Scout inference should run locally and should not require uploading merchant code or payment payloads.
- **Boundedness:** schedules, retries, polling, and liveness horizons must have explicit limits.
- **Reproducibility:** a clean machine must be able to build and run the documented demonstration from pinned dependencies.
- **Fail-closed semantics:** invalid signatures, mismatched identity/value, or uncertain evidence must not authorize fulfillment.
- **Honest terminology:** “model,” “proof,” “exactly once,” and “held out” may be used only with the boundaries defined in this PRD.

---

## 14. Judge-facing demonstration

The demonstration should follow the product's causal logic, not a feature tour.

### Beat 1 — The money bug

Show a realistic merchant program that passes normal tests. Deliver a captured payment, allow fulfillment, crash the process before durable event completion, and redeliver. The order is fulfilled twice.

### Beat 2 — Why search matters

State the problem in one sentence, then show the controlled comparison: every method receives the same candidate set and total execution budget, while the early-discovery result reports how many failures each method finds within the first ten schedules. Clearly label whether this is the regression or held-out result.

### Beat 3 — Deterministic judgment

Show INV-2 fail because the trace contains two logical fulfillment effects. Emphasize that the model proposed a test; executable code decided the failure.

### Beat 4 — Minimal evidence

Show the original history reduced to the smallest 1-minimal input schedule that still reproduces the same violation. Present the scheduled actions and the resulting execution timeline separately.

### Beat 5 — Breadth without dilution

Fast-forward through terminal regression and signature/identity protection. Then show one equivalent normalized schedule passing through Razorpay and PayU adapters without a change to Scout or the invariant engine.

### Beat 6 — Evidence boundary

End by stating the limits before the judges ask: Test Mode, repository-authored programs, no production certification claim. Then state the product value precisely: Paystable turns payment failure hypotheses into executable, replayable counterexamples before deployment.

---

## 15. Documentation deliverables

### 15.1 Product README

The README should optimise for installation, first run, architecture orientation, and evidence navigation.

### 15.2 Scout systems note

A separate four-to-six-page systems note should contain:

1. motivation and threat model;
2. finite-trace semantics and invariant assumptions;
3. architecture and fault taxonomy;
4. ranking model and reproducible training method;
5. evaluation protocol, baselines, ablations, and held-out results;
6. related work;
7. threats to validity and limitations.

This note is not marketing collateral. It should read like an engineering research artifact and make every claim traceable to code, data, or a declared assumption.

### 15.3 Submission artifacts

- public GitHub repository
- five-minute pitch video
- exact project objective and problem statement for the form
- clean-machine installation evidence
- backup demo recording and screenshots
- frozen evaluation report and machine-readable results
- final submission confirmation only after every link has been opened from an unauthenticated browser

---

## 16. Related work and intellectual grounding

Paystable should cite its lineage accurately and avoid claiming equivalence where the mechanisms differ.

### Jepsen

Jepsen combines fault-producing workloads with history checking to evaluate distributed-system consistency. Paystable shares the discipline of executing faults and judging an observed history, but it checks payment-domain safety and bounded-liveness properties rather than claiming general linearizability analysis.

### Lineage-driven fault injection and Molly

Lineage-driven fault injection reasons backward from successful outcomes using data lineage and satisfiability to find fault combinations capable of violating a property. Scout is related in objective—high-value fault selection under combinatorial growth—but should not be described as LDFI unless it actually constructs lineage and performs the corresponding reasoning.

### Delta debugging

Scout's reducer is a direct application of delta-debugging principles: systematically remove parts of a failing input and preserve only reductions that reproduce the same failure. The correct output claim is 1-minimality, not guaranteed global minimum.

### Learned test-case prioritisation

Scout belongs most directly to learned test prioritisation: use historical program, schedule, and execution features to order tests under a finite budget. Its contribution must therefore be evaluated primarily through discovery rank, Success@K, held-out behavior, ablations, and leakage-resistant splits.

### Positioning statement

> Paystable applies learned test prioritisation, deterministic fault execution, finite-trace payment invariants, and delta-debugging reduction to merchant payment-webhook correctness. Razorpay is the primary demonstration; PayU provides evidence that the core verification method operates after gateway normalization.

This is strong, accurate, and testable. No unsupported claim that the method is the first of its kind is required.

---

## 17. Risks and mitigations

| Risk | Consequence | Mitigation |
|---|---|---|
| Scout is a hand-authored heuristic rather than reproducibly trained | AI claim becomes weak or misleading | Document the current mechanism immediately; either train and freeze a small ranker or label it honestly as a heuristic baseline |
| Regression benchmark leakage | 14/14 result overstates generalization | Split by whole program/bug family; freeze a held-out repository set; publish provenance |
| Random baseline is based on too few seeds | Comparison is unstable | Run at least 100 fixed seeds and report uncertainty |
| “Proof” is interpreted as universal correctness | Credibility loss | Define bounded proof semantics in UI, note, and video |
| Exactly-once wording ignores downstream boundaries | Invariant becomes impossible or misleading | Define one logical effect at a declared idempotency boundary; separate safety from bounded liveness |
| Synthetic programs dominate evaluation | Limited external validity | Lead with held-out repository cases, cross-adapter evidence, Test Mode evidence, and explicit limits |
| Live sandbox or Docker failure | Demo cannot complete | Record a verified backup run and preserve machine-readable artifacts |
| More features dilute the central contribution | Evaluation and submission quality suffer | Freeze runtime scope; work only on model legitimacy, held-out evaluation, formal contracts, and submission evidence |

---

## 18. Scope

### Required for the final submission

- current Paystable runtime service and Razorpay integration
- PayU cross-adapter generality demonstration
- Scout schedule ranking with honest model/heuristic classification
- reproducible training and inference documentation if Scout is learned
- five executable finite-trace invariants with fixtures and assumptions
- structured fault taxonomy and coverage matrix
- replay and 1-minimal reduction
- equal-budget baseline comparison
- leakage-resistant held-out evaluation
- genuine Razorpay Test Mode evidence
- proof-oriented local judge UI
- systems note, five-minute video, and clean installation path

### Explicitly outside the core submission

- arbitrary repository understanding or automatic invariant discovery
- autonomous production code modification
- LLM patch generation as a required demo path
- production certification or universal accuracy claims
- hosted multi-tenancy, authentication, billing, or public SaaS operations
- broad gateway expansion beyond using the existing PayU adapter as a generality control
- official client SDKs
- large-scale load claims unsupported by measurement
- complete refund and manual-resolution product workflows

These are not omissions from the thesis. They do not determine whether Scout can efficiently find and prove payment-correctness failures.

---

## 19. Release and submission gates

The project is ready for final submission only when all of the following are true:

- the Scout UI names the ranking mechanism and artifact measurements precisely;
- no page says “verification passed” while displaying a violation;
- regression and held-out results are visually and verbally separated;
- the random baseline uses the frozen multi-seed protocol;
- every headline metric is reproducible from committed artifacts;
- every invariant has notation, assumptions, implementation, and fixtures;
- a minimized finding replays from a clean environment;
- the Razorpay Test Mode evidence is preserved and clearly labelled;
- the PayU cross-adapter control runs without core-engine changes;
- the systems note contains related work and threats to validity;
- the five-minute video fits within the form's limit and has a backup;
- repository and video links work without the author's account;
- the final form text makes no claim stronger than the evidence.

---

## 20. Success criteria

### Product success

- A developer can run one command or documented short sequence and obtain a real Scout evaluation.
- A detected violation is associated with a stable invariant and replayable evidence.
- The final trace is 1-minimal with respect to scheduled input removal.
- Equivalent normalized faults can be evaluated across Razorpay and PayU without changing the core verifier.

### AI success

- Scout is demonstrably trained or labelled as a heuristic without ambiguity.
- Under an equal execution budget, Scout improves early fault discovery over frozen baselines on held-out programs.
- The model's value survives ablation and is not explained solely by leakage or a single random seed.
- Inference is local, small, fast, and reproducible.

### Submission success

- Judges can understand the problem, the AI role, the deterministic authority boundary, the strongest result, and the evidence limits within the first minute.
- The demo shows a real failure, not only a dashboard.
- Every impressive number has a dataset label and reproducible provenance.
- The project is remembered as a correctness engine for agentic payments, not as a webhook deduplication utility.

---

## 21. Why Paystable, why Razorpay, why now

The Paystable commit history predates the buildathon. Its core architecture—durable ingestion, reconciliation, terminal-state handling, transactional outbox, signed callbacks, and evidence—was created because a real payment workflow failed. That origin matters: the verification laboratory is attached to a system that already understands the operational boundary it tests.

Razorpay is an appropriate primary demonstration because its documented delivery semantics expose the exact class of histories Paystable investigates, and Razorpay's AI Builder program asks for AI that performs a real, defensible engineering job. Scout gives AI a narrow role where learning can be measured: choosing the most informative test under budget. Paystable then surrounds that model with deterministic execution and explicit correctness rules suitable for money.

As payment products become more agentic, this boundary becomes more important. An agent may recommend, retry, reconcile, or automate, but it should not act on an event stream whose semantics have never been adversarially verified. Paystable's long-term role is the precondition layer: before software is trusted to act on money, make its assumptions executable and attack them.

---

## 22. References

- Razorpay, [Webhooks documentation](https://razorpay.com/docs/webhooks/)
- Razorpay, [Validate and test webhooks](https://razorpay.com/docs/webhooks/validate-test/)
- Razorpay, [Webhook FAQs](https://razorpay.com/docs/webhooks/faqs/)
- Jepsen, [example analysis and history-checking methodology](https://jepsen.io/analyses/rethinkdb-2-2-3-reconfiguration)
- Alvaro et al., [Lineage-driven fault injection](https://www2.eecs.berkeley.edu/Pubs/TechRpts/2015/EECS-2015-242.html)
- Zeller and Hildebrandt, [Simplifying and isolating failure-inducing input](https://www.st.cs.uni-saarland.de/papers/tse2002/)
- Pan et al., [Machine learning for test-case prioritisation: a systematic review](https://doi.org/10.1007/s10664-021-10066-6)
