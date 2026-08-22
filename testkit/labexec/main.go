package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
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
	Version       int                           `json:"version"`
	Schedule      verification.Schedule         `json:"schedule"`
	Deterministic bool                          `json:"deterministic"`
	Executions    int                           `json:"executions"`
	Result        verification.Result           `json:"result"`
	Reduction     *verification.ReductionReport `json:"reduction,omitempty"`
}

type executor struct {
	baseURL       string
	webhookSecret string
	http          *http.Client
}

func main() {
	if len(os.Args) != 3 && len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: labexec SCHEDULE.json ARTIFACT.json [INVARIANT]")
		os.Exit(2)
	}
	schedule, err := readSchedule(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	e := &executor{
		baseURL: envOr("LAB_URL", "http://localhost:9093"), webhookSecret: envOr("LAB_WEBHOOK_SECRET", "lab-webhook-secret"),
		http: &http.Client{Timeout: 3 * time.Second},
	}
	var reduction *verification.ReductionReport
	if len(os.Args) == 4 {
		reduced, report, err := verification.Reduce(schedule, os.Args[3], e.run)
		if err != nil {
			fmt.Fprintln(os.Stderr, "reduce schedule:", err)
			os.Exit(1)
		}
		schedule, reduction = reduced, &report
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
	data, err := json.MarshalIndent(artifact{Version: 1, Schedule: schedule, Deterministic: true, Executions: 2, Result: first, Reduction: reduction}, "", "  ")
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
	if err := e.waitReady(); err != nil {
		return verification.Result{}, err
	}
	if err := e.post("/reset", schedule, ""); err != nil {
		return verification.Result{}, err
	}
	stopped := false
	for i, action := range schedule.Actions {
		if stopped && action.Type != "restart" {
			return verification.Result{}, fmt.Errorf("action %d must restart the stopped merchant", i+1)
		}
		switch action.Type {
		case "deliver":
			err := e.deliver(action, schedule.Program)
			if err != nil {
				return verification.Result{}, fmt.Errorf("action %d: %w", i+1, err)
			}
			stopped = action.CrashAt != ""
		case "fulfill":
			err := e.post("/fulfill", action, action.Response)
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
	want := make(map[string]int)
	for _, action := range schedule.Actions {
		if action.CrashAt != "" {
			want["crash"]++
		}
		if action.Response != "" && action.Response != "ok" {
			want[verification.ResponseTraceAction(action.Response)]++
		}
		if action.Trust != "" && action.Trust != "valid" && schedule.Program == verification.ProgramAcceptUntrusted {
			want["untrusted_accept"]++
		}
	}
	got := make(map[string]int)
	for _, entry := range result.Trace {
		if _, tracked := want[entry.Action]; tracked {
			got[entry.Action]++
		}
	}
	for action, count := range want {
		if got[action] != count {
			return fmt.Errorf("trace has %d %s entries, want %d", got[action], action, count)
		}
	}
	return nil
}

func (e *executor) deliver(action verification.Action, program string) error {
	copies := action.Parallel
	if copies == 0 {
		copies = 1
	}
	if copies == 1 {
		return e.deliverOnce(action, program)
	}
	start := make(chan struct{})
	results := make(chan error, copies)
	for range copies {
		go func() {
			<-start
			results <- e.deliverOnce(action, program)
		}()
	}
	close(start)
	for range copies {
		if err := <-results; err != nil {
			return err
		}
	}
	return nil
}

func (e *executor) deliverOnce(action verification.Action, program string) error {
	body, err := json.Marshal(action)
	if err != nil {
		return err
	}
	signature := sign(body, e.webhookSecret)
	switch action.Trust {
	case "missing-signature":
		signature = ""
	case "invalid-signature":
		signature = "00"
	case "tampered-body":
		body = append(body, ' ')
	}
	expected := ""
	if action.CrashAt != "" {
		expected = "disconnect"
	} else if action.Trust != "" && action.Trust != "valid" && program != verification.ProgramAcceptUntrusted {
		expected = "unauthorized"
	}
	return e.postBody("/deliver", body, signature, expected)
}

func (e *executor) post(path string, value any, expected string) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return e.postBody(path, body, "", expected)
}

func (e *executor) postBody(path string, body []byte, signature, expected string) error {
	req, err := http.NewRequest(http.MethodPost, e.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if signature != "" {
		req.Header.Set("X-Lab-Signature", signature)
	}
	resp, err := e.http.Do(req)
	if expected == "disconnect" || expected == "lost" || expected == "timeout" || expected == "connection-reset" {
		if err == nil {
			_ = resp.Body.Close()
			return fmt.Errorf("merchant returned a response for %s", expected)
		}
		return nil
	}
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if expected == "unauthorized" && resp.StatusCode == http.StatusUnauthorized {
		return nil
	}
	if expected == "http-500" && resp.StatusCode == http.StatusInternalServerError {
		return nil
	}
	if expected == "db-conflict" && resp.StatusCode == http.StatusConflict {
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("merchant returned %s: %s", resp.Status, bytes.TrimSpace(message))
	}
	return nil
}

func sign(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
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
