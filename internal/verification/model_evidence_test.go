package verification

import "testing"

func TestModelEvidenceIncludesFrozenAblations(t *testing.T) {
	report, err := RunModelEvidenceReport(50)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed || report.ParameterCount != 24 || report.ArtifactBytes == 0 || report.ValidationSet == "" || len(report.Ablations) != 4 {
		t.Fatalf("model evidence is incomplete: %#v", report)
	}
	for _, ablation := range report.Ablations {
		if ablation.Summary.FalseFindingCount != 0 || ablation.Summary.DeterministicReplayRate != 1 {
			t.Fatalf("ablation %s failed its evidence gate: %#v", ablation.Name, ablation.Summary)
		}
	}
}
