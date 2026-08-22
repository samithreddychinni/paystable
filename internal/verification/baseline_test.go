package verification

import (
	"reflect"
	"testing"
)

func TestBaselineReportIsDeterministicAndFindsRequiredBugs(t *testing.T) {
	corpus := GenerateProgramCorpus()
	if _, err := RunBaselineReport(corpus, 49, 7); err == nil {
		t.Fatal("baseline report accepted a budget below 50")
	}
	first, err := RunBaselineReport(corpus, 50, 7)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RunBaselineReport(corpus, 50, 7)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("baseline report is not deterministic")
	}
	if len(first.Runs) != len(corpus.Programs)*3 || len(first.Summary) != 3 {
		t.Fatalf("report has %d runs and %d summaries", len(first.Runs), len(first.Summary))
	}
	for _, summary := range first.Summary {
		if summary.SuccessAt50 != 1 || summary.FalseFindingCount != 0 || summary.DeterministicReplayRate != 1 {
			t.Fatalf("baseline %s did not pass: %#v", summary.Method, summary)
		}
	}
}
