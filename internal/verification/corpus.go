package verification

type ProgramCorpus struct {
	Version            int           `json:"version"`
	MaxScheduleActions int           `json:"max_schedule_actions"`
	Programs           []ProgramCase `json:"programs"`
}

type ProgramCase struct {
	Family              string   `json:"family"`
	Program             string   `json:"program"`
	GroundTruth         Schedule `json:"ground_truth"`
	ExpectedInvariant   string   `json:"expected_invariant,omitempty"`
	ExpectedFinalState  string   `json:"expected_final_state"`
	ExpectedEffectCount int      `json:"expected_effect_count"`
}

// GenerateProgramCorpus returns the executable programs and their canonical schedules.
func GenerateProgramCorpus() ProgramCorpus {
	return ProgramCorpus{Version: 1, MaxScheduleActions: 4, Programs: []ProgramCase{
		{
			Family: "deduplication-order", Program: ProgramFulfillBeforeDedup,
			GroundTruth: Schedule{
				Name: "fulfillment before durable deduplication", Program: ProgramFulfillBeforeDedup, OrderID: "order_corpus_1",
				Actions: []Action{
					{Type: "deliver", EventID: "event_captured", Status: "captured", CrashAt: "after_fulfillment"},
					{Type: "restart"},
					{Type: "deliver", EventID: "event_captured", Status: "captured"},
				},
			},
			ExpectedInvariant: InvariantFulfillmentAtMostOnce, ExpectedFinalState: "captured", ExpectedEffectCount: 2,
		},
		{
			Family: "retry-idempotency", Program: ProgramNewKeyOnRetry,
			GroundTruth: Schedule{
				Name: "new idempotency key after a lost response", Program: ProgramNewKeyOnRetry, OrderID: "order_corpus_2",
				Actions: []Action{
					{Type: "deliver", EventID: "event_captured", Status: "captured"},
					{Type: "fulfill", Response: "lost"},
					{Type: "fulfill", Response: "ok"},
				},
			},
			ExpectedInvariant: InvariantFulfillmentAtMostOnce, ExpectedFinalState: "captured", ExpectedEffectCount: 2,
		},
		{
			Family: "terminal-state", Program: ProgramTerminalRegression,
			GroundTruth: Schedule{
				Name: "stale event changes a terminal state", Program: ProgramTerminalRegression, OrderID: "order_corpus_3",
				Actions: []Action{
					{Type: "deliver", EventID: "event_captured", Status: "captured"},
					{Type: "deliver", EventID: "event_stale", Status: "failed"},
				},
			},
			ExpectedInvariant: InvariantTerminalStateStable, ExpectedFinalState: "failed", ExpectedEffectCount: 0,
		},
		{
			Family: "correct", Program: ProgramCorrect,
			GroundTruth: Schedule{
				Name: "correct payment handling", Program: ProgramCorrect, OrderID: "order_corpus_4",
				Actions: []Action{
					{Type: "deliver", EventID: "event_captured", Status: "captured"},
					{Type: "fulfill", Response: "lost"},
					{Type: "fulfill", Response: "ok"},
					{Type: "deliver", EventID: "event_stale", Status: "failed"},
				},
			},
			ExpectedFinalState: "captured", ExpectedEffectCount: 1,
		},
	}}
}
