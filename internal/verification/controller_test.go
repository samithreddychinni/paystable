package verification

import (
	"reflect"
	"testing"
)

func TestClosedLoopScoutIsDeterministic(t *testing.T) {
	corpus := GenerateProgramCorpus()
	first, err := RunClosedLoopReport(corpus, 50)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RunClosedLoopReport(corpus, 50)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("closed-loop report is not deterministic")
	}
	if first.Summary.SuccessAt10 != 1 || first.Summary.FalseFindingCount != 0 || first.Summary.DeterministicReplayRate != 1 {
		t.Fatalf("closed-loop Scout did not pass: %#v", first.Summary)
	}
	feedbackUpdates, closedLoopExecutions := 0, 0
	vulnerable := make(map[string]bool)
	for _, program := range corpus.Programs {
		vulnerable[program.Program] = program.ExpectedInvariant != ""
	}
	for _, run := range first.Runs {
		feedbackUpdates += run.FeedbackUpdates
		if vulnerable[run.Program] {
			closedLoopExecutions += run.Executions
		}
	}
	if feedbackUpdates == 0 {
		t.Fatal("closed-loop Scout did not use runtime feedback")
	}
	static, err := RunScoutReport(corpus, 50)
	if err != nil {
		t.Fatal(err)
	}
	staticExecutions := 0
	for _, run := range static.Runs {
		if vulnerable[run.Program] {
			staticExecutions += run.Executions
		}
	}
	if closedLoopExecutions > staticExecutions {
		t.Fatalf("closed loop used %d executions, static Scout used %d", closedLoopExecutions, staticExecutions)
	}
}

func TestClosedLoopScoutPrefersAnUnseenInputProfile(t *testing.T) {
	seen := Action{Type: "deliver", EventID: "captured", Status: "captured"}
	unseen := Action{Type: "deliver", EventID: "failed", Status: "failed"}
	candidates := []searchCandidate{
		{actions: []Action{seen}, profile: scoutProfile([]Action{seen})},
		{actions: []Action{unseen}, profile: scoutProfile([]Action{unseen})},
	}
	rankClosedLoopCandidates(candidates, ScoutModel{Weights: make([]float64, len(scoutFeatureNames))}, nil, map[string]bool{
		scoutProfile([]Action{seen}): true,
	})
	if candidates[0].actions[0] != unseen {
		t.Fatal("closed-loop Scout did not prefer the unseen input profile")
	}
}
