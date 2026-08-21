package verification

import (
	"fmt"
	"math/rand"
	"reflect"
	"slices"
)

const (
	BaselineBounded  = "bounded"
	BaselineRandom   = "random"
	BaselineCoverage = "coverage"
)

type BaselineReport struct {
	Version int               `json:"version"`
	Seed    int64             `json:"seed"`
	Budget  int               `json:"budget"`
	Runs    []BaselineRun     `json:"runs"`
	Summary []BaselineSummary `json:"summary"`
}

type BaselineRun struct {
	Method                string  `json:"method"`
	Program               string  `json:"program"`
	Executions            int     `json:"executions"`
	Found                 bool    `json:"found"`
	FirstFindingExecution int     `json:"first_finding_execution,omitempty"`
	ReplayChecks          int     `json:"replay_checks"`
	DeterministicReplays  int     `json:"deterministic_replays"`
	FalseFindings         int     `json:"false_findings"`
	RedundantSchedules    int     `json:"redundant_schedules"`
	RedundantScheduleRate float64 `json:"redundant_schedule_rate"`
	FeedbackUpdates       int     `json:"feedback_updates,omitempty"`
	CoverageFeatures      int     `json:"coverage_features,omitempty"`
}

type BaselineSummary struct {
	Method                        string  `json:"method"`
	SuccessAt10                   float64 `json:"success_at_10"`
	SuccessAt25                   float64 `json:"success_at_25"`
	SuccessAt50                   float64 `json:"success_at_50"`
	MedianExecutionsBeforeFinding int     `json:"median_executions_before_finding"`
	FalseFindingCount             int     `json:"false_finding_count"`
	DeterministicReplayRate       float64 `json:"deterministic_replay_rate"`
	RedundantScheduleRate         float64 `json:"redundant_schedule_rate"`
}

type searchCandidate struct {
	actions  []Action
	features []string
	terminal string
}

// RunBaselineReport evaluates all non-model search methods with one execution budget.
func RunBaselineReport(corpus ProgramCorpus, budget int, seed int64) (BaselineReport, error) {
	if budget < 50 || budget > 1000 {
		return BaselineReport{}, fmt.Errorf("budget must be between 50 and 1000")
	}
	report := BaselineReport{Version: 1, Seed: seed, Budget: budget}
	methods := []string{BaselineBounded, BaselineRandom, BaselineCoverage}
	for _, method := range methods {
		for i, program := range corpus.Programs {
			run, err := runBaseline(method, program, corpus.MaxScheduleActions, budget, seed+int64(i))
			if err != nil {
				return BaselineReport{}, err
			}
			report.Runs = append(report.Runs, run)
		}
		report.Summary = append(report.Summary, summarizeBaseline(method, corpus, report.Runs))
	}
	return report, nil
}

func runBaseline(method string, program ProgramCase, maxActions, budget int, seed int64) (BaselineRun, error) {
	graph, err := CompileBehaviorGraph(program.Program, maxActions)
	if err != nil {
		return BaselineRun{}, err
	}
	candidates := graphCandidates(graph)
	switch method {
	case BaselineBounded:
	case BaselineRandom:
		rand.New(rand.NewSource(seed)).Shuffle(len(candidates), func(i, j int) {
			candidates[i], candidates[j] = candidates[j], candidates[i]
		})
	case BaselineCoverage:
		candidates = coverageOrder(candidates)
	default:
		return BaselineRun{}, fmt.Errorf("unsupported baseline %q", method)
	}
	return evaluateCandidates(method, program, candidates, budget)
}

func evaluateCandidates(method string, program ProgramCase, candidates []searchCandidate, budget int) (BaselineRun, error) {
	run := BaselineRun{Method: method, Program: program.Program}
	seenBehavior := make(map[string]bool)
	for _, candidate := range candidates {
		if run.Executions == budget {
			break
		}
		run.Executions++
		if seenBehavior[candidate.terminal] {
			run.RedundantSchedules++
		}
		seenBehavior[candidate.terminal] = true
		schedule := Schedule{
			Name: method + " search candidate", Program: program.Program,
			OrderID: fmt.Sprintf("order_search_%d", run.Executions), Actions: candidate.actions,
		}
		result, err := Run(schedule)
		if err != nil {
			return BaselineRun{}, fmt.Errorf("execute %s candidate %d: %w", method, run.Executions, err)
		}
		if len(result.Violations) == 0 {
			continue
		}
		run.ReplayChecks++
		replay, err := Run(schedule)
		if err != nil || !reflect.DeepEqual(result, replay) {
			continue
		}
		run.DeterministicReplays++
		if resultHasInvariant(result, program.ExpectedInvariant) {
			run.Found = true
			run.FirstFindingExecution = run.Executions
			break
		}
		run.FalseFindings++
	}
	if run.Executions != 0 {
		run.RedundantScheduleRate = float64(run.RedundantSchedules) / float64(run.Executions)
	}
	return run, nil
}

func graphCandidates(graph BehaviorGraph) []searchCandidate {
	type path struct {
		node     int
		actions  []Action
		features []string
	}
	outgoing := make([][]BehaviorEdge, len(graph.Nodes))
	for _, edge := range graph.Edges {
		outgoing[edge.From] = append(outgoing[edge.From], edge)
	}
	frontier := []path{{node: 0}}
	var candidates []searchCandidate
	for depth := 0; depth < graph.MaxActions; depth++ {
		var next []path
		for _, current := range frontier {
			for _, edge := range outgoing[current.node] {
				actions := append(slices.Clone(current.actions), edge.Action)
				state := graph.Nodes[edge.To].State
				feature := observableStateFeature(state.PaymentState, state.EffectCount)
				features := append(slices.Clone(current.features), feature)
				next = append(next, path{node: edge.To, actions: actions, features: features})
				if graph.Nodes[edge.To].State.Running {
					terminal := fmt.Sprintf("%#v", state)
					candidates = append(candidates, searchCandidate{actions: actions, features: features, terminal: terminal})
				}
			}
		}
		frontier = next
	}
	return candidates
}

func observableStateFeature(state string, effectCount int) string {
	return fmt.Sprintf("%s|%d", state, effectCount)
}

// This quadratic ordering supports short laboratory schedules. Use a priority queue if the corpus grows.
func coverageOrder(candidates []searchCandidate) []searchCandidate {
	remaining := slices.Clone(candidates)
	ordered := make([]searchCandidate, 0, len(candidates))
	seen := make(map[string]bool)
	for len(remaining) != 0 {
		minActions := len(remaining[0].actions)
		for _, candidate := range remaining[1:] {
			if len(candidate.actions) < minActions {
				minActions = len(candidate.actions)
			}
		}
		best, bestScore := 0, -1
		for i, candidate := range remaining {
			if len(candidate.actions) != minActions {
				continue
			}
			score := 0
			candidateFeatures := make(map[string]bool)
			for _, feature := range candidate.features {
				if !seen[feature] && !candidateFeatures[feature] {
					score++
					candidateFeatures[feature] = true
				}
			}
			if score > bestScore {
				best, bestScore = i, score
			}
		}
		selected := remaining[best]
		ordered = append(ordered, selected)
		for _, feature := range selected.features {
			seen[feature] = true
		}
		remaining = append(remaining[:best], remaining[best+1:]...)
	}
	return ordered
}

func resultHasInvariant(result Result, invariant string) bool {
	if invariant == "" {
		return false
	}
	for _, violation := range result.Violations {
		if violation.Invariant == invariant {
			return true
		}
	}
	return false
}

func summarizeBaseline(method string, corpus ProgramCorpus, runs []BaselineRun) BaselineSummary {
	summary := BaselineSummary{Method: method}
	var vulnerable, executions, redundant, replayChecks, deterministic int
	var findings []int
	for _, program := range corpus.Programs {
		var run BaselineRun
		for _, candidate := range runs {
			if candidate.Method == method && candidate.Program == program.Program {
				run = candidate
				break
			}
		}
		executions += run.Executions
		redundant += run.RedundantSchedules
		replayChecks += run.ReplayChecks
		deterministic += run.DeterministicReplays
		summary.FalseFindingCount += run.FalseFindings
		if program.ExpectedInvariant == "" {
			continue
		}
		vulnerable++
		if run.Found {
			findings = append(findings, run.FirstFindingExecution)
			if run.FirstFindingExecution <= 10 {
				summary.SuccessAt10++
			}
			if run.FirstFindingExecution <= 25 {
				summary.SuccessAt25++
			}
			if run.FirstFindingExecution <= 50 {
				summary.SuccessAt50++
			}
		}
	}
	if vulnerable != 0 {
		summary.SuccessAt10 /= float64(vulnerable)
		summary.SuccessAt25 /= float64(vulnerable)
		summary.SuccessAt50 /= float64(vulnerable)
	}
	if len(findings) != 0 {
		slices.Sort(findings)
		summary.MedianExecutionsBeforeFinding = findings[len(findings)/2]
	}
	if executions != 0 {
		summary.RedundantScheduleRate = float64(redundant) / float64(executions)
	}
	if replayChecks != 0 {
		summary.DeterministicReplayRate = float64(deterministic) / float64(replayChecks)
	}
	return summary
}
