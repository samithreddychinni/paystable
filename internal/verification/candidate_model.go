package verification

import "fmt"

type CandidateModelResult struct {
	Name             string          `json:"name"`
	Trained          bool            `json:"trained"`
	FixedPriors      bool            `json:"fixed_priors"`
	PairwiseAccuracy float64         `json:"pairwise_accuracy"`
	Summary          BaselineSummary `json:"summary"`
}

type CandidateModelReport struct {
	Version          int                    `json:"version"`
	TrainingSet      string                 `json:"training_set"`
	ValidationSet    string                 `json:"validation_set"`
	SplitUnit        string                 `json:"split_unit"`
	FrozenHeldOutUse bool                   `json:"frozen_held_out_used"`
	Constraint       string                 `json:"constraint"`
	PromotionRule    string                 `json:"promotion_rule"`
	Budget           int                    `json:"budget"`
	Candidate        ScoutModel             `json:"candidate"`
	Results          []CandidateModelResult `json:"results"`
	Promoted         bool                   `json:"promoted"`
	Decision         string                 `json:"decision"`
	Passed           bool                   `json:"passed"`
}

// RunCandidateModelReport validates a monotonic model without held-out data.
func RunCandidateModelReport(budget int) (CandidateModelReport, error) {
	if budget < 50 || budget > 1000 {
		return CandidateModelReport{}, fmt.Errorf("budget must be between 50 and 1000")
	}
	corpus := GenerateProgramCorpus()
	standard, err := trainScoutWithPriors(corpus, "", true)
	if err != nil {
		return CandidateModelReport{}, err
	}
	candidate, err := trainMonotonicScout(corpus)
	if err != nil {
		return CandidateModelReport{}, err
	}
	models := []struct {
		name        string
		trained     bool
		fixedPriors bool
		model       ScoutModel
	}{
		{name: "monotonic-trained", trained: true, model: candidate},
		{name: "batch-trained", trained: true, fixedPriors: true, model: standard},
		{name: "fixed-priors-only", fixedPriors: true, model: newScoutModel(true)},
		{name: "zero-weights", model: newScoutModel(false)},
	}
	report := CandidateModelReport{
		Version: 1, TrainingSet: "14 vulnerable synthetic programs",
		ValidationSet: "24 known-family transfer implementations", SplitUnit: "complete implementation",
		FrozenHeldOutUse: false, Constraint: "all learned feature weights are nonnegative", Budget: budget,
		PromotionRule: "candidate must improve MRR and pairwise accuracy over batch training and fixed priors",
		Candidate:     candidate,
	}
	for _, model := range models {
		validationCases := independentCases()
		summary, err := evaluateImplementationModel(model.name, model.model, budget, validationCases, false)
		if err != nil {
			return CandidateModelReport{}, err
		}
		accuracy, err := implementationPairwiseAccuracy(model.model, validationCases)
		if err != nil {
			return CandidateModelReport{}, err
		}
		if summary.FalseFindingCount != 0 || summary.DeterministicReplayRate != 1 {
			return CandidateModelReport{}, fmt.Errorf("candidate comparison %q failed its evidence gate", model.name)
		}
		report.Results = append(report.Results, CandidateModelResult{
			Name: model.name, Trained: model.trained, FixedPriors: model.fixedPriors,
			PairwiseAccuracy: accuracy, Summary: summary,
		})
	}
	candidateResult, batchResult, priorResult := report.Results[0], report.Results[1], report.Results[2]
	report.Promoted = candidateResult.Summary.MeanReciprocalRank > batchResult.Summary.MeanReciprocalRank &&
		candidateResult.Summary.MeanReciprocalRank > priorResult.Summary.MeanReciprocalRank &&
		candidateResult.PairwiseAccuracy > batchResult.PairwiseAccuracy && candidateResult.PairwiseAccuracy > priorResult.PairwiseAccuracy
	if report.Promoted {
		report.Decision = "promote after a new untouched held-out evaluation"
	} else {
		report.Decision = "reject because the candidate does not improve the validation metrics"
	}
	report.Passed = true
	return report, nil
}

func implementationPairwiseAccuracy(model ScoutModel, cases []independentCase) (float64, error) {
	var correct, pairs float64
	for _, testCase := range cases {
		if testCase.program.ExpectedInvariant == "" {
			continue
		}
		candidates, err := enumerateIndependentCandidates(testCase, 4)
		if err != nil {
			return 0, err
		}
		var positive, negative []float64
		for i, candidate := range candidates {
			result, err := testCase.execute(Schedule{
				Name: "candidate validation", Program: testCase.program.Program,
				OrderID: fmt.Sprintf("order_candidate_%d", i+1), Actions: candidate.actions,
			})
			if err != nil {
				return 0, err
			}
			if resultHasInvariant(result, testCase.program.ExpectedInvariant) {
				positive = append(positive, model.score(candidate.actions))
			} else {
				negative = append(negative, model.score(candidate.actions))
			}
		}
		for _, positiveScore := range positive {
			for _, negativeScore := range negative {
				pairs++
				if positiveScore > negativeScore {
					correct++
				} else if positiveScore == negativeScore {
					correct += 0.5
				}
			}
		}
	}
	if pairs == 0 {
		return 0, fmt.Errorf("candidate validation has no labeled pairs")
	}
	return correct / pairs, nil
}

func trainMonotonicScout(corpus ProgramCorpus) (ScoutModel, error) {
	model, err := trainScoutWithPriors(corpus, "", false)
	if err != nil {
		return ScoutModel{}, err
	}
	model.Version = 4
	for i, weight := range model.Weights {
		if weight < 0 {
			model.Weights[i] = 0
		}
	}
	return model, nil
}
