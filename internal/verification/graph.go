package verification

import (
	"fmt"
	"slices"
)

const MaxGraphActions = 8

type BehaviorGraph struct {
	Version    int            `json:"version"`
	Program    string         `json:"program"`
	MaxActions int            `json:"max_actions"`
	Nodes      []BehaviorNode `json:"nodes"`
	Edges      []BehaviorEdge `json:"edges"`
}

type BehaviorNode struct {
	ID    int           `json:"id"`
	Depth int           `json:"depth"`
	State BehaviorState `json:"state"`
}

type BehaviorState struct {
	Running             bool     `json:"running"`
	PaymentState        string   `json:"payment_state"`
	CapturedOnce        bool     `json:"captured_once"`
	PendingFulfillment  bool     `json:"pending_fulfillment"`
	UntrustedAccepted   bool     `json:"untrusted_accepted"`
	WrongAmountAccepted bool     `json:"wrong_amount_accepted"`
	WrongOrderAccepted  bool     `json:"wrong_order_accepted"`
	EffectCount         int      `json:"effect_count"`
	EffectAttempt       int      `json:"effect_attempt"`
	SeenEvents          []string `json:"seen_events"`
	FulfillmentKeys     []string `json:"fulfillment_keys"`
}

type BehaviorEdge struct {
	From   int    `json:"from"`
	To     int    `json:"to"`
	Action Action `json:"action"`
}

// CompileBehaviorGraph enumerates legal actions up to the selected depth.
func CompileBehaviorGraph(program string, maxActions int) (BehaviorGraph, error) {
	if !supportedProgram(program) {
		return BehaviorGraph{}, fmt.Errorf("unsupported program %q", program)
	}
	if maxActions < 1 || maxActions > MaxGraphActions {
		return BehaviorGraph{}, fmt.Errorf("max actions must be between 1 and %d", MaxGraphActions)
	}

	initial := &runner{
		schedule: Schedule{Name: "payment behavior graph", Program: program, OrderID: "order_graph"},
		running:  true, seen: make(map[string]bool), effects: make(map[string]bool),
	}
	graph := BehaviorGraph{Version: 6, Program: program, MaxActions: maxActions}
	graph.Nodes = append(graph.Nodes, BehaviorNode{ID: 0, State: behaviorState(initial)})
	states := []*runner{initial}
	nodeByState := map[string]int{behaviorStateKey(0, initial): 0}

	for nodeID := 0; nodeID < len(graph.Nodes); nodeID++ {
		node := graph.Nodes[nodeID]
		if node.Depth == maxActions {
			continue
		}
		for _, action := range behaviorActions(program) {
			current := states[nodeID]
			if (action.Type == "restart" && current.running) || (action.Type != "restart" && !current.running) {
				continue
			}
			next := cloneRunner(current)
			if err := next.run(action); err != nil {
				continue
			}
			depth := node.Depth + 1
			key := behaviorStateKey(depth, next)
			to, exists := nodeByState[key]
			if !exists {
				to = len(graph.Nodes)
				nodeByState[key] = to
				graph.Nodes = append(graph.Nodes, BehaviorNode{ID: to, Depth: depth, State: behaviorState(next)})
				states = append(states, next)
			}
			graph.Edges = append(graph.Edges, BehaviorEdge{From: nodeID, To: to, Action: action})
		}
	}
	return graph, nil
}

func behaviorActions(program string) []Action {
	if program == ProgramRetryForever || program == ProgramRetryBounded {
		return []Action{
			{Type: "deliver", EventID: "event_captured", Status: "captured"},
			{Type: "fulfill", Response: "http-500"},
		}
	}
	actions := []Action{
		{Type: "deliver", EventID: "event_captured", Status: "captured"},
		{Type: "deliver", EventID: "event_stale", Status: "failed"},
		{Type: "fulfill", Response: "ok"},
		{Type: "restart"},
	}
	if program == ProgramAcceptWrongAmount || program == ProgramCorrectAmount {
		actions[0].Amount = ExpectedPaymentAmount
	}
	if program == ProgramCorrect || program == ProgramNewKeyOnRetry {
		actions = slices.Insert(actions, 2, Action{Type: "fulfill", Response: "lost"})
	}
	if program == ProgramFulfillBeforeDedup {
		actions = slices.Insert(actions, 1, Action{
			Type: "deliver", EventID: "event_captured", Status: "captured", CrashAt: "after_fulfillment",
		})
	}
	if program == ProgramConcurrentBeforeClaim || program == ProgramCorrectConcurrency {
		actions = append(actions, Action{Type: "deliver", EventID: "event_parallel", Status: "captured", Parallel: 2})
	}
	if program == ProgramAcceptWrongAmount || program == ProgramCorrectAmount {
		actions = append(actions, Action{Type: "deliver", EventID: "event_wrong_amount", Status: "captured", Amount: 1})
	}
	if program == ProgramAcceptWrongOrder || program == ProgramCorrectOrder {
		actions = append(actions, Action{Type: "deliver", EventID: "event_wrong_order", Status: "captured", PaymentOrderID: "order_other"})
	}
	switch program {
	case ProgramNewKeyOnTimeout, ProgramCorrectNetwork:
		actions = append(actions, Action{Type: "fulfill", Response: "timeout"})
	case ProgramNewKeyOnReset:
		actions = append(actions, Action{Type: "fulfill", Response: "connection-reset"})
	case ProgramNewKeyOnServerError:
		actions = append(actions, Action{Type: "fulfill", Response: "http-500"})
	case ProgramNewKeyOnDBConflict, ProgramCorrectDBConflict:
		actions = append(actions, Action{Type: "fulfill", Response: "db-conflict"})
	case ProgramNewKeyOnDBDeadlock, ProgramCorrectDBDeadlock:
		actions = append(actions, Action{Type: "fulfill", Response: "db-deadlock"})
	case ProgramAcceptUntrusted, ProgramCorrectSecurity:
		actions = append(actions,
			Action{Type: "deliver", EventID: "event_untrusted", Status: "captured", Trust: "invalid-signature"},
			Action{Type: "deliver", EventID: "event_tampered", Status: "captured", Trust: "tampered-body"},
		)
	}
	return actions
}

func cloneRunner(source *runner) *runner {
	clone := *source
	clone.seen = make(map[string]bool, len(source.seen))
	for key, value := range source.seen {
		clone.seen[key] = value
	}
	clone.effects = make(map[string]bool, len(source.effects))
	for key, value := range source.effects {
		clone.effects[key] = value
	}
	clone.trace = nil
	return &clone
}

func behaviorState(r *runner) BehaviorState {
	state := BehaviorState{
		Running: r.running, PaymentState: r.state, CapturedOnce: r.capturedOnce,
		PendingFulfillment: r.pendingEffect, UntrustedAccepted: r.untrusted, WrongAmountAccepted: r.wrongAmount,
		WrongOrderAccepted: r.wrongOrder,
		EffectCount:        r.effectCount, EffectAttempt: r.effectAttempt,
		SeenEvents: []string{}, FulfillmentKeys: []string{},
	}
	for event := range r.seen {
		state.SeenEvents = append(state.SeenEvents, event)
	}
	for key := range r.effects {
		state.FulfillmentKeys = append(state.FulfillmentKeys, key)
	}
	slices.Sort(state.SeenEvents)
	slices.Sort(state.FulfillmentKeys)
	return state
}

func behaviorStateKey(depth int, r *runner) string {
	return fmt.Sprintf("%d:%#v", depth, behaviorState(r))
}
