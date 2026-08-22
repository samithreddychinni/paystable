package verification

import (
	"reflect"
	"slices"
	"testing"
)

func TestScoutIsDeterministicAndBeatsBaselineMedian(t *testing.T) {
	corpus := GenerateProgramCorpus()
	first, err := RunScoutReport(corpus, 50)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RunScoutReport(corpus, 50)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("Scout report is not deterministic")
	}
	if first.Summary.SuccessAt50 != 1 || first.Summary.FalseFindingCount != 0 || first.Summary.DeterministicReplayRate != 1 {
		t.Fatalf("Scout did not pass: %#v", first.Summary)
	}
	families := make(map[string]bool)
	for _, program := range corpus.Programs {
		if program.ExpectedInvariant != "" {
			families[program.Family] = true
		}
	}
	if len(first.EvaluationFolds) != len(families) {
		t.Fatalf("Scout report has %d evaluation folds, want %d", len(first.EvaluationFolds), len(families))
	}
	baselines, err := RunBaselineReport(corpus, 50, 7)
	if err != nil {
		t.Fatal(err)
	}
	for _, baseline := range baselines.Summary {
		if first.Summary.MedianExecutionsBeforeFinding >= baseline.MedianExecutionsBeforeFinding {
			t.Fatalf("Scout median %d did not beat %s median %d", first.Summary.MedianExecutionsBeforeFinding, baseline.Method, baseline.MedianExecutionsBeforeFinding)
		}
	}
}

func TestPriorFreeScoutReportDoesNotUseTheHeldOutSignalPrior(t *testing.T) {
	corpus := priorFreeTestCorpus()
	first, err := RunPriorFreeScoutReport(corpus, 50)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RunPriorFreeScoutReport(corpus, 50)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("prior-free Scout report is not deterministic")
	}
	if first.Evaluation != "leave-one-family-out-prior-free" || first.Summary.FalseFindingCount != 0 {
		t.Fatalf("prior-free Scout report is invalid: %#v", first.Summary)
	}
	for _, fold := range first.EvaluationFolds {
		if fold.HeldOutFamily != "payment-currency" {
			continue
		}
		index := slices.Index(fold.Model.FeatureNames, "currency_mismatch_count")
		if index < 0 || fold.Model.Weights[index] != 0 {
			t.Fatalf("held-out currency weight is not zero: %#v", fold.Model)
		}
		return
	}
	t.Fatal("prior-free Scout report has no payment-currency fold")
}

func TestPriorFreeStressReportIsDeterministic(t *testing.T) {
	corpus := priorFreeTestCorpus()
	first, err := RunPriorFreeStressReport(corpus, 50, 7)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RunPriorFreeStressReport(corpus, 50, 7)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("prior-free stress report is not deterministic")
	}
	if len(first.RandomSeeds) != 20 || first.Confidence.Trials != 40 {
		t.Fatalf("prior-free stress report has %d seeds and %d trials", len(first.RandomSeeds), first.Confidence.Trials)
	}
	if first.Confidence.FalseFindingCount != 0 {
		t.Fatalf("prior-free stress report is invalid: %#v", first.Confidence)
	}
	if first.AmbiguousScoreGroups == 0 || len(first.AmbiguousFamilies) == 0 {
		t.Fatal("prior-free stress report found no ambiguous model scores")
	}
	if first.AdversarialSummary.FalseFindingCount != 0 || first.AdversarialSummary.DeterministicReplayRate != 1 {
		t.Fatalf("adversarial stress report is invalid: %#v", first.AdversarialSummary)
	}
}

func TestExternalTransferRanksEveryMismatchAboveItsControl(t *testing.T) {
	report, err := RunExternalTransferReport(priorFreeTransferTestCorpus())
	if err != nil {
		t.Fatal(err)
	}
	if report.FixedPriors || report.PairwiseWins != report.PairCount || report.SafeControlsAboveFailure != 0 {
		t.Fatalf("external transfer report failed: %#v", report)
	}
}

func TestReplayWindowChallengeExposesScoutBlindSpot(t *testing.T) {
	report, err := RunReplayWindowReport(GenerateProgramCorpus())
	if err != nil {
		t.Fatal(err)
	}
	if report.FailureInvariant != InvariantFulfillmentAtMostOnce || !report.DeterministicReplay || report.CorrectControlViolations != 0 || report.ReducedActions != report.OriginalActions {
		t.Fatalf("replay-window result is invalid: %#v", report)
	}
	if !report.ScoresTied || !report.SameFeatureProfile {
		t.Fatalf("Scout unexpectedly distinguished the unseen replay window: %#v", report)
	}
	graph, err := CompileBehaviorGraph(ProgramExpiringEventClaim, 3)
	if err != nil {
		t.Fatal(err)
	}
	path := []Action{
		{Type: "deliver", EventID: "event_replay", Status: "captured"},
		{Type: "advance", AdvanceSeconds: EventClaimRetentionSeconds + 1},
		{Type: "deliver", EventID: "event_replay", Status: "captured"},
	}
	if !graphContainsPath(graph, path) {
		t.Fatal("replay-window graph does not contain the failure path")
	}
}

func TestReplayScoutV3RanksHeldOutDelays(t *testing.T) {
	invalid := replayWindowSchedule("invalid clock skew", ProgramExpiringEventClaim, 3600, MaxClockSkewSeconds+1, false)
	if Validate(invalid) == nil {
		t.Fatal("clock skew above the limit passed validation")
	}
	first, err := RunReplayV3Report(GenerateProgramCorpus())
	if err != nil {
		t.Fatal(err)
	}
	second, err := RunReplayV3Report(GenerateProgramCorpus())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("Scout v3 replay report is not deterministic")
	}
	if first.PairwiseWins != first.PairCount || first.BaseScoreTies != first.PairCount || first.Model.Version != 3 {
		t.Fatalf("Scout v3 did not improve replay ranking: %#v", first)
	}
	if first.Model.Weights[len(first.Model.Weights)-1] <= 0 {
		t.Fatal("Scout v3 did not learn a replay-delay weight")
	}
	for _, test := range first.Cases {
		for _, delay := range append(slices.Clone(first.TrainingSafeDelays), first.TrainingFailureDelays...) {
			if test.SafeDelaySeconds == delay || test.FailureDelaySeconds == delay {
				t.Fatalf("evaluation delay %d appears in training", delay)
			}
		}
		if !test.DeterministicReplay || test.CorrectControlViolations != 0 || test.ReducedActions != 3 {
			t.Fatalf("Scout v3 case is invalid: %#v", test)
		}
	}
}

func priorFreeTestCorpus() ProgramCorpus {
	full := GenerateProgramCorpus()
	corpus := ProgramCorpus{Version: full.Version, MaxScheduleActions: full.MaxScheduleActions}
	for _, program := range full.Programs {
		switch program.Family {
		case "payment-amount", "correct-amount", "payment-currency", "correct-currency":
			corpus.Programs = append(corpus.Programs, program)
		}
	}
	return corpus
}

func priorFreeTransferTestCorpus() ProgramCorpus {
	full := GenerateProgramCorpus()
	corpus := ProgramCorpus{Version: full.Version, MaxScheduleActions: full.MaxScheduleActions}
	for _, program := range full.Programs {
		switch program.Family {
		case "payment-amount", "correct-amount", "payment-order", "correct-order", "payment-currency", "correct-currency":
			corpus.Programs = append(corpus.Programs, program)
		}
	}
	return corpus
}
