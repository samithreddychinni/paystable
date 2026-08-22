package verification

import (
	"reflect"
	"testing"
)

func TestProgramCorpusGroundTruthReplaysDeterministically(t *testing.T) {
	corpus := GenerateProgramCorpus()
	if len(corpus.Programs) != 25 {
		t.Fatalf("corpus has %d programs, want 25", len(corpus.Programs))
	}
	for _, program := range corpus.Programs {
		t.Run(program.Program, func(t *testing.T) {
			if len(program.GroundTruth.Actions) > corpus.MaxScheduleActions {
				t.Fatalf("ground truth has %d actions, limit is %d", len(program.GroundTruth.Actions), corpus.MaxScheduleActions)
			}
			first, err := Run(program.GroundTruth)
			if err != nil {
				t.Fatal(err)
			}
			second, err := Run(program.GroundTruth)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(first, second) {
				t.Fatal("replay changed the result")
			}
			if first.FinalState != program.ExpectedFinalState || first.EffectCount != program.ExpectedEffectCount {
				t.Fatalf("result state=%q effects=%d, want state=%q effects=%d", first.FinalState, first.EffectCount, program.ExpectedFinalState, program.ExpectedEffectCount)
			}
			if program.ExpectedInvariant == "" {
				if len(first.Violations) != 0 {
					t.Fatalf("correct program violations = %#v", first.Violations)
				}
			} else if len(first.Violations) != 1 || first.Violations[0].Invariant != program.ExpectedInvariant {
				t.Fatalf("violations = %#v, want %s", first.Violations, program.ExpectedInvariant)
			}
			if program.ExpectedInvariant != "" {
				reduced, _, err := Reduce(program.GroundTruth, program.ExpectedInvariant, Run)
				if err != nil {
					t.Fatal(err)
				}
				if len(reduced.Actions) != len(program.GroundTruth.Actions) {
					t.Fatalf("ground truth reduces from %d to %d actions", len(program.GroundTruth.Actions), len(reduced.Actions))
				}
			}
		})
	}
}
