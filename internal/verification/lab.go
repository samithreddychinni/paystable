package verification

import (
	"fmt"
)

const (
	ProgramCorrect                 = "correct"
	ProgramFulfillBeforeDedup      = "fulfill-before-dedup"
	ProgramNewKeyOnRetry           = "new-key-on-retry"
	ProgramNewKeyOnTimeout         = "new-key-on-timeout"
	ProgramNewKeyOnReset           = "new-key-on-reset"
	ProgramNewKeyOnServerError     = "new-key-on-server-error"
	ProgramTerminalRegression      = "terminal-regression"
	ProgramTerminalStable          = "terminal-stable"
	ProgramAcceptUntrusted         = "accept-untrusted-webhook"
	ProgramCorrectSecurity         = "correct-security"
	ProgramCorrectNetwork          = "correct-network"
	InvariantFulfillmentAtMostOnce = "INV-2"
	InvariantTerminalStateStable   = "INV-4"
	InvariantTrustedEventsOnly     = "INV-SEC-1"
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
	Trust    string `json:"trust,omitempty"`
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
	untrusted     bool
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
	for _, entry := range trace {
		if entry.Action == "untrusted_accept" {
			result.Violations = append(result.Violations, Violation{
				Invariant: InvariantTrustedEventsOnly,
				Detail:    fmt.Sprintf("order %s accepted an untrusted payment event", schedule.OrderID),
			})
			break
		}
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
			if action.Trust != "" && action.Trust != "valid" && action.Trust != "missing-signature" && action.Trust != "invalid-signature" && action.Trust != "tampered-body" {
				return fmt.Errorf("action %d has an invalid trust condition", i+1)
			}
			if action.Response != "" {
				return fmt.Errorf("action %d has fields that deliver does not use", i+1)
			}
			if action.CrashAt != "" && action.CrashAt != "after_fulfillment" {
				return fmt.Errorf("action %d has an invalid crash checkpoint", i+1)
			}
		case "fulfill":
			if action.EventID != "" || action.Status != "" || action.CrashAt != "" || action.Trust != "" {
				return fmt.Errorf("action %d has fields that fulfill does not use", i+1)
			}
			if action.Response != "ok" && action.Response != "lost" && action.Response != "timeout" && action.Response != "connection-reset" && action.Response != "http-500" {
				return fmt.Errorf("action %d has an invalid fulfillment response", i+1)
			}
		case "restart":
			if action.EventID != "" || action.Status != "" || action.CrashAt != "" || action.Response != "" || action.Trust != "" {
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
	case ProgramCorrect, ProgramFulfillBeforeDedup, ProgramNewKeyOnRetry, ProgramNewKeyOnTimeout,
		ProgramNewKeyOnReset, ProgramNewKeyOnServerError, ProgramTerminalRegression,
		ProgramTerminalStable, ProgramAcceptUntrusted, ProgramCorrectSecurity, ProgramCorrectNetwork:
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
	trusted := action.Trust == "" || action.Trust == "valid"
	if !trusted && r.schedule.Program != ProgramAcceptUntrusted {
		r.record("reject", "untrusted payment event rejected")
		return nil
	}
	if !trusted {
		r.untrusted = true
		r.record("untrusted_accept", "untrusted payment event accepted")
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
	if UsesNewKey(r.schedule.Program) {
		key = fmt.Sprintf("%s:%d", key, r.effectAttempt)
	}
	if !r.effects[key] {
		r.effects[key] = true
		r.effectCount++
	}
	r.record("fulfill", "fulfillment sink accepted key "+key)
	if action.Response != "ok" {
		r.record(ResponseTraceAction(action.Response), "merchant did not receive a reliable sink response")
		return nil
	}
	r.pendingEffect = false
	return nil
}

// UsesNewKey reports whether a program changes its fulfillment key after a retry.
func UsesNewKey(program string) bool {
	switch program {
	case ProgramNewKeyOnRetry, ProgramNewKeyOnTimeout, ProgramNewKeyOnReset, ProgramNewKeyOnServerError:
		return true
	}
	return false
}

// ResponseTraceAction returns the trace action for an uncertain response.
func ResponseTraceAction(response string) string {
	switch response {
	case "timeout":
		return "response_timeout"
	case "connection-reset":
		return "connection_reset"
	case "http-500":
		return "response_http_500"
	default:
		return "response_lost"
	}
}

func (r *runner) record(action, detail string) {
	r.trace = append(r.trace, TraceEntry{
		Sequence: len(r.trace) + 1, Action: action, Detail: detail,
		State: r.state, EffectCount: r.effectCount,
	})
}
