package verification

import (
	"reflect"
	"testing"
)

func TestDemoIsDeterministicAndComplete(t *testing.T) {
	first, err := RunDemo()
	if err != nil {
		t.Fatal(err)
	}
	second, err := RunDemo()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("demo report is not deterministic")
	}
	if !first.Passed || len(first.Programs) != len(GenerateProgramCorpus().Programs) || len(first.Search) != 5 {
		t.Fatalf("demo report is incomplete: %#v", first)
	}
	if first.RepairCheckedSchedules == 0 || first.RepairRemainingFailures != 0 || first.ScoutModelBytes == 0 || first.ScoutParameterCount != len(scoutFeatureNames) {
		t.Fatalf("demo evidence is incomplete: %#v", first)
	}
	if first.FeaturedFinding == nil || len(first.FeaturedFinding.Result.Violations) != 1 || first.FeaturedFinding.Reduction.ReducedActionCount != 3 {
		t.Fatalf("featured finding is incomplete: %#v", first.FeaturedFinding)
	}
	if len(first.HeldOut.Cases) != 8 || len(first.HeldOut.RandomSeeds) != 100 {
		t.Fatalf("held-out evidence is incomplete: %#v", first.HeldOut)
	}
	if !first.InvariantContracts.Passed || len(first.InvariantContracts.Contracts) != 5 {
		t.Fatalf("invariant contracts are incomplete: %#v", first.InvariantContracts)
	}
}
