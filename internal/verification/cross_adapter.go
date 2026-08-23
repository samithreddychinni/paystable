package verification

import (
	"fmt"
	"reflect"
)

type GatewayStep struct {
	Type    string `json:"type"`
	EventID string `json:"event_id,omitempty"`
	Status  string `json:"status,omitempty"`
	CrashAt string `json:"crash_at,omitempty"`
}

type CrossAdapterCase struct {
	Name              string        `json:"name"`
	Program           string        `json:"program"`
	ExpectedInvariant string        `json:"expected_invariant,omitempty"`
	RazorpayInput     []GatewayStep `json:"razorpay_input"`
	PayUInput         []GatewayStep `json:"payu_input"`
	NormalizedActions []Action      `json:"normalized_actions"`
	Result            Result        `json:"result"`
	ReplayVerified    bool          `json:"replay_verified"`
}

type CrossAdapterReport struct {
	Version      int                `json:"version"`
	SharedEngine string             `json:"shared_engine"`
	Cases        []CrossAdapterCase `json:"cases"`
	Passed       bool               `json:"passed"`
}

// RunCrossAdapterReport compares equivalent Razorpay and PayU schedules.
func RunCrossAdapterReport() (CrossAdapterReport, error) {
	type testCase struct {
		name, program, invariant, finalState string
		steps                                []GatewayStep
	}
	cases := []testCase{
		{
			name: "duplicate delivery after a crash", program: ProgramFulfillBeforeDedup,
			invariant: InvariantFulfillmentAtMostOnce, finalState: "captured",
			steps: []GatewayStep{
				{Type: "deliver", EventID: "event-captured", Status: "captured", CrashAt: "after_fulfillment"},
				{Type: "restart"},
				{Type: "deliver", EventID: "event-captured", Status: "captured"},
			},
		},
		{
			name: "reordered stale event control", program: ProgramTerminalStable, finalState: "captured",
			steps: []GatewayStep{
				{Type: "deliver", EventID: "event-captured", Status: "captured"},
				{Type: "deliver", EventID: "event-stale", Status: "failed"},
			},
		},
		{
			name: "terminal state regression", program: ProgramTerminalRegression,
			invariant: InvariantTerminalStateStable, finalState: "failed",
			steps: []GatewayStep{
				{Type: "deliver", EventID: "event-captured", Status: "captured"},
				{Type: "deliver", EventID: "event-stale", Status: "failed"},
			},
		},
	}

	report := CrossAdapterReport{Version: 1, SharedEngine: "verification.Run"}
	for _, test := range cases {
		razorpayInput := gatewaySteps("razorpay", test.steps)
		payuInput := gatewaySteps("payu", test.steps)
		razorpayActions, err := normalizeGatewaySteps("razorpay", razorpayInput)
		if err != nil {
			return CrossAdapterReport{}, err
		}
		payuActions, err := normalizeGatewaySteps("payu", payuInput)
		if err != nil {
			return CrossAdapterReport{}, err
		}
		if !reflect.DeepEqual(razorpayActions, payuActions) {
			return CrossAdapterReport{}, fmt.Errorf("case %q produced different normalized actions", test.name)
		}

		razorpaySchedule := Schedule{Name: test.name, Program: test.program, OrderID: "order-cross-adapter", Actions: razorpayActions}
		payuSchedule := razorpaySchedule
		payuSchedule.Actions = payuActions
		result, err := Run(razorpaySchedule)
		if err != nil {
			return CrossAdapterReport{}, err
		}
		payuResult, err := Run(payuSchedule)
		if err != nil {
			return CrossAdapterReport{}, err
		}
		replay, err := Run(razorpaySchedule)
		if err != nil {
			return CrossAdapterReport{}, err
		}
		if !reflect.DeepEqual(result, payuResult) || !reflect.DeepEqual(result, replay) || result.FinalState != test.finalState || (test.invariant == "" && len(result.Violations) != 0) || (test.invariant != "" && !resultHasInvariant(result, test.invariant)) {
			return CrossAdapterReport{}, fmt.Errorf("case %q did not produce its expected result", test.name)
		}
		report.Cases = append(report.Cases, CrossAdapterCase{
			Name: test.name, Program: test.program, ExpectedInvariant: test.invariant,
			RazorpayInput: razorpayInput, PayUInput: payuInput, NormalizedActions: razorpayActions,
			Result: result, ReplayVerified: true,
		})
	}
	report.Passed = true
	return report, nil
}

func gatewaySteps(gateway string, steps []GatewayStep) []GatewayStep {
	out := make([]GatewayStep, len(steps))
	for i, step := range steps {
		out[i] = step
		if step.Type != "deliver" || gateway == "razorpay" {
			continue
		}
		if step.Status == "captured" {
			out[i].Status = "success"
		} else if step.Status == "failed" {
			out[i].Status = "failure"
		}
	}
	return out
}

func normalizeGatewaySteps(gateway string, steps []GatewayStep) ([]Action, error) {
	actions := make([]Action, len(steps))
	for i, step := range steps {
		status := step.Status
		if step.Type == "deliver" {
			switch gateway + ":" + status {
			case "razorpay:captured", "payu:success":
				status = "captured"
			case "razorpay:failed", "payu:failure":
				status = "failed"
			default:
				return nil, fmt.Errorf("unsupported %s status %q", gateway, status)
			}
		}
		actions[i] = Action{Type: step.Type, EventID: step.EventID, Status: status, CrashAt: step.CrashAt}
	}
	return actions, nil
}
