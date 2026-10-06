package config

import "testing"

func setRequiredEnvs(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost:5432/paystable")
	t.Setenv("GATEWAY", "payu")
	t.Setenv("WEBHOOK_SECRET", "secret")
	t.Setenv("GATEWAY_API_KEY", "gateway_key")
	t.Setenv("PAYU_STATUS_URL", "https://info.payu.in/merchant/postservice")
	t.Setenv("MERCHANT_CALLBACK_SECRET", "callback_secret")
	t.Setenv("ADMIN_API_KEY", "admin_key")
}

func clearAllEnvs(t *testing.T) {
	for _, env := range []string{
		"DATABASE_URL", "GATEWAY", "WEBHOOK_SECRET", "GATEWAY_API_KEY",
		"PAYU_STATUS_URL", "MERCHANT_CALLBACK_SECRET", "ADMIN_API_KEY", "PORT",
		"STABILIZATION_N", "MAX_BACKOFF_S", "HOLD_MAX_TTL_S", "LOG_LEVEL",
		"ADMIN_ALLOWED_SOURCES",
	} {
		t.Setenv(env, "")
	}
}

func TestLoad_AdminAllowedSources(t *testing.T) {
	clearAllEnvs(t)
	setRequiredEnvs(t)
	t.Setenv("ADMIN_ALLOWED_SOURCES", "172.31.0.1, ::1")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.AdminAllowedIPs) != 2 {
		t.Fatalf("AdminAllowedIPs = %v, want two addresses", cfg.AdminAllowedIPs)
	}
}

func TestLoad_InvalidAdminAllowedSource(t *testing.T) {
	clearAllEnvs(t)
	setRequiredEnvs(t)
	t.Setenv("ADMIN_ALLOWED_SOURCES", "invalid.invalid")

	if _, err := Load(); err == nil {
		t.Fatal("expected error for invalid ADMIN_ALLOWED_SOURCES")
	}
}

func TestLoad_MissingRequired(t *testing.T) {
	clearAllEnvs(t)

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when required env vars are missing")
	}
}

func TestLoad_AllRequired(t *testing.T) {
	clearAllEnvs(t)
	setRequiredEnvs(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.DatabaseURL != "postgres://localhost:5432/paystable" {
		t.Errorf("DatabaseURL = %q, want postgres://localhost:5432/paystable", cfg.DatabaseURL)
	}
	if cfg.Gateway != "payu" {
		t.Errorf("Gateway = %q, want payu", cfg.Gateway)
	}
	if cfg.WebhookSecret != "secret" {
		t.Errorf("WebhookSecret = %q, want secret", cfg.WebhookSecret)
	}
}

func TestLoad_Defaults(t *testing.T) {
	clearAllEnvs(t)
	setRequiredEnvs(t)

	cfg, _ := Load()

	if cfg.Port != "8080" {
		t.Errorf("Port = %q, want 8080", cfg.Port)
	}
	if cfg.StabilizationN != 3 {
		t.Errorf("StabilizationN = %d, want 3", cfg.StabilizationN)
	}
	if cfg.MaxBackoffS != 160 {
		t.Errorf("MaxBackoffS = %d, want 160", cfg.MaxBackoffS)
	}
	if cfg.HoldMaxTTLS != 900 {
		t.Errorf("HoldMaxTTLS = %d, want 900", cfg.HoldMaxTTLS)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want info", cfg.LogLevel)
	}
}

func TestLoad_CustomOptionals(t *testing.T) {
	clearAllEnvs(t)
	setRequiredEnvs(t)

	t.Setenv("PORT", "9000")
	t.Setenv("STABILIZATION_N", "5")
	t.Setenv("MAX_BACKOFF_S", "300")
	t.Setenv("HOLD_MAX_TTL_S", "600")
	t.Setenv("LOG_LEVEL", "debug")

	cfg, _ := Load()

	if cfg.Port != "9000" {
		t.Errorf("Port = %q, want 9000", cfg.Port)
	}
	if cfg.StabilizationN != 5 {
		t.Errorf("StabilizationN = %d, want 5", cfg.StabilizationN)
	}
	if cfg.MaxBackoffS != 300 {
		t.Errorf("MaxBackoffS = %d, want 300", cfg.MaxBackoffS)
	}
	if cfg.HoldMaxTTLS != 600 {
		t.Errorf("HoldMaxTTLS = %d, want 600", cfg.HoldMaxTTLS)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want debug", cfg.LogLevel)
	}
}

func TestLoad_InvalidIntFallsBackToDefault(t *testing.T) {
	clearAllEnvs(t)
	setRequiredEnvs(t)

	t.Setenv("STABILIZATION_N", "abc")
	t.Setenv("MAX_BACKOFF_S", "not_a_number")
	t.Setenv("HOLD_MAX_TTL_S", "")

	cfg, _ := Load()

	if cfg.StabilizationN != 3 {
		t.Errorf("StabilizationN = %d, want 3 (fallback on invalid input)", cfg.StabilizationN)
	}
	if cfg.MaxBackoffS != 160 {
		t.Errorf("MaxBackoffS = %d, want 160 (fallback on invalid input)", cfg.MaxBackoffS)
	}
	if cfg.HoldMaxTTLS != 900 {
		t.Errorf("HoldMaxTTLS = %d, want 900 (fallback on empty string)", cfg.HoldMaxTTLS)
	}
}

func TestLoad_SingleMissingRequired(t *testing.T) {
	clearAllEnvs(t)
	setRequiredEnvs(t)

	t.Setenv("GATEWAY_API_KEY", "")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when GATEWAY_API_KEY is missing")
	}
}

func TestLoadRazorpayCredentials(t *testing.T) {
	clearAllEnvs(t)
	setRequiredEnvs(t)
	t.Setenv("GATEWAY", "razorpay")
	t.Setenv("GATEWAY_API_KEY", "")
	t.Setenv("PAYU_STATUS_URL", "")
	t.Setenv("RAZORPAY_KEY_ID", "rzp_test_key")
	t.Setenv("RAZORPAY_KEY_SECRET", "test-key-secret")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RazorpayAPIURL != "https://api.razorpay.com/v1" {
		t.Fatal("incorrect Razorpay default URL")
	}
	t.Setenv("RAZORPAY_KEY_SECRET", "")
	if _, err := Load(); err == nil {
		t.Fatal("missing key secret accepted")
	}
	t.Setenv("GATEWAY", "unknown")
	if _, err := Load(); err == nil {
		t.Fatal("unknown gateway accepted")
	}
}
