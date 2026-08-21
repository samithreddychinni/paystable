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
	if !first.Passed || len(first.Programs) != 4 || len(first.Search) != 5 {
		t.Fatalf("demo report is incomplete: %#v", first)
	}
	if first.RepairCheckedSchedules == 0 || first.RepairRemainingFailures != 0 || first.ScoutModelBytes == 0 {
		t.Fatalf("demo evidence is incomplete: %#v", first)
	}
}
