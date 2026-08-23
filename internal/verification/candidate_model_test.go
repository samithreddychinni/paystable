package verification

import "testing"

func TestCandidateModelUsesValidationOnly(t *testing.T) {
	report, err := RunCandidateModelReport(50)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed || report.Promoted || report.FrozenHeldOutUse || len(report.Results) != 4 {
		t.Fatalf("candidate model report is incomplete: %#v", report)
	}
	for _, weight := range report.Candidate.Weights {
		if weight < 0 {
			t.Fatalf("candidate has a negative weight: %f", weight)
		}
	}
}
