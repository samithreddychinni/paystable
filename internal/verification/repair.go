package verification

import (
	"fmt"
	"reflect"
)

type RepairReport struct {
	Version             int    `json:"version"`
	Transformation      string `json:"transformation"`
	SourceProgram       string `json:"source_program"`
	RepairedProgram     string `json:"repaired_program"`
	Invariant           string `json:"invariant"`
	SearchGatePassed    bool   `json:"search_gate_passed"`
	CheckedSchedules    int    `json:"checked_schedules"`
	RemainingViolations int    `json:"remaining_violations"`
	DeterministicReplay bool   `json:"deterministic_replay"`
	Before              Result `json:"before"`
	After               Result `json:"after"`
}

// ApplyTerminalStateRepair preserves a captured state after stale events.
func ApplyTerminalStateRepair(schedule Schedule) (Schedule, error) {
	if schedule.Program != ProgramTerminalRegression {
		return Schedule{}, fmt.Errorf("repair requires program %q", ProgramTerminalRegression)
	}
	repaired := schedule
	repaired.Name += " with stable terminal state"
	repaired.Program = ProgramTerminalStable
	return repaired, nil
}

// VerifyTerminalStateRepair checks the search gate and all bounded schedules.
func VerifyTerminalStateRepair(corpus ProgramCorpus) (RepairReport, error) {
	if err := verifySearchGate(corpus); err != nil {
		return RepairReport{}, err
	}
	var source ProgramCase
	for _, program := range corpus.Programs {
		if program.Program == ProgramTerminalRegression {
			source = program
			break
		}
	}
	if source.Program == "" {
		return RepairReport{}, fmt.Errorf("corpus does not contain %q", ProgramTerminalRegression)
	}
	before, err := Run(source.GroundTruth)
	if err != nil {
		return RepairReport{}, err
	}
	repaired, err := ApplyTerminalStateRepair(source.GroundTruth)
	if err != nil {
		return RepairReport{}, err
	}
	after, err := Run(repaired)
	if err != nil {
		return RepairReport{}, err
	}
	replay, err := Run(repaired)
	if err != nil {
		return RepairReport{}, err
	}
	report := RepairReport{
		Version: 1, Transformation: "preserve captured terminal state",
		SourceProgram: source.Program, RepairedProgram: repaired.Program,
		Invariant: InvariantTerminalStateStable, SearchGatePassed: true,
		DeterministicReplay: reflect.DeepEqual(after, replay), Before: before, After: after,
	}
	if !resultHasInvariant(before, InvariantTerminalStateStable) {
		return RepairReport{}, fmt.Errorf("ground truth does not violate %s", InvariantTerminalStateStable)
	}
	if !report.DeterministicReplay || len(after.Violations) != 0 {
		return RepairReport{}, fmt.Errorf("repair did not remove the deterministic violation")
	}
	graph, err := CompileBehaviorGraph(ProgramTerminalStable, corpus.MaxScheduleActions)
	if err != nil {
		return RepairReport{}, err
	}
	for i, candidate := range graphCandidates(graph) {
		schedule := Schedule{
			Name: "repaired terminal-state check", Program: ProgramTerminalStable,
			OrderID: fmt.Sprintf("order_repair_%d", i+1), Actions: candidate.actions,
		}
		result, err := Run(schedule)
		if err != nil {
			return RepairReport{}, err
		}
		report.CheckedSchedules++
		report.RemainingViolations += len(result.Violations)
	}
	if report.RemainingViolations != 0 {
		return RepairReport{}, fmt.Errorf("repair left %d bounded violations", report.RemainingViolations)
	}
	return report, nil
}

func verifySearchGate(corpus ProgramCorpus) error {
	scout, err := RunScoutReport(corpus, 50)
	if err != nil {
		return err
	}
	if scout.Summary.SuccessAt50 != 1 || scout.Summary.FalseFindingCount != 0 || scout.Summary.DeterministicReplayRate != 1 {
		return fmt.Errorf("Scout did not pass the search gate")
	}
	baselines, err := RunBaselineReport(corpus, 50, 7)
	if err != nil {
		return err
	}
	for _, baseline := range baselines.Summary {
		if scout.Summary.MedianExecutionsBeforeFinding >= baseline.MedianExecutionsBeforeFinding {
			return fmt.Errorf("Scout did not beat %s", baseline.Method)
		}
	}
	return nil
}
