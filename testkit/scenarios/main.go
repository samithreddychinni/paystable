package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

var (
	paystableURL = envOr("PAYSTABLE_URL", "http://localhost:8080")
	gatewayURL   = envOr("GATEWAY_URL", "http://localhost:9090")
	adminKey     = envOr("ADMIN_API_KEY", "test-admin-key")
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: scenarios <scenario>")
		fmt.Println("scenarios: happy-path | false-failure | genuine-failure | amount-mismatch | merchant-offline | duplicate-webhook | razorpay-success | razorpay-failed-webhook")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "razorpay-success":
		razorpaySuccess(false)
	case "razorpay-failed-webhook":
		razorpaySuccess(true)
	case "happy-path":
		happyPath()
	case "false-failure":
		falseFailure()
	case "genuine-failure":
		genuineFailure()
	case "amount-mismatch":
		amountMismatch()
	case "merchant-offline":
		merchantOffline()
	case "duplicate-webhook":
		duplicateWebhook()
	default:
		fmt.Printf("unknown scenario: %s\n", os.Args[1])
		os.Exit(1)
	}
}

func happyPath() {
	txnID := txnid("happy")
	log("=== happy path ===")
	log("1. creating hold for %s", txnID)
	createHold(txnID, 49900)

	log("2. scripting gateway: status=success immediately")
	script(txnID, "success", 49900, 0)

	log("3. gateway fires success webhook")
	fireWebhook(txnID, "success")

	log("4. polling status (expect CONFIRMED within ~15s)")
	pollUntilTerminal(txnID)
}

func falseFailure() {
	txnID := txnid("false-failure")
	log("=== false failure: webhook says FAILED, gateway actually SUCCESS ===")
	log("1. creating hold")
	createHold(txnID, 49900)

	log("2. scripting gateway: return 'failed' for 25s, then switch to 'success'")
	script(txnID, "success", 49900, 25)

	log("3. gateway fires FAILURE webhook (the lying signal)")
	fireWebhook(txnID, "failure")

	log("4. paystable should NOT act on this. polling status...")
	log("   expect: VERIFYING while stabilizer polls, then CONFIRMED after replica catches up")
	pollUntilTerminal(txnID)
}

func genuineFailure() {
	txnID := txnid("genuine-failure")
	log("=== genuine failure ===")
	log("1. creating hold")
	createHold(txnID, 49900)

	log("2. scripting gateway: status=failed always")
	script(txnID, "failed", 49900, 0)

	log("3. gateway fires failure webhook")
	fireWebhook(txnID, "failure")

	log("4. polling status (expect FAILED after stabilizer confirms)")
	pollUntilTerminal(txnID)
}

func amountMismatch() {
	txnID := txnid("amount-mismatch")
	log("=== amount mismatch: gateway reports different amount ===")
	log("1. creating hold for ₹499 (49900 paise)")
	createHold(txnID, 49900)

	log("2. scripting gateway: reports success but amount=25000 (₹250, not ₹499)")
	script(txnID, "success", 25000, 0)

	log("3. gateway fires success webhook")
	fireWebhook(txnID, "success")

	log("4. polling status (expect MISMATCH)")
	pollUntilTerminal(txnID)
}

func merchantOffline() {
	txnID := txnid("merchant-offline")
	log("=== merchant offline: delivery should retry ===")
	log("1. creating hold")
	createHold(txnID, 49900)

	log("2. scripting gateway: success")
	script(txnID, "success", 49900, 0)

	log("3. taking merchant offline")
	post(envOr("MERCHANT_URL", "http://localhost:9091")+"/toggle-offline", nil)

	log("4. firing success webhook")
	fireWebhook(txnID, "success")

	log("5. waiting for hold to confirm (it will, then delivery will retry)")
	pollUntilTerminal(txnID)

	log("6. bringing merchant back online (outbox will deliver on next retry)")
	post(envOr("MERCHANT_URL", "http://localhost:9091")+"/toggle-offline", nil)
	log("   watch paystable logs for 'callback_delivered'")
}

func duplicateWebhook() {
	txnID := txnid("duplicate")
	log("=== duplicate webhook: should deduplicate ===")
	log("1. creating hold")
	createHold(txnID, 49900)

	log("2. scripting gateway: success")
	script(txnID, "success", 49900, 0)

	log("3. firing the same success webhook 3 times")
	fireWebhook(txnID, "success")
	fireWebhook(txnID, "success")
	fireWebhook(txnID, "success")

	log("4. polling status (expect single CONFIRMED, not triple)")
	pollUntilTerminal(txnID)
}

func createHold(txnID string, amount int64) {
	body := map[string]interface{}{
		"txn_id":       txnID,
		"gateway":      envOr("GATEWAY", "payu"),
		"amount":       amount,
		"currency":     "INR",
		"ttl_seconds":  300,
		"callback_url": envOr("MERCHANT_CALLBACK_URL", "http://merchant:9091/callback"),
		"metadata":     map[string]string{"scenario": txnID},
	}
	resp := postJSON(paystableURL+"/api/v1/hold", body, map[string]string{
		"Authorization": "Bearer " + adminKey,
	})
	if resp == "" {
		os.Exit(1)
	}
	log("   hold created: %s", txnID)
}

func script(txnID, status string, amount float64, failUntilS int) {
	postJSON(gatewayURL+"/script", map[string]interface{}{
		"txn_id":       txnID,
		"status":       status,
		"amount":       amount,
		"fail_until_s": failUntilS,
		"product_info": "test-ticket",
		"firstname":    "tester",
		"email":        "test@paystable.dev",
	}, nil)
}

func fireWebhook(txnID, status string) {
	postJSON(gatewayURL+"/fire-webhook", map[string]interface{}{
		"txn_id": txnID,
		"status": status,
	}, nil)
}

func pollUntilTerminal(txnID string) string {
	start := time.Now()
	for {
		time.Sleep(3 * time.Second)
		status := getStatus(txnID)
		elapsed := time.Since(start).Round(time.Second)
		log("   [%s] status: %s", elapsed, status)
		if status == "CONFIRMED" || status == "FAILED" || status == "INDETERMINATE" || status == "MISMATCH" {
			log("   terminal state reached: %s", status)
			return status
		}
		if time.Since(start) > 5*time.Minute {
			log("   timed out waiting for terminal state")
			return "timeout"
		}
	}
}

func getStatus(txnID string) string {
	req, err := http.NewRequest("GET", paystableURL+"/api/v1/transactions/"+txnID+"/status", nil)
	if err != nil {
		return "request error"
	}
	req.Header.Set("Authorization", "Bearer "+adminKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "error: " + err.Error()
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	body, _ := io.ReadAll(resp.Body)
	var m map[string]interface{}
	if err := json.Unmarshal(body, &m); err != nil {
		return string(body)
	}
	if s, ok := m["status"].(string); ok {
		return s
	}
	return string(body)
}

func postJSON(url string, body interface{}, headers map[string]string) string {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log("   POST %s error: %v", url, err)
		return ""
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	respBody, _ := io.ReadAll(resp.Body)
	return string(respBody)
}

func post(url string, body interface{}) {
	postJSON(url, body, nil)
}

func txnid(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixMilli())
}

func log(format string, args ...interface{}) {
	fmt.Printf("[%s] %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
}

func envOr(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}

func razorpaySuccess(failedWebhook bool) {
	if envOr("GATEWAY", "payu") != "razorpay" {
		log("Set GATEWAY=razorpay for this scenario.")
		os.Exit(1)
	}
	txnID := fmt.Sprintf("order_%d", time.Now().UnixNano())
	createHold(txnID, 49900)
	script(txnID, "captured", 49900, 0)
	status := "captured"
	if failedWebhook {
		status = "failed"
	}
	fireWebhook(txnID, status)
	if got := pollUntilTerminal(txnID); got != "CONFIRMED" {
		log("expected CONFIRMED, got %s", got)
		os.Exit(1)
	}
}
