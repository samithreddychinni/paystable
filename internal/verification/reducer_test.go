package verification

import "testing"

func TestReduceProducesOneMinimalSchedules(t *testing.T) {
	tests := []struct {
		name      string
		program   string
		invariant string
		actions   []Action
		want      int
	}{
		{
			name: "fulfillment before deduplication", program: ProgramFulfillBeforeDedup,
			invariant: InvariantFulfillmentAtMostOnce, want: 3,
			actions: []Action{
				{Type: "deliver", EventID: "event-1", Status: "captured", CrashAt: "after_fulfillment"},
				{Type: "restart"},
				{Type: "deliver", EventID: "event-1", Status: "captured"},
			},
		},
		{
			name: "new key after lost response", program: ProgramNewKeyOnRetry,
			invariant: InvariantFulfillmentAtMostOnce, want: 3,
			actions: []Action{
				{Type: "deliver", EventID: "event-2", Status: "captured"},
				{Type: "fulfill", Response: "lost"},
				{Type: "fulfill", Response: "ok"},
			},
		},
		{
			name: "stale terminal regression", program: ProgramTerminalRegression,
			invariant: InvariantTerminalStateStable, want: 2,
			actions: []Action{
				{Type: "deliver", EventID: "event-3", Status: "captured"},
				{Type: "fulfill", Response: "ok"},
				{Type: "deliver", EventID: "event-old", Status: "failed"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			schedule := Schedule{Name: tt.name, Program: tt.program, OrderID: "order-1", Actions: tt.actions}
			reduced, report, err := Reduce(schedule, tt.invariant, Run)
			if err != nil {
				t.Fatal(err)
			}
			if len(reduced.Actions) != tt.want || report.ReducedActionCount != tt.want {
				t.Fatalf("got %d actions, want %d", len(reduced.Actions), tt.want)
			}
			result, err := Run(reduced)
			if err != nil || !hasViolation(result, tt.invariant) {
				t.Fatalf("reduced schedule lost %s: result=%+v err=%v", tt.invariant, result, err)
			}
			for i := range reduced.Actions {
				candidate := reduced
				candidate.Actions = removeAction(reduced.Actions, i)
				if Validate(candidate) != nil {
					continue
				}
				result, err := Run(candidate)
				if err == nil && hasViolation(result, tt.invariant) {
					t.Fatalf("action %d can still be removed", i+1)
				}
			}
		})
	}
}
