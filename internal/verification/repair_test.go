package verification

import (
	"reflect"
	"testing"
)

func TestTerminalStateRepairPassesBoundedVerification(t *testing.T) {
	corpus := GenerateProgramCorpus()
	first, err := VerifyTerminalStateRepair(corpus)
	if err != nil {
		t.Fatal(err)
	}
	second, err := VerifyTerminalStateRepair(corpus)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("repair report is not deterministic")
	}
	if !first.SearchGatePassed || !first.DeterministicReplay || first.CheckedSchedules == 0 || first.RemainingViolations != 0 {
		t.Fatalf("repair did not pass: %#v", first)
	}
	if first.Before.FinalState != "failed" || first.After.FinalState != "captured" || len(first.After.Violations) != 0 {
		t.Fatalf("repair result is incorrect: before=%#v after=%#v", first.Before, first.After)
	}
}
