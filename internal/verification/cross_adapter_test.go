package verification

import "testing"

func TestCrossAdapterControl(t *testing.T) {
	report, err := RunCrossAdapterReport()
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed || len(report.Cases) != 3 {
		t.Fatalf("cross-adapter report is incomplete: %#v", report)
	}
	for _, test := range report.Cases {
		if !test.ReplayVerified || len(test.NormalizedActions) == 0 {
			t.Fatalf("case %q is incomplete", test.Name)
		}
	}
}
