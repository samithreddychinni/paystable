package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/IDEA-Amrita/paystable/internal/verification"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "demo" {
		report, err := verification.RunDemo()
		if err != nil {
			fmt.Fprintln(os.Stderr, "run demo:", err)
			os.Exit(1)
		}
		writeJSON(report)
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "cross-adapter" {
		report, err := verification.RunCrossAdapterReport()
		if err != nil {
			fmt.Fprintln(os.Stderr, "run cross-adapter control:", err)
			os.Exit(1)
		}
		writeJSON(report)
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "invariants" {
		report, err := verification.RunInvariantContractReport()
		if err != nil {
			fmt.Fprintln(os.Stderr, "check invariant contracts:", err)
			os.Exit(1)
		}
		writeJSON(report)
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "model-evidence" {
		report, err := verification.RunModelEvidenceReport(50)
		if err != nil {
			fmt.Fprintln(os.Stderr, "create model evidence:", err)
			os.Exit(1)
		}
		writeJSON(report)
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "repair" {
		report, err := verification.VerifyTerminalStateRepair(verification.GenerateProgramCorpus())
		if err != nil {
			fmt.Fprintln(os.Stderr, "verify repair:", err)
			os.Exit(1)
		}
		writeJSON(report)
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "external-transfer" {
		report, err := verification.RunExternalTransferReport(verification.GenerateProgramCorpus())
		if err != nil {
			fmt.Fprintln(os.Stderr, "run external transfer report:", err)
			os.Exit(1)
		}
		writeJSON(report)
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "replay-window" {
		report, err := verification.RunReplayWindowReport(verification.GenerateProgramCorpus())
		if err != nil {
			fmt.Fprintln(os.Stderr, "run replay-window report:", err)
			os.Exit(1)
		}
		writeJSON(report)
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "replay-window-v3" {
		report, err := verification.RunReplayV3Report(verification.GenerateProgramCorpus())
		if err != nil {
			fmt.Fprintln(os.Stderr, "run Scout v3 replay report:", err)
			os.Exit(1)
		}
		writeJSON(report)
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "closed-loop" {
		budget, err := strconv.Atoi(os.Args[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, "budget must be an integer")
			os.Exit(2)
		}
		report, err := verification.RunClosedLoopReport(verification.GenerateProgramCorpus(), budget)
		if err != nil {
			fmt.Fprintln(os.Stderr, "run closed-loop Scout:", err)
			os.Exit(1)
		}
		writeJSON(report)
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "scout" {
		budget, err := strconv.Atoi(os.Args[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, "budget must be an integer")
			os.Exit(2)
		}
		report, err := verification.RunScoutReport(verification.GenerateProgramCorpus(), budget)
		if err != nil {
			fmt.Fprintln(os.Stderr, "run Scout:", err)
			os.Exit(1)
		}
		writeJSON(report)
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "prior-free" {
		budget, err := strconv.Atoi(os.Args[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, "budget must be an integer")
			os.Exit(2)
		}
		report, err := verification.RunPriorFreeScoutReport(verification.GenerateProgramCorpus(), budget)
		if err != nil {
			fmt.Fprintln(os.Stderr, "run prior-free Scout:", err)
			os.Exit(1)
		}
		writeJSON(report)
		return
	}
	if len(os.Args) == 4 && os.Args[1] == "baselines" {
		budget, err := strconv.Atoi(os.Args[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, "budget must be an integer")
			os.Exit(2)
		}
		seed, err := strconv.ParseInt(os.Args[3], 10, 64)
		if err != nil {
			fmt.Fprintln(os.Stderr, "seed must be an integer")
			os.Exit(2)
		}
		report, err := verification.RunBaselineReport(verification.GenerateProgramCorpus(), budget, seed)
		if err != nil {
			fmt.Fprintln(os.Stderr, "run baselines:", err)
			os.Exit(1)
		}
		writeJSON(report)
		return
	}
	if len(os.Args) == 4 && os.Args[1] == "prior-free-stress" {
		budget, err := strconv.Atoi(os.Args[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, "budget must be an integer")
			os.Exit(2)
		}
		seed, err := strconv.ParseInt(os.Args[3], 10, 64)
		if err != nil {
			fmt.Fprintln(os.Stderr, "seed must be an integer")
			os.Exit(2)
		}
		report, err := verification.RunPriorFreeStressReport(verification.GenerateProgramCorpus(), budget, seed)
		if err != nil {
			fmt.Fprintln(os.Stderr, "run prior-free stress report:", err)
			os.Exit(1)
		}
		writeJSON(report)
		return
	}
	if len(os.Args) == 4 && os.Args[1] == "independent" {
		budget, err := strconv.Atoi(os.Args[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, "budget must be an integer")
			os.Exit(2)
		}
		seed, err := strconv.ParseInt(os.Args[3], 10, 64)
		if err != nil {
			fmt.Fprintln(os.Stderr, "seed must be an integer")
			os.Exit(2)
		}
		report, err := verification.RunIndependentReport(verification.GenerateProgramCorpus(), budget, seed)
		if err != nil {
			fmt.Fprintln(os.Stderr, "run independent benchmark:", err)
			os.Exit(1)
		}
		writeJSON(report)
		return
	}
	if len(os.Args) == 4 && os.Args[1] == "heldout" {
		budget, err := strconv.Atoi(os.Args[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, "budget must be an integer")
			os.Exit(2)
		}
		seed, err := strconv.ParseInt(os.Args[3], 10, 64)
		if err != nil {
			fmt.Fprintln(os.Stderr, "seed must be an integer")
			os.Exit(2)
		}
		report, err := verification.RunHeldOutReport(verification.GenerateProgramCorpus(), budget, seed)
		if err != nil {
			fmt.Fprintln(os.Stderr, "run held-out benchmark:", err)
			os.Exit(1)
		}
		writeJSON(report)
		return
	}
	if len(os.Args) == 5 && os.Args[1] == "performance" {
		budget, err := strconv.Atoi(os.Args[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, "budget must be an integer")
			os.Exit(2)
		}
		seed, err := strconv.ParseInt(os.Args[3], 10, 64)
		if err != nil {
			fmt.Fprintln(os.Stderr, "seed must be an integer")
			os.Exit(2)
		}
		repetitions, err := strconv.Atoi(os.Args[4])
		if err != nil {
			fmt.Fprintln(os.Stderr, "repetitions must be an integer")
			os.Exit(2)
		}
		report, err := verification.RunPerformanceReport(verification.GenerateProgramCorpus(), budget, seed, repetitions)
		if err != nil {
			fmt.Fprintln(os.Stderr, "measure performance:", err)
			os.Exit(1)
		}
		writeJSON(report)
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "corpus" {
		writeJSON(verification.GenerateProgramCorpus())
		return
	}
	if len(os.Args) == 4 && os.Args[1] == "graph" {
		maxActions, err := strconv.Atoi(os.Args[3])
		if err != nil {
			fmt.Fprintln(os.Stderr, "max actions must be an integer")
			os.Exit(2)
		}
		graph, err := verification.CompileBehaviorGraph(os.Args[2], maxActions)
		if err != nil {
			fmt.Fprintln(os.Stderr, "compile graph:", err)
			os.Exit(1)
		}
		writeJSON(graph)
		return
	}
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: lab SCHEDULE.json\n       lab baselines BUDGET SEED\n       lab closed-loop BUDGET\n       lab corpus\n       lab cross-adapter\n       lab demo\n       lab external-transfer\n       lab graph PROGRAM MAX_ACTIONS\n       lab heldout BUDGET SEED\n       lab independent BUDGET SEED\n       lab invariants\n       lab model-evidence\n       lab performance BUDGET SEED REPETITIONS\n       lab prior-free BUDGET\n       lab prior-free-stress BUDGET SEED\n       lab repair\n       lab replay-window\n       lab replay-window-v3\n       lab scout BUDGET")
		os.Exit(2)
	}
	file, err := os.Open(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "read schedule:", err)
		os.Exit(1)
	}
	defer file.Close()

	var schedule verification.Schedule
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&schedule); err != nil {
		fmt.Fprintln(os.Stderr, "decode schedule:", err)
		os.Exit(1)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		fmt.Fprintln(os.Stderr, "decode schedule: extra JSON data")
		os.Exit(1)
	}
	result, err := verification.Run(schedule)
	if err != nil {
		fmt.Fprintln(os.Stderr, "run schedule:", err)
		os.Exit(1)
	}
	writeJSON(result)
}

func writeJSON(value any) {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		fmt.Fprintln(os.Stderr, "write result:", err)
		os.Exit(1)
	}
}
