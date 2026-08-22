package verification

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"slices"

	"github.com/IDEA-Amrita/paystable/testkit/merchantcase"
)

type IndependentReport struct {
	Version         int                     `json:"version"`
	Evaluation      string                  `json:"evaluation"`
	Budget          int                     `json:"budget"`
	Seed            int64                   `json:"seed"`
	RandomSeeds     []int64                 `json:"random_seeds"`
	ScoutModelBytes int                     `json:"scout_model_bytes"`
	Cases           []IndependentCaseReport `json:"cases"`
	Runs            []BaselineRun           `json:"runs"`
	Summary         []BaselineSummary       `json:"summary"`
	Confidence      []SuccessConfidence     `json:"success_at_10_confidence"`
}

type SuccessConfidence struct {
	Method            string  `json:"method"`
	Trials            int     `json:"trials"`
	Successes         int     `json:"successes"`
	Rate              float64 `json:"rate"`
	Lower95           float64 `json:"lower_95"`
	Upper95           float64 `json:"upper_95"`
	FalseFindingCount int     `json:"false_finding_count"`
}

type IndependentCaseReport struct {
	Program           string `json:"program"`
	Family            string `json:"family"`
	ExpectedInvariant string `json:"expected_invariant,omitempty"`
	CandidateCount    int    `json:"candidate_count"`
}

type independentCase struct {
	program ProgramCase
	actions []Action
	execute func(Schedule) (Result, error)
}

type rawMerchant interface {
	Deliver([]byte) (bool, error)
	Fulfill() error
	Snapshot() merchantcase.Snapshot
}

type independentState struct {
	running      bool
	state        string
	capturedOnce bool
	pending      bool
	seen         map[string]bool
	effects      map[string]bool
	attempt      int
	effectCount  int
	trace        []TraceEntry
}

// RunIndependentReport evaluates Scout against merchant code outside the training simulator.
func RunIndependentReport(training ProgramCorpus, budget int, seed int64) (IndependentReport, error) {
	if budget < 50 || budget > 1000 {
		return IndependentReport{}, fmt.Errorf("budget must be between 50 and 1000")
	}
	model, err := TrainScout(training)
	if err != nil {
		return IndependentReport{}, err
	}
	modelJSON, err := json.Marshal(model)
	if err != nil {
		return IndependentReport{}, err
	}
	report := IndependentReport{
		Version: 8, Evaluation: "independent-merchant-implementations",
		Budget: budget, Seed: seed, ScoutModelBytes: len(modelJSON),
	}
	for offset := int64(0); offset < 20; offset++ {
		report.RandomSeeds = append(report.RandomSeeds, seed+offset)
	}
	cases := independentCases()
	corpus := ProgramCorpus{Version: training.Version, MaxScheduleActions: 4}
	var randomTrials []BaselineRun
	for i, testCase := range cases {
		candidates, err := enumerateIndependentCandidates(testCase, corpus.MaxScheduleActions)
		if err != nil {
			return IndependentReport{}, err
		}
		if len(candidates) == 0 {
			return IndependentReport{}, fmt.Errorf("program %s has no legal schedules", testCase.program.Program)
		}
		corpus.Programs = append(corpus.Programs, testCase.program)
		report.Cases = append(report.Cases, IndependentCaseReport{
			Program: testCase.program.Program, Family: testCase.program.Family,
			ExpectedInvariant: testCase.program.ExpectedInvariant, CandidateCount: len(candidates),
		})

		bounded, err := evaluateCandidatesWith(BaselineBounded, testCase.program, slices.Clone(candidates), budget, testCase.execute)
		if err != nil {
			return IndependentReport{}, err
		}
		report.Runs = append(report.Runs, bounded)

		for seedIndex, randomSeed := range report.RandomSeeds {
			randomCandidates := slices.Clone(candidates)
			rand.New(rand.NewSource(randomSeed+int64(i))).Shuffle(len(randomCandidates), func(i, j int) {
				randomCandidates[i], randomCandidates[j] = randomCandidates[j], randomCandidates[i]
			})
			randomRun, err := evaluateCandidatesWith(BaselineRandom, testCase.program, randomCandidates, budget, testCase.execute)
			if err != nil {
				return IndependentReport{}, err
			}
			randomTrials = append(randomTrials, randomRun)
			if seedIndex == 0 {
				report.Runs = append(report.Runs, randomRun)
			}
		}

		coverage, err := evaluateCandidatesWith(BaselineCoverage, testCase.program, coverageOrder(candidates), budget, testCase.execute)
		if err != nil {
			return IndependentReport{}, err
		}
		report.Runs = append(report.Runs, coverage)

		scoutCandidates := slices.Clone(candidates)
		rankScoutCandidates(scoutCandidates, model)
		scout, err := evaluateCandidatesWith(ScoutMethod, testCase.program, scoutCandidates, budget, testCase.execute)
		if err != nil {
			return IndependentReport{}, err
		}
		report.Runs = append(report.Runs, scout)

		closed, err := evaluateClosedLoopWith(testCase.program, slices.Clone(candidates), budget, model, testCase.execute)
		if err != nil {
			return IndependentReport{}, err
		}
		report.Runs = append(report.Runs, closed)
	}
	for _, method := range []string{BaselineBounded, BaselineRandom, BaselineCoverage, ScoutMethod, ScoutClosedLoopMethod} {
		report.Summary = append(report.Summary, summarizeBaseline(method, corpus, report.Runs))
		runs := report.Runs
		if method == BaselineRandom {
			runs = randomTrials
		}
		report.Confidence = append(report.Confidence, successConfidence(method, corpus, runs))
	}
	return report, nil
}

func successConfidence(method string, corpus ProgramCorpus, runs []BaselineRun) SuccessConfidence {
	vulnerable := make(map[string]bool)
	for _, program := range corpus.Programs {
		if program.ExpectedInvariant != "" {
			vulnerable[program.Program] = true
		}
	}
	estimate := SuccessConfidence{Method: method}
	for _, run := range runs {
		if run.Method != method {
			continue
		}
		estimate.FalseFindingCount += run.FalseFindings
		if !vulnerable[run.Program] {
			continue
		}
		estimate.Trials++
		if run.Found && run.FirstFindingExecution <= 10 {
			estimate.Successes++
		}
	}
	if estimate.Trials == 0 {
		return estimate
	}
	estimate.Rate = float64(estimate.Successes) / float64(estimate.Trials)
	z := 1.959963984540054
	n := float64(estimate.Trials)
	center := (estimate.Rate + z*z/(2*n)) / (1 + z*z/n)
	margin := z * math.Sqrt(estimate.Rate*(1-estimate.Rate)/n+z*z/(4*n*n)) / (1 + z*z/n)
	estimate.Lower95 = center - margin
	estimate.Upper95 = center + margin
	return estimate
}

func independentCases() []independentCase {
	trustedCaptured := Action{Type: "deliver", EventID: "captured", Status: "captured"}
	trustedFailed := Action{Type: "deliver", EventID: "failed", Status: "failed"}
	parallelCaptured := Action{Type: "deliver", EventID: "parallel", Status: "captured", Parallel: 2}
	wrongAmount := Action{Type: "deliver", EventID: "wrong-amount", Status: "captured", Amount: 1}
	wrongOrder := Action{Type: "deliver", EventID: "wrong-order", Status: "captured", PaymentOrderID: "order_other"}
	wrongCurrency := Action{Type: "deliver", EventID: "wrong-currency", Status: "captured", Currency: "USD"}
	databaseConflict := Action{Type: "fulfill", Response: "db-conflict"}
	databaseDeadlock := Action{Type: "fulfill", Response: "db-deadlock"}
	serverError := Action{Type: "fulfill", Response: "http-500"}
	fulfillOK := Action{Type: "fulfill", Response: "ok"}
	return []independentCase{
		{
			program: ProgramCase{Program: "external-dedup-unsafe", Family: "deduplication-order", ExpectedInvariant: InvariantFulfillmentAtMostOnce},
			actions: []Action{trustedCaptured, trustedFailed, {Type: "deliver", EventID: "captured", Status: "captured", CrashAt: "after_fulfillment"}, {Type: "restart"}},
			execute: func(schedule Schedule) (Result, error) { return runIndependentDedup(schedule, true) },
		},
		{
			program: ProgramCase{Program: "external-dedup-safe", Family: "correct-deduplication"},
			actions: []Action{trustedCaptured, trustedFailed, {Type: "deliver", EventID: "captured", Status: "captured", CrashAt: "after_fulfillment"}, {Type: "restart"}, fulfillOK},
			execute: func(schedule Schedule) (Result, error) { return runIndependentDedup(schedule, false) },
		},
		{
			program: ProgramCase{Program: "external-retry-unsafe", Family: "retry-idempotency", ExpectedInvariant: InvariantFulfillmentAtMostOnce},
			actions: retryActions(trustedCaptured, trustedFailed, fulfillOK),
			execute: func(schedule Schedule) (Result, error) { return runIndependentRetry(schedule, true) },
		},
		{
			program: ProgramCase{Program: "external-retry-safe", Family: "correct-retry"},
			actions: retryActions(trustedCaptured, trustedFailed, fulfillOK),
			execute: func(schedule Schedule) (Result, error) { return runIndependentRetry(schedule, false) },
		},
		{
			program: ProgramCase{Program: "external-db-conflict-unsafe", Family: "database-conflict", ExpectedInvariant: InvariantFulfillmentAtMostOnce},
			actions: []Action{trustedCaptured, trustedFailed, databaseConflict, fulfillOK},
			execute: func(schedule Schedule) (Result, error) { return runIndependentRetry(schedule, true) },
		},
		{
			program: ProgramCase{Program: "external-db-conflict-safe", Family: "correct-db-conflict"},
			actions: []Action{trustedCaptured, trustedFailed, databaseConflict, fulfillOK},
			execute: func(schedule Schedule) (Result, error) { return runIndependentRetry(schedule, false) },
		},
		{
			program: ProgramCase{Program: "external-db-deadlock-unsafe", Family: "database-deadlock", ExpectedInvariant: InvariantFulfillmentAtMostOnce},
			actions: []Action{trustedCaptured, trustedFailed, databaseDeadlock, fulfillOK},
			execute: func(schedule Schedule) (Result, error) { return runIndependentRetry(schedule, true) },
		},
		{
			program: ProgramCase{Program: "external-db-deadlock-safe", Family: "correct-db-deadlock"},
			actions: []Action{trustedCaptured, trustedFailed, databaseDeadlock, fulfillOK},
			execute: func(schedule Schedule) (Result, error) { return runIndependentRetry(schedule, false) },
		},
		{
			program: ProgramCase{Program: "external-retry-overrun", Family: "retry-exhaustion", ExpectedInvariant: InvariantRetryBounded},
			actions: []Action{trustedCaptured, serverError},
			execute: func(schedule Schedule) (Result, error) { return runIndependentExhaustion(schedule, true) },
		},
		{
			program: ProgramCase{Program: "external-retry-bounded", Family: "correct-retry-exhaustion"},
			actions: []Action{trustedCaptured, serverError},
			execute: func(schedule Schedule) (Result, error) { return runIndependentExhaustion(schedule, false) },
		},
		{
			program: ProgramCase{Program: "external-concurrent-unsafe", Family: "concurrent-deduplication", ExpectedInvariant: InvariantFulfillmentAtMostOnce},
			actions: []Action{trustedCaptured, trustedFailed, parallelCaptured, fulfillOK},
			execute: func(schedule Schedule) (Result, error) { return runIndependentConcurrent(schedule, true) },
		},
		{
			program: ProgramCase{Program: "external-concurrent-safe", Family: "correct-concurrency"},
			actions: []Action{trustedCaptured, trustedFailed, parallelCaptured, fulfillOK},
			execute: func(schedule Schedule) (Result, error) { return runIndependentConcurrent(schedule, false) },
		},
		{
			program: ProgramCase{Program: "external-terminal-unsafe", Family: "terminal-state", ExpectedInvariant: InvariantTerminalStateStable},
			actions: []Action{trustedCaptured, trustedFailed, fulfillOK},
			execute: func(schedule Schedule) (Result, error) { return runIndependentTerminal(schedule, true) },
		},
		{
			program: ProgramCase{Program: "external-terminal-safe", Family: "correct-terminal"},
			actions: []Action{trustedCaptured, trustedFailed, fulfillOK},
			execute: func(schedule Schedule) (Result, error) { return runIndependentTerminal(schedule, false) },
		},
		{
			program: ProgramCase{Program: "external-auth-unsafe", Family: "webhook-authentication", ExpectedInvariant: InvariantTrustedEventsOnly},
			actions: authActions(trustedCaptured, trustedFailed, fulfillOK),
			execute: func(schedule Schedule) (Result, error) { return runIndependentAuth(schedule, true) },
		},
		{
			program: ProgramCase{Program: "external-auth-safe", Family: "correct-authentication"},
			actions: authActions(trustedCaptured, trustedFailed, fulfillOK),
			execute: func(schedule Schedule) (Result, error) { return runIndependentAuth(schedule, false) },
		},
		{
			program: ProgramCase{Program: "external-amount-unsafe", Family: "payment-amount", ExpectedInvariant: InvariantExpectedAmount},
			actions: []Action{trustedCaptured, trustedFailed, wrongAmount, fulfillOK},
			execute: func(schedule Schedule) (Result, error) { return runIndependentAmount(schedule, true) },
		},
		{
			program: ProgramCase{Program: "external-amount-safe", Family: "correct-amount"},
			actions: []Action{trustedCaptured, trustedFailed, wrongAmount, fulfillOK},
			execute: func(schedule Schedule) (Result, error) { return runIndependentAmount(schedule, false) },
		},
		{
			program: ProgramCase{Program: "raw-json-amount-unsafe", Family: "payment-amount", ExpectedInvariant: InvariantExpectedAmount},
			actions: []Action{trustedCaptured, trustedFailed, wrongAmount, fulfillOK},
			execute: func(schedule Schedule) (Result, error) {
				return runRawAmountMerchant(schedule, merchantcase.NewVulnerableAmount())
			},
		},
		{
			program: ProgramCase{Program: "raw-json-amount-safe", Family: "correct-amount"},
			actions: []Action{trustedCaptured, trustedFailed, wrongAmount, fulfillOK},
			execute: func(schedule Schedule) (Result, error) {
				return runRawAmountMerchant(schedule, merchantcase.NewCorrectAmount(ExpectedPaymentAmount))
			},
		},
		{
			program: ProgramCase{Program: "raw-json-order-unsafe", Family: "payment-order", ExpectedInvariant: InvariantExpectedOrder},
			actions: []Action{trustedCaptured, trustedFailed, wrongOrder, fulfillOK},
			execute: func(schedule Schedule) (Result, error) {
				return runRawBindingMerchant(schedule, merchantcase.NewVulnerableBinding())
			},
		},
		{
			program: ProgramCase{Program: "raw-json-order-safe", Family: "correct-order"},
			actions: []Action{trustedCaptured, trustedFailed, wrongOrder, fulfillOK},
			execute: func(schedule Schedule) (Result, error) {
				return runRawBindingMerchant(schedule, merchantcase.NewCorrectBinding(schedule.OrderID))
			},
		},
		{
			program: ProgramCase{Program: "raw-json-currency-unsafe", Family: "payment-currency", ExpectedInvariant: InvariantExpectedCurrency},
			actions: []Action{trustedCaptured, trustedFailed, wrongCurrency, fulfillOK},
			execute: func(schedule Schedule) (Result, error) {
				return runRawCurrencyMerchant(schedule, merchantcase.NewVulnerableCurrency())
			},
		},
		{
			program: ProgramCase{Program: "raw-json-currency-safe", Family: "correct-currency"},
			actions: []Action{trustedCaptured, trustedFailed, wrongCurrency, fulfillOK},
			execute: func(schedule Schedule) (Result, error) {
				return runRawCurrencyMerchant(schedule, merchantcase.NewCorrectCurrency(ExpectedPaymentCurrency))
			},
		},
	}
}

func retryActions(captured, failed, ok Action) []Action {
	return []Action{
		captured, failed, ok,
		{Type: "fulfill", Response: "lost"},
		{Type: "fulfill", Response: "timeout"},
		{Type: "fulfill", Response: "connection-reset"},
		{Type: "fulfill", Response: "http-500"},
	}
}

func authActions(captured, failed, ok Action) []Action {
	return []Action{
		captured, failed,
		{Type: "deliver", EventID: "invalid", Status: "captured", Trust: "invalid-signature"},
		{Type: "deliver", EventID: "tampered", Status: "captured", Trust: "tampered-body"},
		ok,
	}
}

func enumerateIndependentCandidates(testCase independentCase, maxActions int) ([]searchCandidate, error) {
	var candidates []searchCandidate
	for length := 1; length <= maxActions; length++ {
		var walk func([]Action)
		walk = func(actions []Action) {
			if len(actions) == length {
				schedule := Schedule{
					Name: "independent candidate", Program: testCase.program.Program,
					OrderID: "order_independent", Actions: slices.Clone(actions),
				}
				result, err := testCase.execute(schedule)
				if err != nil {
					return
				}
				features := make([]string, 0, len(result.Trace))
				for _, entry := range result.Trace {
					features = append(features, observableStateFeature(entry.State, entry.EffectCount))
				}
				terminal := fmt.Sprintf("%s|%d|%#v", result.FinalState, result.EffectCount, result.Violations)
				candidateActions := slices.Clone(actions)
				candidates = append(candidates, searchCandidate{
					actions: candidateActions, features: features, terminal: terminal, profile: scoutProfile(candidateActions),
				})
				return
			}
			for _, action := range testCase.actions {
				walk(append(actions, action))
			}
		}
		walk(nil)
	}
	return candidates, nil
}

func validateIndependent(schedule Schedule) error {
	check := schedule
	check.Program = ProgramCorrect
	return Validate(check)
}

func newIndependentState() independentState {
	return independentState{running: true, seen: make(map[string]bool), effects: make(map[string]bool)}
}

func (s *independentState) record(action, detail string) {
	s.trace = append(s.trace, TraceEntry{
		Sequence: len(s.trace) + 1, Action: action, Detail: detail,
		State: s.state, EffectCount: s.effectCount,
	})
}

func (s *independentState) result(schedule Schedule) Result {
	return ResultFor(schedule, s.state, s.capturedOnce, s.effectCount, s.trace)
}

func trusted(action Action) bool {
	return action.Trust == "" || action.Trust == "valid"
}

func runIndependentDedup(schedule Schedule, unsafe bool) (Result, error) {
	if err := validateIndependent(schedule); err != nil {
		return Result{}, err
	}
	s := newIndependentState()
	for _, action := range schedule.Actions {
		switch action.Type {
		case "restart":
			if s.running {
				return Result{}, fmt.Errorf("cannot restart a running merchant")
			}
			s.running = true
			s.record("restart", "merchant restarted")
		case "deliver":
			if !s.running {
				return Result{}, fmt.Errorf("cannot deliver while the merchant is stopped")
			}
			if !trusted(action) {
				s.record("reject", "untrusted payment event rejected")
				continue
			}
			if s.seen[action.EventID] {
				s.record("deliver", "duplicate event ignored")
				continue
			}
			if unsafe && action.Status == "captured" {
				s.effectCount++
				s.record("fulfill", "fulfillment occurred before event storage")
				if action.CrashAt != "" {
					s.running = false
					s.record("crash", "merchant crashed before event storage")
					continue
				}
			}
			s.seen[action.EventID] = true
			s.record("checkpoint", "event stored")
			if s.state != "captured" {
				s.state = action.Status
			}
			if s.state == "captured" {
				s.capturedOnce = true
				if !unsafe {
					s.pending = true
				}
			}
			s.record("deliver", "payment state updated")
		case "fulfill":
			if !s.running || !s.pending {
				return Result{}, fmt.Errorf("payment is not ready for fulfillment")
			}
			if !s.effects[schedule.OrderID] {
				s.effects[schedule.OrderID] = true
				s.effectCount++
			}
			s.record("fulfill", "fulfillment sink accepted the stable key")
			s.pending = action.Response != "ok"
		}
	}
	if !s.running {
		return Result{}, fmt.Errorf("schedule ended while the merchant was stopped")
	}
	return s.result(schedule), nil
}

func runIndependentRetry(schedule Schedule, unsafe bool) (Result, error) {
	if err := validateIndependent(schedule); err != nil {
		return Result{}, err
	}
	s := newIndependentState()
	for _, action := range schedule.Actions {
		switch action.Type {
		case "deliver":
			if !trusted(action) {
				s.record("reject", "untrusted payment event rejected")
				continue
			}
			if s.seen[action.EventID] {
				s.record("deliver", "duplicate event ignored")
				continue
			}
			s.seen[action.EventID] = true
			if s.state != "captured" {
				s.state = action.Status
			}
			if s.state == "captured" {
				s.capturedOnce = true
				s.pending = true
			}
			s.record("deliver", "payment state updated")
		case "fulfill":
			if !s.pending {
				return Result{}, fmt.Errorf("payment is not ready for fulfillment")
			}
			s.attempt++
			key := schedule.OrderID
			if unsafe {
				key = fmt.Sprintf("%s:%d", key, s.attempt)
			}
			if !s.effects[key] {
				s.effects[key] = true
				s.effectCount++
			}
			s.record("fulfill", "fulfillment sink accepted key "+key)
			if action.Response != "ok" {
				s.record(ResponseTraceAction(action.Response), "fulfillment response was uncertain")
			}
			s.pending = action.Response != "ok"
		default:
			return Result{}, fmt.Errorf("retry merchant does not support %s", action.Type)
		}
	}
	return s.result(schedule), nil
}

func runIndependentConcurrent(schedule Schedule, unsafe bool) (Result, error) {
	if err := validateIndependent(schedule); err != nil {
		return Result{}, err
	}
	s := newIndependentState()
	for _, action := range schedule.Actions {
		switch action.Type {
		case "deliver":
			if !trusted(action) {
				s.record("reject", "untrusted payment event rejected")
				continue
			}
			copies := action.Parallel
			if copies == 0 {
				copies = 1
			}
			for range copies {
				beforeClaim := unsafe && action.Parallel == 2 && action.Status == "captured"
				if beforeClaim {
					s.effectCount++
					s.record("fulfill", "fulfillment occurred before the event claim")
				}
				if s.seen[action.EventID] {
					s.record("deliver", "duplicate event ignored")
					continue
				}
				s.seen[action.EventID] = true
				if s.state != "captured" {
					s.state = action.Status
				}
				if s.state == "captured" {
					s.capturedOnce = true
					s.pending = !beforeClaim
				}
				s.record("deliver", "payment state updated")
			}
		case "fulfill":
			if !s.pending {
				return Result{}, fmt.Errorf("payment is not ready for fulfillment")
			}
			if !s.effects[schedule.OrderID] {
				s.effects[schedule.OrderID] = true
				s.effectCount++
			}
			s.pending = action.Response != "ok"
			s.record("fulfill", "fulfillment sink accepted the stable key")
		default:
			return Result{}, fmt.Errorf("concurrent merchant does not support %s", action.Type)
		}
	}
	return s.result(schedule), nil
}

func runIndependentExhaustion(schedule Schedule, unsafe bool) (Result, error) {
	if err := validateIndependent(schedule); err != nil {
		return Result{}, err
	}
	s := newIndependentState()
	for _, action := range schedule.Actions {
		switch action.Type {
		case "deliver":
			if s.seen[action.EventID] {
				s.record("deliver", "duplicate event ignored")
				continue
			}
			s.seen[action.EventID] = true
			s.state = action.Status
			if s.state == "captured" {
				s.capturedOnce = true
				s.pending = true
			}
			s.record("deliver", "payment state updated")
		case "fulfill":
			if !s.pending {
				return Result{}, fmt.Errorf("payment is not ready for fulfillment")
			}
			s.attempt++
			if !s.effects[schedule.OrderID] {
				s.effects[schedule.OrderID] = true
				s.effectCount++
			}
			s.record("fulfill", "fulfillment sink accepted the stable key")
			if action.Response == "ok" {
				s.pending = false
				continue
			}
			s.record(ResponseTraceAction(action.Response), "fulfillment response was uncertain")
			if !unsafe && s.attempt >= 2 {
				s.pending = false
				s.record("retry_exhausted", "fulfillment retry limit reached")
			}
			if unsafe && s.attempt > 2 {
				s.record("retry_overrun", "fulfillment continued after the retry limit")
			}
		default:
			return Result{}, fmt.Errorf("retry-limit merchant does not support %s", action.Type)
		}
	}
	return s.result(schedule), nil
}

func runIndependentTerminal(schedule Schedule, unsafe bool) (Result, error) {
	if err := validateIndependent(schedule); err != nil {
		return Result{}, err
	}
	s := newIndependentState()
	for _, action := range schedule.Actions {
		switch action.Type {
		case "deliver":
			if !trusted(action) {
				s.record("reject", "untrusted payment event rejected")
				continue
			}
			if unsafe || s.state != "captured" {
				s.state = action.Status
			}
			if s.state == "captured" {
				s.capturedOnce = true
				s.pending = true
			}
			s.record("deliver", "payment state updated")
		case "fulfill":
			if !s.pending {
				return Result{}, fmt.Errorf("payment is not ready for fulfillment")
			}
			if !s.effects[schedule.OrderID] {
				s.effects[schedule.OrderID] = true
				s.effectCount++
			}
			s.pending = action.Response != "ok"
			s.record("fulfill", "fulfillment sink accepted the stable key")
		default:
			return Result{}, fmt.Errorf("terminal merchant does not support %s", action.Type)
		}
	}
	return s.result(schedule), nil
}

func runIndependentAuth(schedule Schedule, unsafe bool) (Result, error) {
	if err := validateIndependent(schedule); err != nil {
		return Result{}, err
	}
	s := newIndependentState()
	for _, action := range schedule.Actions {
		switch action.Type {
		case "deliver":
			if !trusted(action) && !unsafe {
				s.record("reject", "untrusted payment event rejected")
				continue
			}
			if !trusted(action) {
				s.record("untrusted_accept", "untrusted payment event accepted")
			}
			if s.seen[action.EventID] {
				s.record("deliver", "duplicate event ignored")
				continue
			}
			s.seen[action.EventID] = true
			if s.state != "captured" {
				s.state = action.Status
			}
			if s.state == "captured" {
				s.capturedOnce = true
				s.pending = true
			}
			s.record("deliver", "payment state updated")
		case "fulfill":
			if !s.pending {
				return Result{}, fmt.Errorf("payment is not ready for fulfillment")
			}
			if !s.effects[schedule.OrderID] {
				s.effects[schedule.OrderID] = true
				s.effectCount++
			}
			s.pending = action.Response != "ok"
			s.record("fulfill", "fulfillment sink accepted the stable key")
		default:
			return Result{}, fmt.Errorf("authentication merchant does not support %s", action.Type)
		}
	}
	return s.result(schedule), nil
}

func runIndependentAmount(schedule Schedule, unsafe bool) (Result, error) {
	if err := validateIndependent(schedule); err != nil {
		return Result{}, err
	}
	s := newIndependentState()
	for _, action := range schedule.Actions {
		switch action.Type {
		case "deliver":
			if !trusted(action) {
				s.record("reject", "untrusted payment event rejected")
				continue
			}
			if HasAmountMismatch(action) && !unsafe {
				s.record("reject", "payment amount mismatch rejected")
				continue
			}
			if HasAmountMismatch(action) {
				s.record("amount_mismatch_accept", "payment amount mismatch accepted")
			}
			if s.seen[action.EventID] {
				s.record("deliver", "duplicate event ignored")
				continue
			}
			s.seen[action.EventID] = true
			if s.state != "captured" {
				s.state = action.Status
			}
			if s.state == "captured" {
				s.capturedOnce = true
				s.pending = true
			}
			s.record("deliver", "payment state updated")
		case "fulfill":
			if !s.pending {
				return Result{}, fmt.Errorf("payment is not ready for fulfillment")
			}
			if !s.effects[schedule.OrderID] {
				s.effects[schedule.OrderID] = true
				s.effectCount++
			}
			s.pending = action.Response != "ok"
			s.record("fulfill", "fulfillment sink accepted the stable key")
		default:
			return Result{}, fmt.Errorf("amount merchant does not support %s", action.Type)
		}
	}
	return s.result(schedule), nil
}

func runRawAmountMerchant(schedule Schedule, merchant rawMerchant) (Result, error) {
	if err := validateIndependent(schedule); err != nil {
		return Result{}, err
	}
	var trace []TraceEntry
	record := func(action, detail string) {
		snapshot := merchant.Snapshot()
		trace = append(trace, TraceEntry{
			Sequence: len(trace) + 1, Action: action, Detail: detail,
			State: snapshot.State, EffectCount: snapshot.EffectCount,
		})
	}
	for _, action := range schedule.Actions {
		switch action.Type {
		case "deliver":
			amount := action.Amount
			if amount == 0 {
				amount = ExpectedPaymentAmount
			}
			body, err := json.Marshal(struct {
				EventID string `json:"event_id"`
				Status  string `json:"status"`
				Amount  int64  `json:"amount"`
			}{action.EventID, action.Status, amount})
			if err != nil {
				return Result{}, err
			}
			accepted, err := merchant.Deliver(body)
			if err != nil {
				return Result{}, err
			}
			if !accepted {
				record("reject", "payment amount mismatch rejected")
				continue
			}
			if HasAmountMismatch(action) {
				record("amount_mismatch_accept", "payment amount mismatch accepted")
			}
			record("deliver", "raw payment event processed")
		case "fulfill":
			if err := merchant.Fulfill(); err != nil {
				return Result{}, err
			}
			record("fulfill", "fulfillment sink accepted the stable key")
		default:
			return Result{}, fmt.Errorf("raw amount merchant does not support %s", action.Type)
		}
	}
	snapshot := merchant.Snapshot()
	return ResultFor(schedule, snapshot.State, snapshot.CapturedOnce, snapshot.EffectCount, trace), nil
}

func runRawBindingMerchant(schedule Schedule, merchant rawMerchant) (Result, error) {
	if err := validateIndependent(schedule); err != nil {
		return Result{}, err
	}
	var trace []TraceEntry
	record := func(action, detail string) {
		snapshot := merchant.Snapshot()
		trace = append(trace, TraceEntry{
			Sequence: len(trace) + 1, Action: action, Detail: detail,
			State: snapshot.State, EffectCount: snapshot.EffectCount,
		})
	}
	for _, action := range schedule.Actions {
		switch action.Type {
		case "deliver":
			orderID := action.PaymentOrderID
			if orderID == "" {
				orderID = schedule.OrderID
			}
			body, err := json.Marshal(struct {
				EventID string `json:"event_id"`
				Status  string `json:"status"`
				OrderID string `json:"order_id"`
			}{action.EventID, action.Status, orderID})
			if err != nil {
				return Result{}, err
			}
			accepted, err := merchant.Deliver(body)
			if err != nil {
				return Result{}, err
			}
			if !accepted {
				record("reject", "payment order mismatch rejected")
				continue
			}
			if HasOrderMismatch(schedule.OrderID, action) {
				record("order_mismatch_accept", "payment order mismatch accepted")
			}
			record("deliver", "raw payment event processed")
		case "fulfill":
			if err := merchant.Fulfill(); err != nil {
				return Result{}, err
			}
			record("fulfill", "fulfillment sink accepted the stable key")
		default:
			return Result{}, fmt.Errorf("raw order merchant does not support %s", action.Type)
		}
	}
	snapshot := merchant.Snapshot()
	return ResultFor(schedule, snapshot.State, snapshot.CapturedOnce, snapshot.EffectCount, trace), nil
}

func runRawCurrencyMerchant(schedule Schedule, merchant rawMerchant) (Result, error) {
	if err := validateIndependent(schedule); err != nil {
		return Result{}, err
	}
	var trace []TraceEntry
	record := func(action, detail string) {
		snapshot := merchant.Snapshot()
		trace = append(trace, TraceEntry{
			Sequence: len(trace) + 1, Action: action, Detail: detail,
			State: snapshot.State, EffectCount: snapshot.EffectCount,
		})
	}
	for _, action := range schedule.Actions {
		switch action.Type {
		case "deliver":
			currency := action.Currency
			if currency == "" {
				currency = ExpectedPaymentCurrency
			}
			body, err := json.Marshal(struct {
				EventID  string `json:"event_id"`
				Status   string `json:"status"`
				Currency string `json:"currency"`
			}{action.EventID, action.Status, currency})
			if err != nil {
				return Result{}, err
			}
			accepted, err := merchant.Deliver(body)
			if err != nil {
				return Result{}, err
			}
			if !accepted {
				record("reject", "payment currency mismatch rejected")
				continue
			}
			if HasCurrencyMismatch(action) {
				record("currency_mismatch_accept", "payment currency mismatch accepted")
			}
			record("deliver", "raw payment event processed")
		case "fulfill":
			if err := merchant.Fulfill(); err != nil {
				return Result{}, err
			}
			record("fulfill", "fulfillment sink accepted the stable key")
		default:
			return Result{}, fmt.Errorf("raw currency merchant does not support %s", action.Type)
		}
	}
	snapshot := merchant.Snapshot()
	return ResultFor(schedule, snapshot.State, snapshot.CapturedOnce, snapshot.EffectCount, trace), nil
}
