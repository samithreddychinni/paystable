package verification

import (
	"reflect"
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
	if len(first.EvaluationFolds) != 3 {
		t.Fatalf("Scout report has %d evaluation folds, want 3", len(first.EvaluationFolds))
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
