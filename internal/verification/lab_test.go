package verification

import (
	"reflect"
	"testing"
)

func TestRequiredBugSchedulesReplayDeterministically(t *testing.T) {
	tests := []struct {
		name      string
		program   string
		actions   []Action
		invariant string
	}{
		{
			name: "fulfillment before dedup", program: ProgramFulfillBeforeDedup,
			actions: []Action{
				{Type: "deliver", EventID: "event_1", Status: "captured", CrashAt: "after_fulfillment"},
				{Type: "restart"},
				{Type: "deliver", EventID: "event_1", Status: "captured"},
			},
			invariant: InvariantFulfillmentAtMostOnce,
		},
		{
			name: "new retry key", program: ProgramNewKeyOnRetry,
			actions: []Action{
				{Type: "deliver", EventID: "event_1", Status: "captured"},
				{Type: "fulfill", Response: "lost"},
				{Type: "fulfill", Response: "ok"},
			},
			invariant: InvariantFulfillmentAtMostOnce,
		},
		{
			name: "terminal state regression", program: ProgramTerminalRegression,
			actions: []Action{
				{Type: "deliver", EventID: "event_1", Status: "captured"},
				{Type: "fulfill", Response: "ok"},
				{Type: "deliver", EventID: "event_0", Status: "failed"},
			},
			invariant: InvariantTerminalStateStable,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			schedule := Schedule{Name: test.name, Program: test.program, OrderID: "order_1", Actions: test.actions}
			first, err := Run(schedule)
			if err != nil {
				t.Fatal(err)
			}
			second, err := Run(schedule)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(first, second) {
				t.Fatal("replay changed the result")
			}
			if len(first.Violations) != 1 || first.Violations[0].Invariant != test.invariant {
				t.Fatalf("violations = %#v, want %s", first.Violations, test.invariant)
			}
		})
	}
}

func TestCorrectProgramHasNoFinding(t *testing.T) {
	schedule := Schedule{
		Name: "correct merchant", Program: ProgramCorrect, OrderID: "order_1",
		Actions: []Action{
			{Type: "deliver", EventID: "event_1", Status: "captured"},
			{Type: "fulfill", Response: "lost"},
			{Type: "fulfill", Response: "ok"},
			{Type: "deliver", EventID: "event_0", Status: "failed"},
		},
	}
	result, err := Run(schedule)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Violations) != 0 || result.EffectCount != 1 || result.FinalState != "captured" {
		t.Fatalf("correct result = %#v", result)
	}
}
