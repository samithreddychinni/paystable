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

func TestHeldOutReportMatchesTheFrozenProtocol(t *testing.T) {
	report, err := RunHeldOutReport(GenerateProgramCorpus(), 50, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Cases) != 8 || len(report.RandomSeeds) != 100 || report.MissRank != 51 {
		t.Fatalf("held-out protocol changed: %#v", report)
	}
	summaries := make(map[string]BaselineSummary)
	for _, summary := range report.Summary {
		if summary.FalseFindingCount != 0 || summary.DeterministicReplayRate != 1 {
			t.Fatalf("method %s failed its evidence gate: %#v", summary.Method, summary)
		}
		summaries[summary.Method] = summary
	}
	random, scout, closed := summaries[BaselineRandom], summaries[ScoutMethod], summaries[ScoutClosedLoopMethod]
	if scout.SuccessAt10 != 0.75 || scout.SuccessAt25 != 1 || scout.MedianExecutionsBeforeFinding != 2.5 ||
		random.SuccessAt10 != 0.82 || random.MedianExecutionsBeforeFinding != 3 ||
		closed.SuccessAt3 != 1 || closed.MedianExecutionsBeforeFinding != 1.5 {
		t.Fatalf("held-out result changed: random=%#v scout=%#v closed=%#v", random, scout, closed)
	}
}
