package verification

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"slices"
)

const (
	ScoutMethod           = "scout"
	ScoutClosedLoopMethod = "scout-closed-loop"
	ScoutPriorFreeMethod  = "scout-prior-free"
)

var scoutFeatureNames = []string{
	"action_count",
	"deliver_count",
	"fulfill_count",
	"restart_count",
	"captured_count",
	"failed_count",
	"crash_count",
	"uncertain_response_count",
	"duplicate_event_count",
	"deliver_after_restart",
	"fulfill_after_uncertain_response",
	"failed_after_captured",
	"same_event_after_restart",
	"missing_signature_count",
	"invalid_signature_count",
	"tampered_body_count",
	"timeout_count",
	"connection_reset_count",
	"server_error_count",
	"untrusted_captured",
	"parallel_delivery_count",
	"amount_mismatch_count",
	"order_mismatch_count",
	"currency_mismatch_count",
}

var scoutPriorWeights = map[string]float64{
	"same_event_after_restart":         1,
	"fulfill_after_uncertain_response": 1,
	"failed_after_captured":            1,
	"untrusted_captured":               1,
	"parallel_delivery_count":          1,
	"amount_mismatch_count":            1,
	"order_mismatch_count":             1,
	"currency_mismatch_count":          1,
}

type ScoutModel struct {
	Version       int       `json:"version"`
	FeatureNames  []string  `json:"feature_names"`
	Weights       []float64 `json:"weights"`
	Epochs        int       `json:"epochs"`
	LearningRate  float64   `json:"learning_rate"`
	TrainingPairs int       `json:"training_pairs"`
}

type ScoutReport struct {
	Version         int             `json:"version"`
	Evaluation      string          `json:"evaluation"`
	Budget          int             `json:"budget"`
	ModelBytes      int             `json:"model_bytes"`
	Model           ScoutModel      `json:"model"`
	EvaluationFolds []ScoutFold     `json:"evaluation_folds"`
	Runs            []BaselineRun   `json:"runs"`
	Summary         BaselineSummary `json:"summary"`
}

type ScoutFold struct {
	HeldOutFamily string     `json:"held_out_family"`
	Model         ScoutModel `json:"model"`
}

type ScoutStressReport struct {
	Version              int               `json:"version"`
	Evaluation           string            `json:"evaluation"`
	Budget               int               `json:"budget"`
	Seed                 int64             `json:"seed"`
	RandomSeeds          []int64           `json:"random_seeds"`
	Runs                 []BaselineRun     `json:"runs"`
	Confidence           SuccessConfidence `json:"success_at_10_confidence"`
	AmbiguousScoreGroups int               `json:"ambiguous_score_groups"`
	AmbiguousFamilies    []string          `json:"ambiguous_families"`
	AdversarialRuns      []BaselineRun     `json:"adversarial_runs"`
	AdversarialSummary   BaselineSummary   `json:"adversarial_summary"`
}

// TrainScout fits a linear pairwise ranker to deterministic invariant results.
func TrainScout(corpus ProgramCorpus) (ScoutModel, error) {
	return trainScout(corpus, "")
}

func trainScout(corpus ProgramCorpus, excludedFamily string) (ScoutModel, error) {
	return trainScoutWithPriors(corpus, excludedFamily, true)
}

func trainScoutWithPriors(corpus ProgramCorpus, excludedFamily string, fixedPriors bool) (ScoutModel, error) {
	model := ScoutModel{
		Version: 2, FeatureNames: slices.Clone(scoutFeatureNames),
		Weights: make([]float64, len(scoutFeatureNames)), Epochs: 40, LearningRate: 0.01,
	}
	if fixedPriors {
		for i, name := range model.FeatureNames {
			model.Weights[i] = scoutPriorWeights[name]
		}
	}
	type pair struct{ positive, negative []float64 }
	var pairs []pair
	for _, program := range corpus.Programs {
		if program.ExpectedInvariant == "" || program.Family == excludedFamily {
			continue
		}
		graph, err := CompileBehaviorGraph(program.Program, corpus.MaxScheduleActions)
		if err != nil {
			return ScoutModel{}, err
		}
		var positives, negatives [][]float64
		for i, candidate := range graphCandidates(graph) {
			schedule := Schedule{
				Name: "Scout training candidate", Program: program.Program,
				OrderID: fmt.Sprintf("order_scout_training_%d", i+1), Actions: candidate.actions,
			}
			result, err := Run(schedule)
			if err != nil {
				return ScoutModel{}, err
			}
			features := scoutFeatures(candidate.actions)
			if resultHasInvariant(result, program.ExpectedInvariant) {
				positives = append(positives, features)
			} else {
				negatives = append(negatives, features)
			}
		}
		for _, positive := range positives {
			for _, negative := range negatives {
				pairs = append(pairs, pair{positive: positive, negative: negative})
			}
		}
	}
	if len(pairs) == 0 {
		return ScoutModel{}, fmt.Errorf("training corpus has no ranking pairs")
	}
	model.TrainingPairs = len(pairs)
	for range model.Epochs {
		for _, pair := range pairs {
			if scoutScore(model.Weights, pair.positive)-scoutScore(model.Weights, pair.negative) >= 1 {
				continue
			}
			for i := range model.Weights {
				model.Weights[i] += model.LearningRate * (pair.positive[i] - pair.negative[i])
			}
		}
	}
	return model, nil
}

// RunScoutReport trains Scout and evaluates it with the baseline execution budget.
func RunScoutReport(corpus ProgramCorpus, budget int) (ScoutReport, error) {
	return runScoutReport(corpus, budget, false, true)
}

// RunClosedLoopReport updates Scout after each runtime result.
func RunClosedLoopReport(corpus ProgramCorpus, budget int) (ScoutReport, error) {
	return runScoutReport(corpus, budget, true, true)
}

// RunPriorFreeScoutReport evaluates held-out families without fixed risk priors.
func RunPriorFreeScoutReport(corpus ProgramCorpus, budget int) (ScoutReport, error) {
	return runScoutReport(corpus, budget, false, false)
}

// RunPriorFreeStressReport shuffles score ties across repeated held-out trials.
func RunPriorFreeStressReport(corpus ProgramCorpus, budget int, seed int64) (ScoutStressReport, error) {
	if budget < 50 || budget > 1000 {
		return ScoutStressReport{}, fmt.Errorf("budget must be between 50 and 1000")
	}
	report := ScoutStressReport{
		Version: 1, Evaluation: "leave-one-family-out-prior-free-tie-stress", Budget: budget, Seed: seed,
	}
	for offset := int64(0); offset < 20; offset++ {
		report.RandomSeeds = append(report.RandomSeeds, seed+offset)
	}
	fullModel, err := trainScoutWithPriors(corpus, "", false)
	if err != nil {
		return ScoutStressReport{}, err
	}
	foldModels := make(map[string]ScoutModel)
	ambiguousFamilies := make(map[string]bool)
	for programIndex, program := range corpus.Programs {
		model := fullModel
		if program.ExpectedInvariant != "" {
			model, err = priorFreeFoldModel(corpus, program.Family, foldModels)
			if err != nil {
				return ScoutStressReport{}, err
			}
		}
		graph, err := CompileBehaviorGraph(program.Program, corpus.MaxScheduleActions)
		if err != nil {
			return ScoutStressReport{}, err
		}
		candidates := graphCandidates(graph)
		candidates, err = addPriorFreeHardNegatives(program, candidates)
		if err != nil {
			return ScoutStressReport{}, err
		}
		for _, trialSeed := range report.RandomSeeds {
			trial := slices.Clone(candidates)
			random := rand.New(rand.NewSource(trialSeed + int64(programIndex)*int64(len(report.RandomSeeds))))
			random.Shuffle(len(trial), func(i, j int) { trial[i], trial[j] = trial[j], trial[i] })
			rankScoutCandidates(trial, model)
			run, err := evaluateCandidates(ScoutPriorFreeMethod, program, trial, budget)
			if err != nil {
				return ScoutStressReport{}, err
			}
			report.Runs = append(report.Runs, run)
		}
		type labeledCandidate struct {
			candidate searchCandidate
			score     float64
			violates  bool
		}
		labeled := make([]labeledCandidate, 0, len(candidates))
		groups := make(map[uint64]struct{ safe, failing bool })
		for candidateIndex, candidate := range candidates {
			schedule := Schedule{
				Name: "prior-free stress candidate", Program: program.Program,
				OrderID: fmt.Sprintf("order_prior_free_stress_%d", candidateIndex+1), Actions: candidate.actions,
			}
			result, err := Run(schedule)
			if err != nil {
				return ScoutStressReport{}, err
			}
			violates := resultHasInvariant(result, program.ExpectedInvariant)
			score := model.score(candidate.actions)
			labeled = append(labeled, labeledCandidate{candidate: candidate, score: score, violates: violates})
			group := groups[math.Float64bits(score)]
			if violates {
				group.failing = true
			} else {
				group.safe = true
			}
			groups[math.Float64bits(score)] = group
		}
		for _, group := range groups {
			if group.safe && group.failing {
				report.AmbiguousScoreGroups++
				ambiguousFamilies[program.Family] = true
			}
		}
		slices.SortStableFunc(labeled, func(a, b labeledCandidate) int {
			if order := -compareFloat(a.score, b.score); order != 0 {
				return order
			}
			if a.violates == b.violates {
				return 0
			}
			if a.violates {
				return 1
			}
			return -1
		})
		adversarial := make([]searchCandidate, len(labeled))
		for i := range labeled {
			adversarial[i] = labeled[i].candidate
		}
		adversarialRun, err := evaluateCandidates(ScoutPriorFreeMethod, program, adversarial, budget)
		if err != nil {
			return ScoutStressReport{}, err
		}
		report.AdversarialRuns = append(report.AdversarialRuns, adversarialRun)
	}
	for family := range ambiguousFamilies {
		report.AmbiguousFamilies = append(report.AmbiguousFamilies, family)
	}
	slices.Sort(report.AmbiguousFamilies)
	report.Confidence = successConfidence(ScoutPriorFreeMethod, corpus, report.Runs)
	report.AdversarialSummary = summarizeBaseline(ScoutPriorFreeMethod, corpus, report.AdversarialRuns)
	return report, nil
}

func addPriorFreeHardNegatives(program ProgramCase, candidates []searchCandidate) ([]searchCandidate, error) {
	if program.Family != "payment-amount" && program.Family != "payment-currency" {
		return candidates, nil
	}
	augmented := slices.Clone(candidates)
	for candidateIndex, candidate := range candidates {
		actions := slices.Clone(candidate.actions)
		changed := false
		for i := range actions {
			if program.Family == "payment-amount" && HasAmountMismatch(actions[i]) {
				actions[i].Amount = ExpectedPaymentAmount
				changed = true
			}
			if program.Family == "payment-currency" && HasCurrencyMismatch(actions[i]) {
				actions[i].Currency = ExpectedPaymentCurrency
				changed = true
			}
		}
		if !changed {
			continue
		}
		schedule := Schedule{
			Name: "prior-free hard negative", Program: program.Program,
			OrderID: fmt.Sprintf("order_prior_free_negative_%d", candidateIndex+1), Actions: actions,
		}
		result, err := Run(schedule)
		if err != nil {
			return nil, err
		}
		if resultHasInvariant(result, program.ExpectedInvariant) {
			return nil, fmt.Errorf("hard negative violates %s", program.ExpectedInvariant)
		}
		features := make([]string, 0, len(result.Trace))
		for _, entry := range result.Trace {
			features = append(features, observableStateFeature(entry.State, entry.EffectCount))
		}
		augmented = append(augmented, searchCandidate{
			actions: actions, features: features,
			terminal: fmt.Sprintf("%s|%d|%#v", result.FinalState, result.EffectCount, result.Violations),
			profile:  scoutProfile(actions),
		})
	}
	return augmented, nil
}

func priorFreeFoldModel(corpus ProgramCorpus, family string, models map[string]ScoutModel) (ScoutModel, error) {
	if model, exists := models[family]; exists {
		return model, nil
	}
	model, err := trainScoutWithPriors(corpus, family, false)
	if err == nil {
		models[family] = model
	}
	return model, err
}

func runScoutReport(corpus ProgramCorpus, budget int, closedLoop, fixedPriors bool) (ScoutReport, error) {
	if budget < 50 || budget > 1000 {
		return ScoutReport{}, fmt.Errorf("budget must be between 50 and 1000")
	}
	model, err := trainScoutWithPriors(corpus, "", fixedPriors)
	if err != nil {
		return ScoutReport{}, err
	}
	report := ScoutReport{Version: 1, Evaluation: "leave-one-family-out", Budget: budget, Model: model}
	method := ScoutMethod
	if closedLoop {
		report.Evaluation = "leave-one-family-out-closed-loop"
		method = ScoutClosedLoopMethod
	}
	if !fixedPriors {
		report.Evaluation = "leave-one-family-out-prior-free"
		method = ScoutPriorFreeMethod
	}
	modelJSON, err := json.Marshal(model)
	if err != nil {
		return ScoutReport{}, err
	}
	report.ModelBytes = len(modelJSON)
	foldModels := make(map[string]ScoutModel)
	for _, program := range corpus.Programs {
		rankingModel := model
		if program.ExpectedInvariant != "" {
			var exists bool
			rankingModel, exists = foldModels[program.Family]
			if !exists {
				rankingModel, err = trainScoutWithPriors(corpus, program.Family, fixedPriors)
				if err != nil {
					return ScoutReport{}, err
				}
				foldModels[program.Family] = rankingModel
				report.EvaluationFolds = append(report.EvaluationFolds, ScoutFold{HeldOutFamily: program.Family, Model: rankingModel})
			}
		}
		graph, err := CompileBehaviorGraph(program.Program, corpus.MaxScheduleActions)
		if err != nil {
			return ScoutReport{}, err
		}
		candidates := graphCandidates(graph)
		var run BaselineRun
		if closedLoop {
			run, err = evaluateClosedLoop(program, candidates, budget, rankingModel)
		} else {
			rankScoutCandidates(candidates, rankingModel)
			run, err = evaluateCandidates(method, program, candidates, budget)
		}
		if err != nil {
			return ScoutReport{}, err
		}
		report.Runs = append(report.Runs, run)
	}
	report.Summary = summarizeBaseline(method, corpus, report.Runs)
	return report, nil
}

func evaluateClosedLoop(program ProgramCase, candidates []searchCandidate, budget int, model ScoutModel) (BaselineRun, error) {
	return evaluateClosedLoopWith(program, candidates, budget, model, Run)
}

func evaluateClosedLoopWith(program ProgramCase, candidates []searchCandidate, budget int, model ScoutModel, execute func(Schedule) (Result, error)) (BaselineRun, error) {
	model.Weights = slices.Clone(model.Weights)
	remaining := slices.Clone(candidates)
	run := BaselineRun{Method: ScoutClosedLoopMethod, Program: program.Program}
	seenBehavior := make(map[string]bool)
	seenCoverage := make(map[string]bool)
	seenProfiles := make(map[string]bool)
	for len(remaining) != 0 && run.Executions < budget {
		rankClosedLoopCandidates(remaining, model, seenCoverage, seenProfiles)
		candidate := remaining[0]
		remaining = remaining[1:]
		seenProfiles[candidate.profile] = true
		run.Executions++
		if seenBehavior[candidate.terminal] {
			run.RedundantSchedules++
		}
		seenBehavior[candidate.terminal] = true
		schedule := Schedule{
			Name: "Scout closed-loop candidate", Program: program.Program,
			OrderID: fmt.Sprintf("order_scout_feedback_%d", run.Executions), Actions: candidate.actions,
		}
		result, err := execute(schedule)
		if err != nil {
			return BaselineRun{}, fmt.Errorf("execute Scout candidate %d: %w", run.Executions, err)
		}
		if len(result.Violations) != 0 {
			run.ReplayChecks++
			replay, err := execute(schedule)
			if err == nil && reflect.DeepEqual(result, replay) {
				run.DeterministicReplays++
				if resultHasInvariant(result, program.ExpectedInvariant) {
					run.Found = true
					run.FirstFindingExecution = run.Executions
					break
				}
				run.FalseFindings++
			}
		}
		newCoverage := recordTraceCoverage(seenCoverage, result)
		run.CoverageFeatures += newCoverage
		reward := -0.25
		if newCoverage != 0 {
			reward = 1
		}
		features := scoutFeatures(candidate.actions)
		for i := range model.Weights {
			model.Weights[i] += 0.01 * reward * features[i] / float64(len(candidate.actions))
		}
		run.FeedbackUpdates++
	}
	if run.Executions != 0 {
		run.RedundantScheduleRate = float64(run.RedundantSchedules) / float64(run.Executions)
	}
	return run, nil
}

func rankScoutCandidates(candidates []searchCandidate, model ScoutModel) {
	slices.SortStableFunc(candidates, func(a, b searchCandidate) int {
		return -compareFloat(model.score(a.actions), model.score(b.actions))
	})
}

func rankClosedLoopCandidates(candidates []searchCandidate, model ScoutModel, seenCoverage, seenProfiles map[string]bool) {
	slices.SortStableFunc(candidates, func(a, b searchCandidate) int {
		aSeen := seenProfiles[a.profile]
		bSeen := seenProfiles[b.profile]
		if aSeen != bSeen {
			if aSeen {
				return 1
			}
			return -1
		}
		aScore := model.score(a.actions) + coverageBonus(a, seenCoverage)
		bScore := model.score(b.actions) + coverageBonus(b, seenCoverage)
		return -compareFloat(aScore, bScore)
	})
}

func scoutProfile(actions []Action) string {
	return fmt.Sprint(scoutFeatures(actions))
}

func coverageBonus(candidate searchCandidate, seenCoverage map[string]bool) float64 {
	if len(seenCoverage) == 0 {
		return 0
	}
	newFeatures := make(map[string]bool)
	for _, feature := range candidate.features {
		if !seenCoverage[feature] {
			newFeatures[feature] = true
		}
	}
	return 0.5 * float64(len(newFeatures))
}

func recordTraceCoverage(seen map[string]bool, result Result) int {
	added := 0
	for _, entry := range result.Trace {
		feature := observableStateFeature(entry.State, entry.EffectCount)
		if !seen[feature] {
			seen[feature] = true
			added++
		}
	}
	return added
}

func (m ScoutModel) score(actions []Action) float64 {
	return scoutScore(m.Weights, scoutFeatures(actions))
}

func scoutScore(weights, features []float64) float64 {
	var score float64
	for i := range weights {
		score += weights[i] * features[i]
	}
	return score
}

func compareFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func scoutFeatures(actions []Action) []float64 {
	features := make([]float64, len(scoutFeatureNames))
	features[0] = float64(len(actions))
	seenEvents := make(map[string]bool)
	capturedSeen := false
	for i, action := range actions {
		switch action.Type {
		case "deliver":
			copies := action.Parallel
			if copies == 0 {
				copies = 1
			}
			features[1] += float64(copies)
			if action.Parallel != 0 {
				features[20]++
			}
			if HasAmountMismatch(action) {
				features[21]++
			}
			if action.PaymentOrderID != "" {
				features[22]++
			}
			if HasCurrencyMismatch(action) {
				features[23]++
			}
			switch action.Trust {
			case "missing-signature":
				features[13]++
			case "invalid-signature":
				features[14]++
			case "tampered-body":
				features[15]++
			}
			if seenEvents[action.EventID] {
				features[8] += float64(copies)
			} else {
				features[8] += float64(copies - 1)
			}
			seenEvents[action.EventID] = true
			if action.Status == "captured" {
				features[4] += float64(copies)
				capturedSeen = true
				if action.Trust != "" && action.Trust != "valid" {
					features[19] = 1
				}
			}
			if action.Status == "failed" {
				features[5]++
				if capturedSeen {
					features[11] = 1
				}
			}
			if action.CrashAt != "" {
				features[6]++
			}
		case "fulfill":
			features[2]++
			if action.Response != "ok" {
				features[7]++
			}
			switch action.Response {
			case "timeout":
				features[16]++
			case "connection-reset":
				features[17]++
			case "http-500":
				features[18]++
			}
		case "restart":
			features[3]++
		}
		if i == 0 {
			continue
		}
		previous := actions[i-1]
		if previous.Type == "restart" && action.Type == "deliver" {
			features[9] = 1
			if i > 1 && actions[i-2].EventID != "" && actions[i-2].EventID == action.EventID {
				features[12] = 1
			}
		}
		if previous.Type == "fulfill" && previous.Response != "ok" && action.Type == "fulfill" {
			features[10] = 1
		}
	}
	return features
}
