package verification

import (
	"encoding/json"
	"fmt"
	"slices"
)

type ModelAblation struct {
	Name              string          `json:"name"`
	Trained           bool            `json:"trained"`
	FixedPriors       bool            `json:"fixed_priors"`
	ModelBytes        int             `json:"model_bytes"`
	Summary           BaselineSummary `json:"summary"`
	ClosedLoopSummary BaselineSummary `json:"closed_loop_summary"`
}

type ModelEvidenceReport struct {
	Version        int             `json:"version"`
	ArtifactFormat string          `json:"artifact_format"`
	ArtifactBytes  int             `json:"artifact_bytes"`
	ParameterCount int             `json:"parameter_count"`
	TrainingSeed   string          `json:"training_seed"`
	TrainingSet    string          `json:"training_set"`
	ValidationSet  string          `json:"validation_set"`
	RegressionSet  string          `json:"regression_set"`
	EvaluationSet  string          `json:"evaluation_set"`
	SplitUnit      string          `json:"split_unit"`
	Objective      string          `json:"objective"`
	LabelAuthority string          `json:"label_authority"`
	FrozenCommit   string          `json:"frozen_commit"`
	Budget         int             `json:"budget"`
	Model          ScoutModel      `json:"model"`
	Ablations      []ModelAblation `json:"ablations"`
	Passed         bool            `json:"passed"`
}

// RunModelEvidenceReport records Scout training and held-out ablations.
func RunModelEvidenceReport(budget int) (ModelEvidenceReport, error) {
	if budget < 50 || budget > 1000 {
		return ModelEvidenceReport{}, fmt.Errorf("budget must be between 50 and 1000")
	}
	corpus := GenerateProgramCorpus()
	trained, err := trainScoutWithPriors(corpus, "", true)
	if err != nil {
		return ModelEvidenceReport{}, err
	}
	priorFree, err := trainScoutWithPriors(corpus, "", false)
	if err != nil {
		return ModelEvidenceReport{}, err
	}
	models := []struct {
		name        string
		trained     bool
		fixedPriors bool
		model       ScoutModel
	}{
		{name: "trained-with-fixed-priors", trained: true, fixedPriors: true, model: trained},
		{name: "fixed-priors-only", fixedPriors: true, model: newScoutModel(true)},
		{name: "trained-without-priors", trained: true, model: priorFree},
		{name: "zero-weights", model: newScoutModel(false)},
	}
	artifact, err := json.Marshal(trained)
	if err != nil {
		return ModelEvidenceReport{}, err
	}
	report := ModelEvidenceReport{
		Version: 2, ArtifactFormat: "compact JSON", ArtifactBytes: len(artifact),
		ParameterCount: len(trained.Weights), TrainingSeed: "none because training order is deterministic",
		TrainingSet: "14 vulnerable synthetic programs", ValidationSet: "none because the frozen model has no selection step",
		RegressionSet: "14 vulnerable programs and 11 correct controls", EvaluationSet: "8 frozen held-out merchant implementations",
		SplitUnit: "complete merchant implementation", Objective: "pairwise hinge ranking with margin 1",
		LabelAuthority: "deterministic invariant result", FrozenCommit: "b245a558166accacfa13039ffeff2ce0425f5a24",
		Budget: budget, Model: trained,
	}
	for _, candidate := range models {
		modelJSON, err := json.Marshal(candidate.model)
		if err != nil {
			return ModelEvidenceReport{}, err
		}
		summary, err := evaluateHeldOutModel(candidate.name, candidate.model, budget, false)
		if err != nil {
			return ModelEvidenceReport{}, err
		}
		closed, err := evaluateHeldOutModel(candidate.name+"-closed-loop", candidate.model, budget, true)
		if err != nil {
			return ModelEvidenceReport{}, err
		}
		if summary.FalseFindingCount != 0 || summary.DeterministicReplayRate != 1 || closed.FalseFindingCount != 0 || closed.DeterministicReplayRate != 1 {
			return ModelEvidenceReport{}, fmt.Errorf("ablation %q failed its evidence gate", candidate.name)
		}
		report.Ablations = append(report.Ablations, ModelAblation{
			Name: candidate.name, Trained: candidate.trained, FixedPriors: candidate.fixedPriors,
			ModelBytes: len(modelJSON), Summary: summary, ClosedLoopSummary: closed,
		})
	}
	report.Passed = true
	return report, nil
}

func evaluateHeldOutModel(method string, model ScoutModel, budget int, closedLoop bool) (BaselineSummary, error) {
	corpus := ProgramCorpus{Version: 1, MaxScheduleActions: 4}
	var runs []BaselineRun
	for _, testCase := range heldoutCases() {
		candidates, err := enumerateIndependentCandidates(testCase, corpus.MaxScheduleActions)
		if err != nil {
			return BaselineSummary{}, err
		}
		var run BaselineRun
		if closedLoop {
			run, err = evaluateClosedLoopWith(testCase.program, candidates, budget, model, testCase.execute)
		} else {
			rankScoutCandidates(candidates, model)
			run, err = evaluateCandidatesWith(method, testCase.program, slices.Clone(candidates), budget, testCase.execute)
		}
		if err != nil {
			return BaselineSummary{}, err
		}
		run.Method = method
		corpus.Programs = append(corpus.Programs, testCase.program)
		runs = append(runs, run)
	}
	return summarizeBaseline(method, corpus, runs, budget), nil
}
