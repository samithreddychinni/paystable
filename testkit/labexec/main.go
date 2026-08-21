package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/IDEA-Amrita/paystable/internal/verification"
)

type artifact struct {
	Version       int                   `json:"version"`
	Schedule      verification.Schedule `json:"schedule"`
	Deterministic bool                  `json:"deterministic"`
	Executions    int                   `json:"executions"`
	Result        verification.Result   `json:"result"`
}

type executor struct {
	baseURL string
	http    *http.Client
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: labexec SCHEDULE.json ARTIFACT.json")
		os.Exit(2)
	}
	schedule, err := readSchedule(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	e := &executor{
		baseURL: envOr("LAB_URL", "http://localhost:9093"),
		http:    &http.Client{Timeout: 3 * time.Second},
	}
	first, err := e.run(schedule)
	if err != nil {
		fmt.Fprintln(os.Stderr, "first execution:", err)
		os.Exit(1)
	}
	second, err := e.run(schedule)
	if err != nil {
		fmt.Fprintln(os.Stderr, "replay execution:", err)
		os.Exit(1)
	}
	if !reflect.DeepEqual(first, second) {
		fmt.Fprintln(os.Stderr, "replay result does not match the first execution")
		os.Exit(1)
	}
	data, err := json.MarshalIndent(artifact{Version: 1, Schedule: schedule, Deterministic: true, Executions: 2, Result: first}, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "encode artifact:", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(filepath.Dir(os.Args[2]), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "create artifact directory:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(os.Args[2], append(data, '\n'), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "write artifact:", err)
		os.Exit(1)
	}
	fmt.Println(os.Args[2])
}

func (e *executor) run(schedule verification.Schedule) (verification.Result, error) {
	if err := e.post("/reset", schedule, false); err != nil {
		return verification.Result{}, err
	}
	stopped := false
	for i, action := range schedule.Actions {
		if stopped && action.Type != "restart" {
			return verification.Result{}, fmt.Errorf("action %d must restart the stopped merchant", i+1)
		}
		switch action.Type {
		case "deliver":
			err := e.post("/deliver", action, action.CrashAt != "")
			if err != nil {
				return verification.Result{}, fmt.Errorf("action %d: %w", i+1, err)
			}
			stopped = action.CrashAt != ""
		case "fulfill":
			err := e.post("/fulfill", action, action.Response == "lost")
			if err != nil {
				return verification.Result{}, fmt.Errorf("action %d: %w", i+1, err)
			}
			if action.Response == "lost" {
				if err := e.waitReady(); err != nil {
					return verification.Result{}, err
				}
			}
		case "restart":
			if !stopped {
				return verification.Result{}, fmt.Errorf("action %d restarts a running merchant", i+1)
			}
			if err := e.waitReady(); err != nil {
				return verification.Result{}, err
			}
			stopped = false
		}
	}
	if stopped {
		return verification.Result{}, fmt.Errorf("schedule ended while the merchant was stopped")
	}
	var result verification.Result
	if err := e.get("/result", &result); err != nil {
		return verification.Result{}, err
	}
	if err := verifyTrace(schedule, result); err != nil {
		return verification.Result{}, err
	}
	return result, nil
}

func verifyTrace(schedule verification.Schedule, result verification.Result) error {
	wantCrash, wantLost, gotCrash, gotLost := 0, 0, 0, 0
	for _, action := range schedule.Actions {
		if action.CrashAt != "" {
			wantCrash++
		}
		if action.Response == "lost" {
			wantLost++
		}
	}
	for _, entry := range result.Trace {
		switch entry.Action {
		case "crash":
			gotCrash++
		case "response_lost":
			gotLost++
		}
	}
	if gotCrash != wantCrash || gotLost != wantLost {
		return fmt.Errorf("trace has %d crashes and %d lost responses, want %d and %d", gotCrash, gotLost, wantCrash, wantLost)
	}
	return nil
}

func (e *executor) post(path string, value any, expectDisconnect bool) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, e.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.http.Do(req)
	if expectDisconnect {
		if err == nil {
			_ = resp.Body.Close()
			return fmt.Errorf("merchant did not stop at the checkpoint")
		}
		return nil
	}
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("merchant returned %s: %s", resp.Status, bytes.TrimSpace(message))
	}
	return nil
}

func (e *executor) get(path string, value any) error {
	resp, err := e.http.Get(e.baseURL + path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("merchant returned %s", resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(value)
}

func (e *executor) waitReady() error {
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := e.http.Get(e.baseURL + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusNoContent {
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("merchant did not restart within 20 seconds")
}

func readSchedule(path string) (verification.Schedule, error) {
	file, err := os.Open(path)
	if err != nil {
		return verification.Schedule{}, fmt.Errorf("read schedule: %w", err)
	}
	defer file.Close()
	var schedule verification.Schedule
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&schedule); err != nil {
		return verification.Schedule{}, fmt.Errorf("decode schedule: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return verification.Schedule{}, fmt.Errorf("decode schedule: extra JSON data")
	}
	if err := verification.Validate(schedule); err != nil {
		return verification.Schedule{}, fmt.Errorf("validate schedule: %w", err)
	}
	return schedule, nil
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
