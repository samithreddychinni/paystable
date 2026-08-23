package verification

import "testing"

func TestHeldOutCasesMatchTheirDeclaredInvariant(t *testing.T) {
	for _, testCase := range heldoutCases() {
		var found bool
		candidates, err := enumerateIndependentCandidates(testCase, 4)
		if err != nil {
			t.Fatal(err)
		}
		for i, candidate := range candidates {
			result, err := testCase.execute(Schedule{
				Name: "held-out semantic check", Program: testCase.program.Program,
				OrderID: "order_heldout_check", Actions: candidate.actions,
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Violations) != 0 && testCase.program.ExpectedInvariant == "" {
				t.Fatalf("control %s produced a finding at candidate %d", testCase.program.Program, i+1)
			}
			found = found || resultHasInvariant(result, testCase.program.ExpectedInvariant)
		}
		if testCase.program.ExpectedInvariant != "" && !found {
			t.Fatalf("merchant %s did not produce %s", testCase.program.Program, testCase.program.ExpectedInvariant)
		}
	}
}
