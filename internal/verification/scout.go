package verification

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
)

const (
	ScoutMethod           = "scout"
	ScoutClosedLoopMethod = "scout-closed-loop"
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

// TrainScout fits a linear pairwise ranker to deterministic invariant results.
func TrainScout(corpus ProgramCorpus) (ScoutModel, error) {
	return trainScout(corpus, "")
}

func trainScout(corpus ProgramCorpus, excludedFamily string) (ScoutModel, error) {
	model := ScoutModel{
		Version: 1, FeatureNames: slices.Clone(scoutFeatureNames),
		Weights: make([]float64, len(scoutFeatureNames)), Epochs: 40, LearningRate: 0.01,
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
	return runScoutReport(corpus, budget, false)
}

// RunClosedLoopReport updates Scout after each runtime result.
func RunClosedLoopReport(corpus ProgramCorpus, budget int) (ScoutReport, error) {
	return runScoutReport(corpus, budget, true)
}

func runScoutReport(corpus ProgramCorpus, budget int, closedLoop bool) (ScoutReport, error) {
	if budget < 50 || budget > 1000 {
		return ScoutReport{}, fmt.Errorf("budget must be between 50 and 1000")
	}
	model, err := TrainScout(corpus)
	if err != nil {
		return ScoutReport{}, err
	}
	report := ScoutReport{Version: 1, Evaluation: "leave-one-family-out", Budget: budget, Model: model}
	method := ScoutMethod
	if closedLoop {
		report.Evaluation = "leave-one-family-out-closed-loop"
		method = ScoutClosedLoopMethod
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
				rankingModel, err = trainScout(corpus, program.Family)
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
	for len(remaining) != 0 && run.Executions < budget {
		rankClosedLoopCandidates(remaining, model, seenCoverage)
		candidate := remaining[0]
		remaining = remaining[1:]
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

func rankClosedLoopCandidates(candidates []searchCandidate, model ScoutModel, seenCoverage map[string]bool) {
	slices.SortStableFunc(candidates, func(a, b searchCandidate) int {
		aScore := model.score(a.actions) + coverageBonus(a, seenCoverage)
		bScore := model.score(b.actions) + coverageBonus(b, seenCoverage)
		return -compareFloat(aScore, bScore)
	})
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
			features[1]++
			switch action.Trust {
			case "missing-signature":
				features[13]++
			case "invalid-signature":
				features[14]++
			case "tampered-body":
				features[15]++
			}
			if seenEvents[action.EventID] {
				features[8]++
			}
			seenEvents[action.EventID] = true
			if action.Status == "captured" {
				features[4]++
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
