package verification

import (
	"fmt"
)

const (
	ProgramCorrect                 = "correct"
	ProgramFulfillBeforeDedup      = "fulfill-before-dedup"
	ProgramNewKeyOnRetry           = "new-key-on-retry"
	ProgramTerminalRegression      = "terminal-regression"
	ProgramTerminalStable          = "terminal-stable"
	InvariantFulfillmentAtMostOnce = "INV-2"
	InvariantTerminalStateStable   = "INV-4"
)

type Schedule struct {
	Name    string   `json:"name"`
	Program string   `json:"program"`
	OrderID string   `json:"order_id"`
	Actions []Action `json:"actions"`
}

type Action struct {
	Type     string `json:"type"`
	EventID  string `json:"event_id,omitempty"`
	Status   string `json:"status,omitempty"`
	CrashAt  string `json:"crash_at,omitempty"`
	Response string `json:"response,omitempty"`
}

type TraceEntry struct {
	Sequence    int    `json:"sequence"`
	Action      string `json:"action"`
	Detail      string `json:"detail"`
	State       string `json:"state"`
	EffectCount int    `json:"effect_count"`
}

type Violation struct {
	Invariant string `json:"invariant"`
	Detail    string `json:"detail"`
}

type Result struct {
	Scenario    string       `json:"scenario"`
	Program     string       `json:"program"`
	FinalState  string       `json:"final_state"`
	EffectCount int          `json:"effect_count"`
	Violations  []Violation  `json:"violations"`
	Trace       []TraceEntry `json:"trace"`
}

// The in-process lab provides fast deterministic checks. Use the container lab when a test requires process crashes.
type runner struct {
	schedule      Schedule
	running       bool
	seen          map[string]bool
	state         string
	capturedOnce  bool
	pendingEffect bool
	effects       map[string]bool
	effectCount   int
	effectAttempt int
	trace         []TraceEntry
}

func Run(schedule Schedule) (Result, error) {
	if err := Validate(schedule); err != nil {
		return Result{}, err
	}
	r := &runner{
		schedule: schedule, running: true, seen: make(map[string]bool),
		effects: make(map[string]bool),
	}
	for _, action := range schedule.Actions {
		if err := r.run(action); err != nil {
			return Result{}, err
		}
	}

	return ResultFor(schedule, r.state, r.capturedOnce, r.effectCount, r.trace), nil
}

// ResultFor applies the deterministic invariants to an observed execution.
func ResultFor(schedule Schedule, finalState string, capturedOnce bool, effectCount int, trace []TraceEntry) Result {
	result := Result{
		Scenario: schedule.Name, Program: schedule.Program, FinalState: finalState,
		EffectCount: effectCount, Violations: []Violation{}, Trace: trace,
	}
	if effectCount > 1 {
		result.Violations = append(result.Violations, Violation{
			Invariant: InvariantFulfillmentAtMostOnce,
			Detail:    fmt.Sprintf("order %s produced %d fulfillment effects", schedule.OrderID, effectCount),
		})
	}
	if capturedOnce && finalState != "captured" {
		result.Violations = append(result.Violations, Violation{
			Invariant: InvariantTerminalStateStable,
			Detail:    fmt.Sprintf("captured order %s regressed to %s", schedule.OrderID, finalState),
		})
	}
	return result
}

// Validate rejects schedules outside the bounded action grammar.
func Validate(schedule Schedule) error {
	if schedule.Name == "" || schedule.OrderID == "" || len(schedule.Actions) == 0 {
		return fmt.Errorf("name, order_id, and actions are required")
	}
	if !supportedProgram(schedule.Program) {
		return fmt.Errorf("unsupported program %q", schedule.Program)
	}
	for i, action := range schedule.Actions {
		switch action.Type {
		case "deliver":
			if action.EventID == "" || (action.Status != "captured" && action.Status != "failed") {
				return fmt.Errorf("action %d has an invalid payment event", i+1)
			}
			if action.Response != "" {
				return fmt.Errorf("action %d has fields that deliver does not use", i+1)
			}
			if action.CrashAt != "" && action.CrashAt != "after_fulfillment" {
				return fmt.Errorf("action %d has an invalid crash checkpoint", i+1)
			}
		case "fulfill":
			if action.EventID != "" || action.Status != "" || action.CrashAt != "" {
				return fmt.Errorf("action %d has fields that fulfill does not use", i+1)
			}
			if action.Response != "ok" && action.Response != "lost" {
				return fmt.Errorf("action %d has an invalid fulfillment response", i+1)
			}
		case "restart":
			if action.EventID != "" || action.Status != "" || action.CrashAt != "" || action.Response != "" {
				return fmt.Errorf("action %d has fields that restart does not use", i+1)
			}
		default:
			return fmt.Errorf("action %d has unsupported type %q", i+1, action.Type)
		}
	}
	return nil
}

func supportedProgram(program string) bool {
	switch program {
	case ProgramCorrect, ProgramFulfillBeforeDedup, ProgramNewKeyOnRetry, ProgramTerminalRegression, ProgramTerminalStable:
		return true
	}
	return false
}

func (r *runner) run(action Action) error {
	switch action.Type {
	case "restart":
		r.running = true
		r.record("restart", "merchant process restarted")
		return nil
	case "deliver":
		return r.deliver(action)
	case "fulfill":
		return r.fulfill(action)
	}
	return nil
}

func (r *runner) deliver(action Action) error {
	if !r.running {
		return fmt.Errorf("cannot deliver %s while the merchant is stopped", action.EventID)
	}
	if r.seen[action.EventID] {
		r.record("deliver", "duplicate event ignored")
		return nil
	}

	if r.schedule.Program == ProgramFulfillBeforeDedup && action.Status == "captured" {
		r.effectCount++
		r.record("fulfill", "fulfillment occurred before durable event storage")
		if action.CrashAt == "after_fulfillment" {
			r.running = false
			r.record("crash", "merchant crashed after fulfillment")
			return nil
		}
	}

	r.seen[action.EventID] = true
	r.record("checkpoint", "event stored at after_deduplication")
	wasCaptured := r.state == "captured"
	if r.schedule.Program == ProgramTerminalRegression || r.state != "captured" {
		r.state = action.Status
	}
	if r.state == "captured" {
		r.capturedOnce = true
		if !wasCaptured && action.Status == "captured" && r.schedule.Program != ProgramFulfillBeforeDedup {
			r.pendingEffect = true
		}
	}
	r.record("deliver", "payment state updated")
	return nil
}

func (r *runner) fulfill(action Action) error {
	if !r.running {
		return fmt.Errorf("cannot fulfill while the merchant is stopped")
	}
	if !r.pendingEffect {
		return fmt.Errorf("cannot fulfill before a captured payment")
	}
	r.effectAttempt++
	key := r.schedule.OrderID
	if r.schedule.Program == ProgramNewKeyOnRetry {
		key = fmt.Sprintf("%s:%d", key, r.effectAttempt)
	}
	if !r.effects[key] {
		r.effects[key] = true
		r.effectCount++
	}
	r.record("fulfill", "fulfillment sink accepted key "+key)
	if action.Response == "lost" {
		r.record("response_lost", "merchant did not receive the sink response")
		return nil
	}
	r.pendingEffect = false
	return nil
}

func (r *runner) record(action, detail string) {
	r.trace = append(r.trace, TraceEntry{
		Sequence: len(r.trace) + 1, Action: action, Detail: detail,
		State: r.state, EffectCount: r.effectCount,
	})
}
