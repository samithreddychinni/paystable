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
	return ProgramCorpus{Version: 4, MaxScheduleActions: 4, Programs: []ProgramCase{
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
			Family: "concurrent-deduplication", Program: ProgramConcurrentBeforeClaim,
			GroundTruth: Schedule{
				Name: "fulfillment before a concurrent event claim", Program: ProgramConcurrentBeforeClaim, OrderID: "order_corpus_concurrent_1",
				Actions: []Action{{Type: "deliver", EventID: "event_parallel", Status: "captured", Parallel: 2}},
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
			Family: "retry-idempotency", Program: ProgramNewKeyOnTimeout,
			GroundTruth: Schedule{
				Name: "new idempotency key after a response timeout", Program: ProgramNewKeyOnTimeout, OrderID: "order_corpus_3",
				Actions: []Action{
					{Type: "deliver", EventID: "event_captured", Status: "captured"},
					{Type: "fulfill", Response: "timeout"},
					{Type: "fulfill", Response: "ok"},
				},
			},
			ExpectedInvariant: InvariantFulfillmentAtMostOnce, ExpectedFinalState: "captured", ExpectedEffectCount: 2,
		},
		{
			Family: "retry-idempotency", Program: ProgramNewKeyOnReset,
			GroundTruth: Schedule{
				Name: "new idempotency key after a connection reset", Program: ProgramNewKeyOnReset, OrderID: "order_corpus_4",
				Actions: []Action{
					{Type: "deliver", EventID: "event_captured", Status: "captured"},
					{Type: "fulfill", Response: "connection-reset"},
					{Type: "fulfill", Response: "ok"},
				},
			},
			ExpectedInvariant: InvariantFulfillmentAtMostOnce, ExpectedFinalState: "captured", ExpectedEffectCount: 2,
		},
		{
			Family: "retry-idempotency", Program: ProgramNewKeyOnServerError,
			GroundTruth: Schedule{
				Name: "new idempotency key after a server error", Program: ProgramNewKeyOnServerError, OrderID: "order_corpus_5",
				Actions: []Action{
					{Type: "deliver", EventID: "event_captured", Status: "captured"},
					{Type: "fulfill", Response: "http-500"},
					{Type: "fulfill", Response: "ok"},
				},
			},
			ExpectedInvariant: InvariantFulfillmentAtMostOnce, ExpectedFinalState: "captured", ExpectedEffectCount: 2,
		},
		{
			Family: "database-conflict", Program: ProgramNewKeyOnDBConflict,
			GroundTruth: Schedule{
				Name: "new idempotency key after a database conflict", Program: ProgramNewKeyOnDBConflict, OrderID: "order_corpus_db_conflict_1",
				Actions: []Action{
					{Type: "deliver", EventID: "event_captured", Status: "captured"},
					{Type: "fulfill", Response: "db-conflict"},
					{Type: "fulfill", Response: "ok"},
				},
			},
			ExpectedInvariant: InvariantFulfillmentAtMostOnce, ExpectedFinalState: "captured", ExpectedEffectCount: 2,
		},
		{
			Family: "database-deadlock", Program: ProgramNewKeyOnDBDeadlock,
			GroundTruth: Schedule{
				Name: "new idempotency key after a database deadlock", Program: ProgramNewKeyOnDBDeadlock, OrderID: "order_corpus_db_deadlock_1",
				Actions: []Action{
					{Type: "deliver", EventID: "event_captured", Status: "captured"},
					{Type: "fulfill", Response: "db-deadlock"},
					{Type: "fulfill", Response: "ok"},
				},
			},
			ExpectedInvariant: InvariantFulfillmentAtMostOnce, ExpectedFinalState: "captured", ExpectedEffectCount: 2,
		},
		{
			Family: "retry-exhaustion", Program: ProgramRetryForever,
			GroundTruth: Schedule{
				Name: "fulfillment continues after retry exhaustion", Program: ProgramRetryForever, OrderID: "order_corpus_retry_limit_1",
				Actions: []Action{
					{Type: "deliver", EventID: "event_captured", Status: "captured"},
					{Type: "fulfill", Response: "http-500"},
					{Type: "fulfill", Response: "http-500"},
					{Type: "fulfill", Response: "http-500"},
				},
			},
			ExpectedInvariant: InvariantRetryBounded, ExpectedFinalState: "captured", ExpectedEffectCount: 1,
		},
		{
			Family: "terminal-state", Program: ProgramTerminalRegression,
			GroundTruth: Schedule{
				Name: "stale event changes a terminal state", Program: ProgramTerminalRegression, OrderID: "order_corpus_6",
				Actions: []Action{
					{Type: "deliver", EventID: "event_captured", Status: "captured"},
					{Type: "deliver", EventID: "event_stale", Status: "failed"},
				},
			},
			ExpectedInvariant: InvariantTerminalStateStable, ExpectedFinalState: "failed", ExpectedEffectCount: 0,
		},
		{
			Family: "correct-terminal", Program: ProgramTerminalStable,
			GroundTruth: Schedule{
				Name: "delayed stale event keeps the terminal state", Program: ProgramTerminalStable, OrderID: "order_corpus_delayed_1",
				Actions: []Action{
					{Type: "deliver", EventID: "event_captured", Status: "captured"},
					{Type: "deliver", EventID: "event_stale", Status: "failed"},
				},
			},
			ExpectedFinalState: "captured", ExpectedEffectCount: 0,
		},
		{
			Family: "webhook-authentication", Program: ProgramAcceptUntrusted,
			GroundTruth: Schedule{
				Name: "invalid signature changes payment state", Program: ProgramAcceptUntrusted, OrderID: "order_corpus_7",
				Actions: []Action{{
					Type: "deliver", EventID: "event_untrusted", Status: "captured", Trust: "invalid-signature",
				}},
			},
			ExpectedInvariant: InvariantTrustedEventsOnly, ExpectedFinalState: "captured", ExpectedEffectCount: 0,
		},
		{
			Family: "correct", Program: ProgramCorrect,
			GroundTruth: Schedule{
				Name: "correct payment handling", Program: ProgramCorrect, OrderID: "order_corpus_8",
				Actions: []Action{
					{Type: "deliver", EventID: "event_captured", Status: "captured"},
					{Type: "fulfill", Response: "lost"},
					{Type: "fulfill", Response: "ok"},
					{Type: "deliver", EventID: "event_stale", Status: "failed"},
				},
			},
			ExpectedFinalState: "captured", ExpectedEffectCount: 1,
		},
		{
			Family: "correct-security", Program: ProgramCorrectSecurity,
			GroundTruth: Schedule{
				Name: "correct webhook authentication", Program: ProgramCorrectSecurity, OrderID: "order_corpus_9",
				Actions: []Action{
					{Type: "deliver", EventID: "event_untrusted", Status: "captured", Trust: "invalid-signature"},
					{Type: "deliver", EventID: "event_tampered", Status: "captured", Trust: "tampered-body"},
					{Type: "deliver", EventID: "event_captured", Status: "captured"},
					{Type: "fulfill", Response: "ok"},
				},
			},
			ExpectedFinalState: "captured", ExpectedEffectCount: 1,
		},
		{
			Family: "correct-network", Program: ProgramCorrectNetwork,
			GroundTruth: Schedule{
				Name: "correct fulfillment retry after a timeout", Program: ProgramCorrectNetwork, OrderID: "order_corpus_10",
				Actions: []Action{
					{Type: "deliver", EventID: "event_captured", Status: "captured"},
					{Type: "fulfill", Response: "timeout"},
					{Type: "fulfill", Response: "ok"},
				},
			},
			ExpectedFinalState: "captured", ExpectedEffectCount: 1,
		},
		{
			Family: "correct-concurrency", Program: ProgramCorrectConcurrency,
			GroundTruth: Schedule{
				Name: "correct concurrent event claim", Program: ProgramCorrectConcurrency, OrderID: "order_corpus_concurrent_2",
				Actions: []Action{
					{Type: "deliver", EventID: "event_parallel", Status: "captured", Parallel: 2},
					{Type: "fulfill", Response: "ok"},
				},
			},
			ExpectedFinalState: "captured", ExpectedEffectCount: 1,
		},
		{
			Family: "correct-db-conflict", Program: ProgramCorrectDBConflict,
			GroundTruth: Schedule{
				Name: "stable key after a database conflict", Program: ProgramCorrectDBConflict, OrderID: "order_corpus_db_conflict_2",
				Actions: []Action{
					{Type: "deliver", EventID: "event_captured", Status: "captured"},
					{Type: "fulfill", Response: "db-conflict"},
					{Type: "fulfill", Response: "ok"},
				},
			},
			ExpectedFinalState: "captured", ExpectedEffectCount: 1,
		},
		{
			Family: "correct-db-deadlock", Program: ProgramCorrectDBDeadlock,
			GroundTruth: Schedule{
				Name: "stable key after a database deadlock", Program: ProgramCorrectDBDeadlock, OrderID: "order_corpus_db_deadlock_2",
				Actions: []Action{
					{Type: "deliver", EventID: "event_captured", Status: "captured"},
					{Type: "fulfill", Response: "db-deadlock"},
					{Type: "fulfill", Response: "ok"},
				},
			},
			ExpectedFinalState: "captured", ExpectedEffectCount: 1,
		},
		{
			Family: "correct-retry-exhaustion", Program: ProgramRetryBounded,
			GroundTruth: Schedule{
				Name: "fulfillment stops at the retry limit", Program: ProgramRetryBounded, OrderID: "order_corpus_retry_limit_2",
				Actions: []Action{
					{Type: "deliver", EventID: "event_captured", Status: "captured"},
					{Type: "fulfill", Response: "http-500"},
					{Type: "fulfill", Response: "http-500"},
				},
			},
			ExpectedFinalState: "captured", ExpectedEffectCount: 1,
		},
	}}
}
