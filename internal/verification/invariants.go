package verification

import "fmt"

const (
	InvariantNoUnauthorizedFulfillment = "INV-1"
	InvariantBoundedLiveness           = "INV-3"
	InvariantEventCallbackIdempotency  = "INV-5"
)

type ContractStep struct {
	Action          string `json:"action"`
	OrderID         string `json:"order_id,omitempty"`
	LogicalID       string `json:"logical_id,omitempty"`
	State           string `json:"state,omitempty"`
	Verified        bool   `json:"verified,omitempty"`
	IdentityMatched bool   `json:"identity_matched,omitempty"`
	ValueMatched    bool   `json:"value_matched,omitempty"`
	Healthy         bool   `json:"healthy,omitempty"`
}

type InvariantContract struct {
	Invariant         string         `json:"invariant"`
	Name              string         `json:"name"`
	Assumptions       []string       `json:"assumptions"`
	PassingTrace      []ContractStep `json:"passing_trace"`
	FailingTrace      []ContractStep `json:"failing_trace"`
	PassingViolations []Violation    `json:"passing_violations"`
	FailingViolations []Violation    `json:"failing_violations"`
}

type InvariantContractReport struct {
	Version   int                 `json:"version"`
	Horizon   int                 `json:"horizon"`
	Contracts []InvariantContract `json:"contracts"`
	Passed    bool                `json:"passed"`
}

// CheckInvariantTrace checks the five bounded PRD contracts.
func CheckInvariantTrace(trace []ContractStep, horizon int) []Violation {
	var violations []Violation
	authorized := make(map[string]bool)
	fulfillments := make(map[string]int)
	states := make(map[string]string)
	acceptedEvents := make(map[string]bool)
	emittedCallbacks := make(map[string]bool)

	for _, step := range trace {
		switch step.Action {
		case "authorize":
			authorized[step.OrderID] = authorized[step.OrderID] || step.Verified && step.IdentityMatched && step.ValueMatched
		case "fulfill":
			fulfillments[step.OrderID]++
			if !authorized[step.OrderID] {
				violations = appendInvariantViolation(violations, InvariantNoUnauthorizedFulfillment,
					fmt.Sprintf("order %s was fulfilled without verified matching evidence", step.OrderID))
			}
			if fulfillments[step.OrderID] > 1 {
				violations = appendInvariantViolation(violations, InvariantFulfillmentAtMostOnce,
					fmt.Sprintf("order %s produced more than one logical fulfillment", step.OrderID))
			}
		case "commit_state":
			previous := states[step.OrderID]
			if previous != "" && !allowedPaymentTransition(previous, step.State) {
				violations = appendInvariantViolation(violations, InvariantTerminalStateStable,
					fmt.Sprintf("order %s changed from %s to %s", step.OrderID, previous, step.State))
			}
			states[step.OrderID] = step.State
		case "accept_event":
			if acceptedEvents[step.LogicalID] {
				violations = appendInvariantViolation(violations, InvariantEventCallbackIdempotency,
					fmt.Sprintf("event %s was accepted more than once", step.LogicalID))
			}
			acceptedEvents[step.LogicalID] = true
		case "emit_callback":
			if emittedCallbacks[step.LogicalID] {
				violations = appendInvariantViolation(violations, InvariantEventCallbackIdempotency,
					fmt.Sprintf("callback %s was emitted more than once", step.LogicalID))
			}
			emittedCallbacks[step.LogicalID] = true
		}
	}

	if horizon < 1 {
		return violations
	}
	for i, step := range trace {
		end := i + horizon
		if step.Action != "eligible" || end >= len(trace) {
			continue
		}
		healthy, fulfilled := true, false
		for _, observed := range trace[i : end+1] {
			healthy = healthy && observed.Healthy
			fulfilled = fulfilled || observed.Action == "fulfill" && observed.OrderID == step.OrderID
		}
		if healthy && !fulfilled {
			violations = appendInvariantViolation(violations, InvariantBoundedLiveness,
				fmt.Sprintf("eligible order %s was not fulfilled within horizon %d", step.OrderID, horizon))
		}
	}
	return violations
}

func appendInvariantViolation(violations []Violation, invariant, detail string) []Violation {
	for _, violation := range violations {
		if violation.Invariant == invariant {
			return violations
		}
	}
	return append(violations, Violation{Invariant: invariant, Detail: detail})
}

func allowedPaymentTransition(previous, next string) bool {
	switch previous {
	case "authorized":
		return next == "authorized" || next == "captured" || next == "failed"
	case "captured":
		return next == "captured"
	case "failed":
		return next == "failed"
	}
	return false
}

// RunInvariantContractReport executes one passing and one failing fixture per contract.
func RunInvariantContractReport() (InvariantContractReport, error) {
	const horizon = 2
	authorized := ContractStep{Action: "authorize", OrderID: "order-1", Verified: true, IdentityMatched: true, ValueMatched: true, Healthy: true}
	contracts := []InvariantContract{
		{
			Invariant: InvariantNoUnauthorizedFulfillment, Name: "No unauthorized fulfillment",
			Assumptions:  []string{"The lab observes the merchant fulfillment boundary.", "Gateway evidence is authoritative."},
			PassingTrace: []ContractStep{authorized, {Action: "authorize", OrderID: "order-1"}, {Action: "fulfill", OrderID: "order-1"}},
			FailingTrace: []ContractStep{{Action: "authorize", OrderID: "order-1", Verified: true, ValueMatched: true}, {Action: "fulfill", OrderID: "order-1"}},
		},
		{
			Invariant: InvariantFulfillmentAtMostOnce, Name: "At-most-once logical fulfillment",
			Assumptions:  []string{"The lab observes one merchant-controlled idempotency boundary."},
			PassingTrace: []ContractStep{authorized, {Action: "fulfill", OrderID: "order-1"}},
			FailingTrace: []ContractStep{authorized, {Action: "fulfill", OrderID: "order-1"}, {Action: "fulfill", OrderID: "order-1"}},
		},
		{
			Invariant: InvariantBoundedLiveness, Name: "Bounded fulfillment liveness",
			Assumptions:  []string{"The environment stays healthy for the declared horizon.", "The run contains the complete observation window."},
			PassingTrace: []ContractStep{authorized, {Action: "eligible", OrderID: "order-1", Healthy: true}, {Action: "tick", Healthy: true}, {Action: "fulfill", OrderID: "order-1", Healthy: true}},
			FailingTrace: []ContractStep{{Action: "eligible", OrderID: "order-1", Healthy: true}, {Action: "tick", Healthy: true}, {Action: "tick", Healthy: true}},
		},
		{
			Invariant: InvariantTerminalStateStable, Name: "Monotonic legal payment state",
			Assumptions:  []string{"Payment identity stays stable across observations."},
			PassingTrace: []ContractStep{{Action: "commit_state", OrderID: "order-1", State: "authorized"}, {Action: "commit_state", OrderID: "order-1", State: "captured"}},
			FailingTrace: []ContractStep{{Action: "commit_state", OrderID: "order-1", State: "captured"}, {Action: "commit_state", OrderID: "order-1", State: "failed"}},
		},
		{
			Invariant: InvariantEventCallbackIdempotency, Name: "Idempotent event and callback acceptance",
			Assumptions:  []string{"Event and callback logical identifiers stay stable across retries."},
			PassingTrace: []ContractStep{{Action: "accept_event", LogicalID: "event-1"}, {Action: "emit_callback", LogicalID: "callback-1"}},
			FailingTrace: []ContractStep{{Action: "accept_event", LogicalID: "event-1"}, {Action: "accept_event", LogicalID: "event-1"}},
		},
	}
	report := InvariantContractReport{Version: 1, Horizon: horizon, Contracts: contracts}
	for i := range report.Contracts {
		contract := &report.Contracts[i]
		contract.PassingViolations = CheckInvariantTrace(contract.PassingTrace, horizon)
		contract.FailingViolations = CheckInvariantTrace(contract.FailingTrace, horizon)
		if len(contract.PassingViolations) != 0 || !resultHasInvariant(Result{Violations: contract.FailingViolations}, contract.Invariant) {
			return InvariantContractReport{}, fmt.Errorf("contract %s fixture failed", contract.Invariant)
		}
	}
	report.Passed = true
	return report, nil
}
