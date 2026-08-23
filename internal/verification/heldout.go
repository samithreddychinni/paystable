package verification

import "github.com/IDEA-Amrita/paystable/testkit/heldoutmerchant"

// RunHeldOutReport evaluates the frozen merchant set without changing Scout.
func RunHeldOutReport(training ProgramCorpus, budget int, seed int64) (IndependentReport, error) {
	return runImplementationReport(
		training, budget, seed,
		"frozen-complete-implementation-held-out",
		"post-feature-freeze-held-out-merchants",
		heldoutCases(),
	)
}

func heldoutCases() []independentCase {
	captured := Action{Type: "deliver", EventID: "heldout-captured", Status: "captured"}
	failed := Action{Type: "deliver", EventID: "heldout-failed", Status: "failed"}
	crashCaptured := captured
	crashCaptured.CrashAt = "after_fulfillment"
	invalidCaptured := Action{Type: "deliver", EventID: "heldout-invalid", Status: "captured", Trust: "invalid-signature"}
	tamperedCaptured := Action{Type: "deliver", EventID: "heldout-tampered", Status: "captured", Trust: "tampered-body"}
	timeout := Action{Type: "fulfill", Response: "timeout"}
	lost := Action{Type: "fulfill", Response: "lost"}
	ok := Action{Type: "fulfill", Response: "ok"}
	restart := Action{Type: "restart"}

	return []independentCase{
		heldoutCase("heldout-dedup-unsafe", "deduplication-order", InvariantFulfillmentAtMostOnce,
			[]Action{captured, failed, crashCaptured, restart}, heldoutmerchant.NewUnsafeDedup),
		heldoutCase("heldout-dedup-safe", "correct-deduplication", "",
			[]Action{captured, failed, crashCaptured, restart}, heldoutmerchant.NewSafeDedup),
		heldoutCase("heldout-state-unsafe", "terminal-state", InvariantTerminalStateStable,
			[]Action{captured, failed}, heldoutmerchant.NewUnsafeState),
		heldoutCase("heldout-state-safe", "correct-terminal", "",
			[]Action{captured, failed}, heldoutmerchant.NewSafeState),
		heldoutCase("heldout-trust-unsafe", "webhook-authentication", InvariantTrustedEventsOnly,
			[]Action{captured, failed, invalidCaptured, tamperedCaptured}, heldoutmerchant.NewUnsafeTrust),
		heldoutCase("heldout-trust-safe", "correct-authentication", "",
			[]Action{captured, failed, invalidCaptured, tamperedCaptured}, heldoutmerchant.NewSafeTrust),
		heldoutCase("heldout-retry-unsafe", "retry-idempotency", InvariantFulfillmentAtMostOnce,
			[]Action{captured, failed, timeout, lost, ok}, heldoutmerchant.NewUnsafeRetry),
		heldoutCase("heldout-retry-safe", "correct-retry", "",
			[]Action{captured, failed, timeout, lost, ok}, heldoutmerchant.NewSafeRetry),
	}
}

func heldoutCase(program, family, invariant string, actions []Action, create func() heldoutmerchant.Merchant) independentCase {
	return independentCase{
		program: ProgramCase{Program: program, Family: family, ExpectedInvariant: invariant},
		actions: actions,
		execute: func(schedule Schedule) (Result, error) {
			return runHeldOutMerchant(schedule, create())
		},
	}
}

func runHeldOutMerchant(schedule Schedule, merchant heldoutmerchant.Merchant) (Result, error) {
	if err := validateIndependent(schedule); err != nil {
		return Result{}, err
	}
	trace := make([]TraceEntry, 0, len(schedule.Actions))
	for _, action := range schedule.Actions {
		traceAction, detail := action.Type, "action completed"
		switch action.Type {
		case "deliver":
			trusted := action.Trust == "" || action.Trust == "valid"
			accepted := merchant.Deliver(heldoutmerchant.Event{
				ID: action.EventID, Status: action.Status, SignatureValid: trusted,
			}, action.CrashAt == "after_fulfillment")
			if accepted && !trusted {
				traceAction = "untrusted_accept"
			}
			if !accepted {
				detail = "event rejected"
			}
		case "fulfill":
			merchant.Fulfill(action.Response)
		case "restart":
			merchant.Restart()
		}
		snapshot := merchant.Snapshot()
		trace = append(trace, TraceEntry{
			Sequence: len(trace) + 1, Action: traceAction, Detail: detail,
			State: snapshot.State, EffectCount: snapshot.EffectCount,
		})
	}
	snapshot := merchant.Snapshot()
	return ResultFor(schedule, snapshot.State, snapshot.CapturedOnce, snapshot.EffectCount, trace), nil
}
