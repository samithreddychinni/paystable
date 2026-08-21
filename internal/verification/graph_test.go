package verification

import (
	"reflect"
	"testing"
)

func TestBehaviorGraphIsDeterministicAndContainsRequiredPaths(t *testing.T) {
	tests := []struct {
		program string
		path    []Action
	}{
		{ProgramFulfillBeforeDedup, []Action{
			{Type: "deliver", EventID: "event_captured", Status: "captured", CrashAt: "after_fulfillment"},
			{Type: "restart"},
			{Type: "deliver", EventID: "event_captured", Status: "captured"},
		}},
		{ProgramNewKeyOnRetry, []Action{
			{Type: "deliver", EventID: "event_captured", Status: "captured"},
			{Type: "fulfill", Response: "lost"},
			{Type: "fulfill", Response: "ok"},
		}},
		{ProgramTerminalRegression, []Action{
			{Type: "deliver", EventID: "event_captured", Status: "captured"},
			{Type: "fulfill", Response: "ok"},
			{Type: "deliver", EventID: "event_stale", Status: "failed"},
		}},
		{ProgramCorrect, []Action{
			{Type: "deliver", EventID: "event_captured", Status: "captured"},
			{Type: "fulfill", Response: "lost"},
			{Type: "fulfill", Response: "ok"},
			{Type: "deliver", EventID: "event_stale", Status: "failed"},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.program, func(t *testing.T) {
			first, err := CompileBehaviorGraph(tt.program, 4)
			if err != nil {
				t.Fatal(err)
			}
			second, err := CompileBehaviorGraph(tt.program, 4)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(first, second) {
				t.Fatal("graph compilation is not deterministic")
			}
			if !graphContainsPath(first, tt.path) {
				t.Fatalf("graph does not contain required path: %#v", tt.path)
			}
			for _, edge := range first.Edges {
				if edge.From < 0 || edge.From >= len(first.Nodes) || edge.To < 0 || edge.To >= len(first.Nodes) {
					t.Fatalf("edge has an invalid node: %#v", edge)
				}
				if first.Nodes[edge.To].Depth != first.Nodes[edge.From].Depth+1 {
					t.Fatalf("edge crosses an invalid depth: %#v", edge)
				}
				if edge.From == 0 && (edge.Action.Type == "fulfill" || edge.Action.Type == "restart") {
					t.Fatalf("root has an illegal action: %#v", edge.Action)
				}
			}
		})
	}
}

func graphContainsPath(graph BehaviorGraph, path []Action) bool {
	current := map[int]bool{0: true}
	for _, action := range path {
		next := make(map[int]bool)
		for _, edge := range graph.Edges {
			if current[edge.From] && reflect.DeepEqual(edge.Action, action) {
				next[edge.To] = true
			}
		}
		current = next
	}
	return len(current) != 0
}
