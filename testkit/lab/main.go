package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/IDEA-Amrita/paystable/internal/verification"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: lab SCHEDULE.json")
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
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		fmt.Fprintln(os.Stderr, "write result:", err)
		os.Exit(1)
	}
}
