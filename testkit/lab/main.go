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
		fmt.Fprintln(os.Stderr, "usage: lab SCHEDULE.json\n       lab corpus\n       lab graph PROGRAM MAX_ACTIONS")
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
