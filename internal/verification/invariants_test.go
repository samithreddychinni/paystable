package verification

import "testing"

func TestInvariantContractsHavePassingAndFailingFixtures(t *testing.T) {
	report, err := RunInvariantContractReport()
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed || report.Horizon != 2 || len(report.Contracts) != 5 {
		t.Fatalf("invariant report is incomplete: %#v", report)
	}
	for _, contract := range report.Contracts {
		if len(contract.PassingTrace) == 0 || len(contract.FailingTrace) == 0 || len(contract.PassingViolations) != 0 || len(contract.FailingViolations) == 0 {
			t.Fatalf("contract %s has incomplete fixtures", contract.Invariant)
		}
	}
}
