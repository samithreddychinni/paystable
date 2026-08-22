package verification

import (
	"reflect"
	"testing"
)

func TestIndependentReportUsesUnseenImplementations(t *testing.T) {
	corpus := GenerateProgramCorpus()
	first, err := RunIndependentReport(corpus, 50, 7)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RunIndependentReport(corpus, 50, 7)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("independent report is not deterministic")
	}
	if len(first.Cases) != 22 {
		t.Fatalf("independent report has %d cases, want 22", len(first.Cases))
	}
	if len(first.RandomSeeds) != 20 || len(first.Confidence) != 5 {
		t.Fatalf("independent report has %d seeds and %d confidence estimates", len(first.RandomSeeds), len(first.Confidence))
	}
	var scout BaselineSummary
	for _, summary := range first.Summary {
		if summary.FalseFindingCount != 0 || summary.DeterministicReplayRate != 1 {
			t.Fatalf("method %s failed its safety gate: %#v", summary.Method, summary)
		}
		if summary.Method == ScoutMethod {
			scout = summary
		}
	}
	if scout.SuccessAt10 != 1 || scout.MedianExecutionsBeforeFinding != 1 {
		t.Fatalf("Scout did not transfer to independent implementations: %#v", scout)
	}
	for _, summary := range first.Summary[:3] {
		worseMedian := scout.MedianExecutionsBeforeFinding > summary.MedianExecutionsBeforeFinding
		tiedWithoutHigherSuccess := scout.MedianExecutionsBeforeFinding == summary.MedianExecutionsBeforeFinding && scout.SuccessAt10 <= summary.SuccessAt10
		if worseMedian || tiedWithoutHigherSuccess {
			t.Fatalf("Scout did not beat %s: Scout=%#v baseline=%#v", summary.Method, scout, summary)
		}
	}
	for _, estimate := range first.Confidence {
		if estimate.Method == BaselineRandom && (estimate.Trials != 220 || estimate.FalseFindingCount != 0) {
			t.Fatalf("random confidence estimate is invalid: %#v", estimate)
		}
	}
}
