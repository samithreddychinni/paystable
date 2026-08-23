package verification

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"

	"github.com/IDEA-Amrita/paystable/internal/gateway/razorpay"
)

type DemoReport struct {
	Version                 int               `json:"version"`
	Mode                    string            `json:"mode"`
	Seed                    int64             `json:"seed"`
	Budget                  int               `json:"budget"`
	RazorpaySignature       DemoSignature     `json:"razorpay_signature"`
	Programs                []DemoProgram     `json:"programs"`
	Search                  []BaselineSummary `json:"search"`
	FeaturedFinding         *DemoFinding      `json:"featured_finding"`
	ScoutModelBytes         int               `json:"scout_model_bytes"`
	RepairCheckedSchedules  int               `json:"repair_checked_schedules"`
	RepairRemainingFailures int               `json:"repair_remaining_failures"`
	Passed                  bool              `json:"passed"`
}

type DemoFinding struct {
	Schedule  Schedule        `json:"schedule"`
	Result    Result          `json:"result"`
	Reduction ReductionReport `json:"reduction"`
}

type DemoSignature struct {
	ValidBodyAccepted bool `json:"valid_body_accepted"`
	TamperedRejected  bool `json:"tampered_body_rejected"`
}

type DemoProgram struct {
	Program         string `json:"program"`
	Expected        string `json:"expected_invariant,omitempty"`
	GraphNodes      int    `json:"graph_nodes"`
	GraphEdges      int    `json:"graph_edges"`
	OriginalActions int    `json:"original_actions"`
	ReducedActions  int    `json:"reduced_actions"`
	Deterministic   bool   `json:"deterministic"`
}

// RunDemo executes the deterministic verification story without external services.
func RunDemo() (DemoReport, error) {
	const (
		budget = 50
		seed   = int64(7)
	)
	report := DemoReport{Version: 1, Mode: "deterministic-in-process", Seed: seed, Budget: budget}
	body := []byte(`{"event":"payment.captured","payload":{"payment":{"entity":{"status":"captured"}}}}`)
	secret := "demo_webhook_secret"
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	signature := hex.EncodeToString(mac.Sum(nil))
	report.RazorpaySignature.ValidBodyAccepted = razorpay.VerifyWebhookSignature(body, signature, secret)
	tamperedBody := append(append([]byte{}, body...), ' ')
	report.RazorpaySignature.TamperedRejected = !razorpay.VerifyWebhookSignature(tamperedBody, signature, secret)
	if !report.RazorpaySignature.ValidBodyAccepted || !report.RazorpaySignature.TamperedRejected {
		return DemoReport{}, fmt.Errorf("Razorpay raw-body signature check failed")
	}

	corpus := GenerateProgramCorpus()
	for _, program := range corpus.Programs {
		first, err := Run(program.GroundTruth)
		if err != nil {
			return DemoReport{}, err
		}
		second, err := Run(program.GroundTruth)
		if err != nil {
			return DemoReport{}, err
		}
		if first.FinalState != program.ExpectedFinalState || first.EffectCount != program.ExpectedEffectCount {
			return DemoReport{}, fmt.Errorf("program %s did not match its ground truth", program.Program)
		}
		if (program.ExpectedInvariant == "" && len(first.Violations) != 0) ||
			(program.ExpectedInvariant != "" && !resultHasInvariant(first, program.ExpectedInvariant)) {
			return DemoReport{}, fmt.Errorf("program %s produced an incorrect invariant result", program.Program)
		}
		graph, err := CompileBehaviorGraph(program.Program, corpus.MaxScheduleActions)
		if err != nil {
			return DemoReport{}, err
		}
		programReport := DemoProgram{
			Program: program.Program, Expected: program.ExpectedInvariant,
			GraphNodes: len(graph.Nodes), GraphEdges: len(graph.Edges),
			OriginalActions: len(program.GroundTruth.Actions), ReducedActions: len(program.GroundTruth.Actions),
			Deterministic: reflect.DeepEqual(first, second),
		}
		if !programReport.Deterministic {
			return DemoReport{}, fmt.Errorf("program %s did not replay deterministically", program.Program)
		}
		if program.ExpectedInvariant != "" {
			reduced, reduction, err := Reduce(program.GroundTruth, program.ExpectedInvariant, Run)
			if err != nil {
				return DemoReport{}, err
			}
			programReport.ReducedActions = len(reduced.Actions)
			if program.Program == ProgramFulfillBeforeDedup {
				result, err := Run(reduced)
				if err != nil {
					return DemoReport{}, err
				}
				report.FeaturedFinding = &DemoFinding{Schedule: reduced, Result: result, Reduction: reduction}
			}
		}
		report.Programs = append(report.Programs, programReport)
	}

	baselines, err := RunBaselineReport(corpus, budget, seed)
	if err != nil {
		return DemoReport{}, err
	}
	report.Search = append(report.Search, baselines.Summary...)
	scout, err := RunScoutReport(corpus, budget)
	if err != nil {
		return DemoReport{}, err
	}
	report.Search = append(report.Search, scout.Summary)
	report.ScoutModelBytes = scout.ModelBytes
	closedLoop, err := RunClosedLoopReport(corpus, budget)
	if err != nil {
		return DemoReport{}, err
	}
	report.Search = append(report.Search, closedLoop.Summary)
	for _, summary := range report.Search {
		if summary.SuccessAt50 != 1 || summary.FalseFindingCount != 0 || summary.DeterministicReplayRate != 1 {
			return DemoReport{}, fmt.Errorf("search method %s did not pass", summary.Method)
		}
	}
	repair, err := VerifyTerminalStateRepair(corpus)
	if err != nil {
		return DemoReport{}, err
	}
	report.RepairCheckedSchedules = repair.CheckedSchedules
	report.RepairRemainingFailures = repair.RemainingViolations
	report.Passed = true
	return report, nil
}
