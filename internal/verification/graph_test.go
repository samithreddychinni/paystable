package verification

import (
	"reflect"
	"testing"
)

func TestBehaviorGraphIsDeterministicAndContainsRequiredPaths(t *testing.T) {
	corpus := GenerateProgramCorpus()
	for _, program := range corpus.Programs {
		t.Run(program.Program, func(t *testing.T) {
			first, err := CompileBehaviorGraph(program.Program, corpus.MaxScheduleActions)
			if err != nil {
				t.Fatal(err)
			}
			second, err := CompileBehaviorGraph(program.Program, corpus.MaxScheduleActions)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(first, second) {
				t.Fatal("graph compilation is not deterministic")
			}
			if !graphContainsPath(first, program.GroundTruth.Actions) {
				t.Fatalf("graph does not contain ground truth: %#v", program.GroundTruth.Actions)
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
