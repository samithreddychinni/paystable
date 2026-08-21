package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	DatabaseURL            string
	Gateway                string
	WebhookSecret          string
	GatewayAPIKey          string
	PayuStatusURL          string
	RazorpayKeyID          string
	RazorpayKeySecret      string
	RazorpayWebhookSecret  string
	RazorpayAPIBaseURL     string
	MerchantCallbackSecret string
	AdminAPIKey            string
	SecretEncryptionKey    string
	Port                   string
	StabilizationN         int
	MaxBackoffS            int
	HoldMaxTTLS            int
	LogLevel               string
	DeliveryTimeoutS       int
	DeliveryConcurrency    int
	DeliveryAllowInsecure  bool
}

// 1)StabilizationN:What: number of consecutive agreeing verification polls required to declare a terminal state (default 3)
// 2)MaxBackoffS:What: maximum backoff time in seconds for retry attempts (default 160)// per-attempt cap, not a cumulative cap,eg for 160=>stops at 160s not when cumSum is 160s
// 3)HoldMaxTTLS:What: is the absolute lifetime (seconds) of a hold from creation → expires_at(default 900)
// so when it checking reaches with MaxBackoffS it doesnt expand from there rather maintain there itself.But when cumSum of time taken exceeds HoldMaxTTLS,the checking break.from there it will see last N transactions(StabilizationN) n based on thatitss say whether success or failure
func Load() (*Config, error) {
	loadDotEnv()

	c := &Config{
		Port:                  envOr("PORT", "8080"),
		StabilizationN:        envIntOr("STABILIZATION_N", 3),
		MaxBackoffS:           envIntOr("MAX_BACKOFF_S", 160),
		HoldMaxTTLS:           envIntOr("HOLD_MAX_TTL_S", 900),
		LogLevel:              envOr("LOG_LEVEL", "info"),
		SecretEncryptionKey:   envOr("SECRET_ENCRYPTION_KEY", ""),
		DeliveryTimeoutS:      envIntOr("DELIVERY_TIMEOUT_S", 10),
		DeliveryConcurrency:   envIntOr("DELIVERY_WORKER_CONCURRENCY", 20),
		DeliveryAllowInsecure: os.Getenv("DELIVERY_ALLOW_INSECURE_CALLBACK") == "true",
		RazorpayKeyID:         envOr("RAZORPAY_KEY_ID", ""),
		RazorpayKeySecret:     envOr("RAZORPAY_KEY_SECRET", ""),
		RazorpayWebhookSecret: envOr("RAZORPAY_WEBHOOK_SECRET", ""),
		RazorpayAPIBaseURL:    envOr("RAZORPAY_API_BASE_URL", "https://api.razorpay.com/v1"),
	}

	required := map[string]*string{
		"DATABASE_URL":             &c.DatabaseURL,
		"GATEWAY":                  &c.Gateway,
		"MERCHANT_CALLBACK_SECRET": &c.MerchantCallbackSecret,
		"ADMIN_API_KEY":            &c.AdminAPIKey,
	}
	switch os.Getenv("GATEWAY") {
	case "payu":
		required["WEBHOOK_SECRET"] = &c.WebhookSecret
		required["GATEWAY_API_KEY"] = &c.GatewayAPIKey
		required["PAYU_STATUS_URL"] = &c.PayuStatusURL
	case "razorpay":
		required["RAZORPAY_KEY_ID"] = &c.RazorpayKeyID
		required["RAZORPAY_KEY_SECRET"] = &c.RazorpayKeySecret
		required["RAZORPAY_WEBHOOK_SECRET"] = &c.RazorpayWebhookSecret
	default:
		return nil, fmt.Errorf("unsupported GATEWAY %q", os.Getenv("GATEWAY"))
	}

	for key, ptr := range required {
		val := os.Getenv(key)
		if val == "" {
			return nil, fmt.Errorf("required env var %s is not set", key)
		}
		*ptr = val
	}

	return c, nil
}

// LoadDotEnv loads .env into the process environment without validation.
func LoadDotEnv() {
	loadDotEnv()
}

func loadDotEnv() {
	file, err := os.Open(".env")
	if err != nil {
		return // Ignore if .env doesn't exist
	}
	defer func() {
		_ = file.Close()
	}()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		if (strings.HasPrefix(val, "\"") && strings.HasSuffix(val, "\"")) ||
			(strings.HasPrefix(val, "'") && strings.HasSuffix(val, "'")) {
			val = val[1 : len(val)-1]
		}
		if os.Getenv(key) == "" {
			if err := os.Setenv(key, val); err != nil {
				continue
			}
		}
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envIntOr(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}
