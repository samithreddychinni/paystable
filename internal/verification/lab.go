package verification

import (
	"fmt"
)

const (
	ProgramCorrect                       = "correct"
	ProgramConcurrentBeforeClaim         = "fulfill-before-concurrent-claim"
	ProgramCorrectConcurrency            = "correct-concurrency"
	ProgramFulfillBeforeDedup            = "fulfill-before-dedup"
	ProgramNewKeyOnRetry                 = "new-key-on-retry"
	ProgramNewKeyOnTimeout               = "new-key-on-timeout"
	ProgramNewKeyOnReset                 = "new-key-on-reset"
	ProgramNewKeyOnServerError           = "new-key-on-server-error"
	ProgramNewKeyOnDBConflict            = "new-key-on-db-conflict"
	ProgramNewKeyOnDBDeadlock            = "new-key-on-db-deadlock"
	ProgramRetryForever                  = "retry-forever"
	ProgramRetryBounded                  = "retry-bounded"
	ProgramTerminalRegression            = "terminal-regression"
	ProgramTerminalStable                = "terminal-stable"
	ProgramAcceptUntrusted               = "accept-untrusted-webhook"
	ProgramCorrectSecurity               = "correct-security"
	ProgramCorrectNetwork                = "correct-network"
	ProgramCorrectDBConflict             = "correct-db-conflict"
	ProgramCorrectDBDeadlock             = "correct-db-deadlock"
	ProgramAcceptWrongAmount             = "accept-wrong-amount"
	ProgramCorrectAmount                 = "correct-amount"
	ProgramAcceptWrongOrder              = "accept-wrong-order"
	ProgramCorrectOrder                  = "correct-order"
	ProgramAcceptWrongCurrency           = "accept-wrong-currency"
	ProgramCorrectCurrency               = "correct-currency"
	ProgramExpiringEventClaim            = "expire-event-claim"
	ProgramDurableEventClaim             = "durable-event-claim"
	InvariantFulfillmentAtMostOnce       = "INV-2"
	InvariantTerminalStateStable         = "INV-4"
	InvariantTrustedEventsOnly           = "INV-SEC-1"
	InvariantRetryBounded                = "INV-RETRY-1"
	InvariantExpectedAmount              = "INV-AMOUNT-1"
	InvariantExpectedOrder               = "INV-ORDER-1"
	InvariantExpectedCurrency            = "INV-CURRENCY-1"
	ExpectedPaymentAmount          int64 = 49900
	ExpectedPaymentCurrency              = "INR"
	EventClaimRetentionSeconds     int64 = 86400
	MaxAdvanceSeconds              int64 = 604800
)

type Schedule struct {
	Name    string   `json:"name"`
	Program string   `json:"program"`
	OrderID string   `json:"order_id"`
	Actions []Action `json:"actions"`
}

type Action struct {
	Type           string `json:"type"`
	EventID        string `json:"event_id,omitempty"`
	Status         string `json:"status,omitempty"`
	CrashAt        string `json:"crash_at,omitempty"`
	Response       string `json:"response,omitempty"`
	Trust          string `json:"trust,omitempty"`
	Parallel       int    `json:"parallel,omitempty"`
	Amount         int64  `json:"amount,omitempty"`
	PaymentOrderID string `json:"payment_order_id,omitempty"`
	Currency       string `json:"currency,omitempty"`
	AdvanceSeconds int64  `json:"advance_seconds,omitempty"`
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
	wrongAmount   bool
	wrongOrder    bool
	wrongCurrency bool
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
		if entry.Action == "retry_overrun" {
			result.Violations = append(result.Violations, Violation{
				Invariant: InvariantRetryBounded,
				Detail:    fmt.Sprintf("order %s exceeded its retry limit", schedule.OrderID),
			})
			break
		}
		if entry.Action == "amount_mismatch_accept" {
			result.Violations = append(result.Violations, Violation{
				Invariant: InvariantExpectedAmount,
				Detail:    fmt.Sprintf("order %s accepted a payment amount mismatch", schedule.OrderID),
			})
			break
		}
		if entry.Action == "order_mismatch_accept" {
			result.Violations = append(result.Violations, Violation{
				Invariant: InvariantExpectedOrder,
				Detail:    fmt.Sprintf("order %s accepted a payment for another order", schedule.OrderID),
			})
			break
		}
		if entry.Action == "currency_mismatch_accept" {
			result.Violations = append(result.Violations, Violation{
				Invariant: InvariantExpectedCurrency,
				Detail:    fmt.Sprintf("order %s accepted a payment currency mismatch", schedule.OrderID),
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
			if action.AdvanceSeconds != 0 {
				return fmt.Errorf("action %d has fields that deliver does not use", i+1)
			}
			if action.Amount < 0 {
				return fmt.Errorf("action %d has an invalid payment amount", i+1)
			}
			if action.CrashAt != "" && action.CrashAt != "after_fulfillment" {
				return fmt.Errorf("action %d has an invalid crash checkpoint", i+1)
			}
			if action.Parallel != 0 && action.Parallel != 2 {
				return fmt.Errorf("action %d must use two parallel deliveries", i+1)
			}
			if action.Parallel != 0 && action.CrashAt != "" {
				return fmt.Errorf("action %d cannot combine parallel delivery with a crash", i+1)
			}
		case "fulfill":
			if action.EventID != "" || action.Status != "" || action.CrashAt != "" || action.Trust != "" || action.Parallel != 0 || action.Amount != 0 || action.PaymentOrderID != "" || action.Currency != "" || action.AdvanceSeconds != 0 {
				return fmt.Errorf("action %d has fields that fulfill does not use", i+1)
			}
			if action.Response != "ok" && action.Response != "lost" && action.Response != "timeout" && action.Response != "connection-reset" && action.Response != "http-500" && action.Response != "db-conflict" && action.Response != "db-deadlock" {
				return fmt.Errorf("action %d has an invalid fulfillment response", i+1)
			}
		case "restart":
			if action.EventID != "" || action.Status != "" || action.CrashAt != "" || action.Response != "" || action.Trust != "" || action.Parallel != 0 || action.Amount != 0 || action.PaymentOrderID != "" || action.Currency != "" || action.AdvanceSeconds != 0 {
				return fmt.Errorf("action %d has fields that restart does not use", i+1)
			}
		case "advance":
			if action.EventID != "" || action.Status != "" || action.CrashAt != "" || action.Response != "" || action.Trust != "" || action.Parallel != 0 || action.Amount != 0 || action.PaymentOrderID != "" || action.Currency != "" {
				return fmt.Errorf("action %d has fields that advance does not use", i+1)
			}
			if action.AdvanceSeconds < 1 || action.AdvanceSeconds > MaxAdvanceSeconds {
				return fmt.Errorf("action %d has an invalid clock advance", i+1)
			}
		default:
			return fmt.Errorf("action %d has unsupported type %q", i+1, action.Type)
		}
	}
	return nil
}

func supportedProgram(program string) bool {
	switch program {
	case ProgramCorrect, ProgramConcurrentBeforeClaim, ProgramCorrectConcurrency, ProgramFulfillBeforeDedup, ProgramNewKeyOnRetry, ProgramNewKeyOnTimeout,
		ProgramNewKeyOnReset, ProgramNewKeyOnServerError, ProgramTerminalRegression,
		ProgramNewKeyOnDBConflict, ProgramNewKeyOnDBDeadlock, ProgramRetryForever, ProgramRetryBounded,
		ProgramTerminalStable, ProgramAcceptUntrusted, ProgramCorrectSecurity,
		ProgramCorrectNetwork, ProgramCorrectDBConflict, ProgramCorrectDBDeadlock,
		ProgramAcceptWrongAmount, ProgramCorrectAmount, ProgramAcceptWrongOrder, ProgramCorrectOrder,
		ProgramAcceptWrongCurrency, ProgramCorrectCurrency, ProgramExpiringEventClaim, ProgramDurableEventClaim:
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
		copies := action.Parallel
		if copies == 0 {
			copies = 1
		}
		for range copies {
			if err := r.deliver(action); err != nil {
				return err
			}
		}
		return nil
	case "fulfill":
		return r.fulfill(action)
	case "advance":
		r.record("advance", fmt.Sprintf("test clock advanced by %d seconds", action.AdvanceSeconds))
		if r.schedule.Program == ProgramExpiringEventClaim && action.AdvanceSeconds > EventClaimRetentionSeconds {
			clear(r.seen)
			r.record("expire", "event claims expired")
		}
	}
	return nil
}

func (r *runner) deliver(action Action) error {
	if !r.running {
		return fmt.Errorf("cannot deliver %s while the merchant is stopped", action.EventID)
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
	if HasAmountMismatch(action) && r.schedule.Program != ProgramAcceptWrongAmount {
		r.record("reject", "payment amount mismatch rejected")
		return nil
	}
	if HasAmountMismatch(action) {
		r.wrongAmount = true
		r.record("amount_mismatch_accept", "payment amount mismatch accepted")
	}
	if HasCurrencyMismatch(action) && r.schedule.Program != ProgramAcceptWrongCurrency {
		r.record("reject", "payment currency mismatch rejected")
		return nil
	}
	if HasCurrencyMismatch(action) {
		r.wrongCurrency = true
		r.record("currency_mismatch_accept", "payment currency mismatch accepted")
	}
	if HasOrderMismatch(r.schedule.OrderID, action) && r.schedule.Program != ProgramAcceptWrongOrder {
		r.record("reject", "payment order mismatch rejected")
		return nil
	}
	if HasOrderMismatch(r.schedule.OrderID, action) {
		r.wrongOrder = true
		r.record("order_mismatch_accept", "payment order mismatch accepted")
	}

	beforeDedup := FulfillsBeforeDedup(r.schedule.Program, action)
	if beforeDedup && action.Status == "captured" {
		r.effectCount++
		r.record("fulfill", "fulfillment occurred before durable event storage")
		if action.CrashAt == "after_fulfillment" {
			r.running = false
			r.record("crash", "merchant crashed after fulfillment")
			return nil
		}
	}
	if r.seen[action.EventID] {
		r.record("deliver", "duplicate event ignored")
		return nil
	}

	r.seen[action.EventID] = true
	r.record("checkpoint", "event stored at after_deduplication")
	wasCaptured := r.state == "captured"
	if r.schedule.Program == ProgramTerminalRegression || r.state != "captured" {
		r.state = action.Status
	}
	if r.state == "captured" {
		r.capturedOnce = true
		if !wasCaptured && action.Status == "captured" && !beforeDedup {
			r.pendingEffect = true
		}
	}
	r.record("deliver", "payment state updated")
	if (r.schedule.Program == ProgramExpiringEventClaim || r.schedule.Program == ProgramDurableEventClaim) && action.Status == "captured" {
		r.effectCount++
		r.record("fulfill", "accepted payment event caused fulfillment")
	}
	return nil
}

// HasAmountMismatch reports whether an explicit payment amount differs from the expected amount.
func HasAmountMismatch(action Action) bool {
	return action.Amount != 0 && action.Amount != ExpectedPaymentAmount
}

// HasCurrencyMismatch reports whether an explicit payment currency differs from INR.
func HasCurrencyMismatch(action Action) bool {
	return action.Currency != "" && action.Currency != ExpectedPaymentCurrency
}

// HasOrderMismatch reports whether an explicit payment order differs from the schedule order.
func HasOrderMismatch(expected string, action Action) bool {
	return action.PaymentOrderID != "" && action.PaymentOrderID != expected
}

// FulfillsBeforeDedup reports whether delivery can cause an effect before the event claim.
func FulfillsBeforeDedup(program string, action Action) bool {
	return program == ProgramFulfillBeforeDedup || (program == ProgramConcurrentBeforeClaim && action.Parallel == 2)
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
		if RetryExhausted(r.schedule.Program, r.effectAttempt) {
			r.pendingEffect = false
			r.record("retry_exhausted", "fulfillment retry limit reached")
		}
		if RetryOverrun(r.schedule.Program, r.effectAttempt) {
			r.record("retry_overrun", "fulfillment continued after the retry limit")
		}
		return nil
	}
	r.pendingEffect = false
	return nil
}

// UsesNewKey reports whether a program changes its fulfillment key after a retry.
func UsesNewKey(program string) bool {
	switch program {
	case ProgramNewKeyOnRetry, ProgramNewKeyOnTimeout, ProgramNewKeyOnReset, ProgramNewKeyOnServerError,
		ProgramNewKeyOnDBConflict, ProgramNewKeyOnDBDeadlock:
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
	case "db-conflict":
		return "database_conflict"
	case "db-deadlock":
		return "database_deadlock"
	default:
		return "response_lost"
	}
}

// RetryExhausted reports whether a program stops at its uncertain-attempt limit.
func RetryExhausted(program string, attempt int) bool {
	return program == ProgramRetryBounded && attempt >= 2
}

// RetryOverrun reports whether a program continued after its uncertain-attempt limit.
func RetryOverrun(program string, attempt int) bool {
	return program == ProgramRetryForever && attempt > 2
}

func (r *runner) record(action, detail string) {
	r.trace = append(r.trace, TraceEntry{
		Sequence: len(r.trace) + 1, Action: action, Detail: detail,
		State: r.state, EffectCount: r.effectCount,
	})
}
