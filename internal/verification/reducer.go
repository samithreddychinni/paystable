package verification

import "fmt"

type ReductionReport struct {
	Invariant           string   `json:"invariant"`
	OriginalActionCount int      `json:"original_action_count"`
	ReducedActionCount  int      `json:"reduced_action_count"`
	CandidateExecutions int      `json:"candidate_executions"`
	RemovedActions      []Action `json:"removed_actions"`
}

// Reduce removes actions until no single remaining action can be removed while preserving the invariant violation.
func Reduce(schedule Schedule, invariant string, execute func(Schedule) (Result, error)) (Schedule, ReductionReport, error) {
	report := ReductionReport{
		Invariant:           invariant,
		OriginalActionCount: len(schedule.Actions),
		RemovedActions:      []Action{},
	}
	result, err := execute(schedule)
	if err != nil {
		return Schedule{}, report, fmt.Errorf("execute original schedule: %w", err)
	}
	if !hasViolation(result, invariant) {
		return Schedule{}, report, fmt.Errorf("original schedule does not violate %s", invariant)
	}

	for i := 0; i < len(schedule.Actions); {
		candidate := schedule
		candidate.Actions = removeAction(schedule.Actions, i)
		if Validate(candidate) != nil {
			i++
			continue
		}
		report.CandidateExecutions++
		result, err := execute(candidate)
		if err == nil && hasViolation(result, invariant) {
			report.RemovedActions = append(report.RemovedActions, schedule.Actions[i])
			schedule = candidate
			i = 0
			continue
		}
		i++
	}
	report.ReducedActionCount = len(schedule.Actions)
	return schedule, report, nil
}

func removeAction(actions []Action, index int) []Action {
	reduced := make([]Action, 0, len(actions)-1)
	reduced = append(reduced, actions[:index]...)
	return append(reduced, actions[index+1:]...)
}

func hasViolation(result Result, invariant string) bool {
	for _, violation := range result.Violations {
		if violation.Invariant == invariant {
			return true
		}
	}
	return false
}
