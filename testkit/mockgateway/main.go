package main

import (
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	mu     sync.RWMutex
	states = map[string]txnState{}
	salt   = os.Getenv("GATEWAY_SALT")
	gkey   = os.Getenv("GATEWAY_KEY")
	psURL  = envOr("PAYSTABLE_URL", "http://localhost:8080")
)

type txnState struct {
	Status      string
	Amount      float64
	FailUntil   time.Time
	ProductInfo string
	FirstName   string
	Email       string
	Udf1        string
}

func main() {
	port := envOr("PORT", "9090")
	mux := http.NewServeMux()
	mux.HandleFunc("POST /script", handleScript)
	mux.HandleFunc("GET /v1/orders/{id}", handleRazorpayOrder)
	mux.HandleFunc("GET /v1/orders/{id}/payments", handleRazorpayPayments)
	mux.HandleFunc("POST /fire-webhook", handleFireWebhook)
	mux.HandleFunc("GET /merchant/postservice.php", handleStatus)
	mux.HandleFunc("POST /merchant/postservice.php", handleStatus)
	slog.Info("mock gateway starting", "port", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		slog.Error("mock gateway failed", "error", err)
		os.Exit(1)
	}
}

func handleScript(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TxnID       string  `json:"txn_id"`
		Status      string  `json:"status"`
		Amount      float64 `json:"amount"`
		FailUntilS  int     `json:"fail_until_s"`
		ProductInfo string  `json:"product_info"`
		FirstName   string  `json:"firstname"`
		Email       string  `json:"email"`
		Udf1        string  `json:"udf1"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s := txnState{
		Status: req.Status, Amount: req.Amount,
		ProductInfo: req.ProductInfo, FirstName: req.FirstName,
		Email: req.Email, Udf1: req.Udf1,
	}
	if req.FailUntilS > 0 {
		s.FailUntil = time.Now().Add(time.Duration(req.FailUntilS) * time.Second)
	}
	mu.Lock()
	states[req.TxnID] = s
	mu.Unlock()
	slog.Info("scripted txn", "txn_id", req.TxnID, "status", req.Status, "fail_until_s", req.FailUntilS)
	w.WriteHeader(http.StatusOK)
}

func handleFireWebhook(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TxnID  string `json:"txn_id"`
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	mu.RLock()
	s, ok := states[req.TxnID]
	mu.RUnlock()
	if !ok {
		http.Error(w, "txn not scripted", http.StatusNotFound)
		return
	}
	if envOr("GATEWAY", "payu") == "razorpay" {
		fireRazorpayWebhook(w, r, req.TxnID, req.Status, s)
		return
	}
	params := map[string]string{
		"key": gkey, "txnid": req.TxnID,
		"amount":      fmt.Sprintf("%.2f", s.Amount/100),
		"productinfo": s.ProductInfo, "firstname": s.FirstName,
		"email": s.Email, "status": req.Status,
		"udf1": s.Udf1, "udf2": "", "udf3": "", "udf4": "", "udf5": "",
		"mihpayid": "mock_" + req.TxnID,
	}
	params["hash"] = responseHash(params, salt)
	resp, err := http.Post(
		psURL+"/webhooks/payu",
		"application/x-www-form-urlencoded",
		strings.NewReader(urlEncode(params)),
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if err := resp.Body.Close(); err != nil {
		slog.Warn("close webhook response", "error", err)
	}
	slog.Info("webhook fired", "txn_id", req.TxnID, "status", req.Status, "code", resp.StatusCode)
	w.WriteHeader(http.StatusOK)
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	// Real PayU sends var1=txnid in a POST form body; fall back to query param for tooling.
	_ = r.ParseForm()
	txnID := r.FormValue("var1")
	if txnID == "" {
		txnID = r.URL.Query().Get("txnid")
	}

	mu.RLock()
	s, ok := states[txnID]
	mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")

	if !ok || txnID == "" {
		slog.Info("status polled", "txn_id", txnID, "returning", "not_found")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":              0,
			"msg":                 "No transaction found",
			"transaction_details": map[string]interface{}{},
		})
		return
	}

	status := s.Status
	if !s.FailUntil.IsZero() && time.Now().Before(s.FailUntil) {
		status = "failed"
	}
	amountStr := fmt.Sprintf("%.2f", s.Amount/100)

	slog.Info("status polled", "txn_id", txnID, "returning", status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status": 1,
		"msg":    "Transaction Fetched Successfully",
		"transaction_details": map[string]interface{}{
			txnID: map[string]interface{}{
				"status": status,
				"amount": amountStr,
				"txnid":  txnID,
			},
		},
	})
}

func responseHash(p map[string]string, s string) string {
	str := fmt.Sprintf("%s|%s||||||%s|%s|%s|%s|%s|%s|%s|%s|%s|%s|%s",
		s, p["status"],
		p["udf5"], p["udf4"], p["udf3"], p["udf2"], p["udf1"],
		p["email"], p["firstname"], p["productinfo"], p["amount"], p["txnid"], p["key"])
	h := sha512.New()
	h.Write([]byte(str))
	return hex.EncodeToString(h.Sum(nil))
}

func urlEncode(params map[string]string) string {
	var parts []string
	for k, v := range params {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, "&")
}

func envOr(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}
