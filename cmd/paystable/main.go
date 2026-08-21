package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/IDEA-Amrita/paystable/internal/adminapi"
	"github.com/IDEA-Amrita/paystable/internal/config"
	"github.com/IDEA-Amrita/paystable/internal/database"
	"github.com/IDEA-Amrita/paystable/internal/delivery"
	"github.com/IDEA-Amrita/paystable/internal/gateway"
	"github.com/IDEA-Amrita/paystable/internal/gateway/payu"
	"github.com/IDEA-Amrita/paystable/internal/gateway/razorpay"
	"github.com/IDEA-Amrita/paystable/internal/hold"
	"github.com/IDEA-Amrita/paystable/internal/sse"
	"github.com/IDEA-Amrita/paystable/internal/stabilizer"
	"github.com/IDEA-Amrita/paystable/internal/ui"
	"github.com/IDEA-Amrita/paystable/internal/webhook"
)

var Version = "dev"

func main() {
	if handleCommand(os.Args[1:]) {
		return
	}

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config", "error", err)
		os.Exit(1)
	}

	setupLogger(cfg.LogLevel)

	db, err := database.Connect(cfg.DatabaseURL)
	if err != nil {
		slog.Error("database connection failed", "error", err)
		os.Exit(1)
	}
	defer db.Close() //nolint:errcheck

	if err := database.Migrate(db); err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	holdStore := hold.NewStore(db)
	holdHandler := hold.NewHandler(holdStore, cfg.HoldMaxTTLS, cfg.AdminAPIKey)

	lag := stabilizer.NewLagEstimator()
	payuClient := payu.NewClient(cfg.PayuStatusURL, cfg.GatewayAPIKey)
	razorpayClient := razorpay.NewClient(cfg.RazorpayAPIBaseURL, cfg.RazorpayKeyID, cfg.RazorpayKeySecret)
	gatewayFactory := func(g string) gateway.GatewayClient {
		switch g {
		case "payu":
			return payuClient
		case "razorpay":
			return razorpayClient
		}
		return nil
	}

	go stabilizer.Run(ctx, db, cfg, lag, gatewayFactory)
	go stabilizer.RunTTLScanner(ctx, db, cfg, gatewayFactory)
	go delivery.Run(ctx, db, delivery.Config{
		CallbackSecret:    cfg.MerchantCallbackSecret,
		AllowInsecure:     cfg.DeliveryAllowInsecure,
		TimeoutS:          cfg.DeliveryTimeoutS,
		WorkerConcurrency: cfg.DeliveryConcurrency,
	})

	mux := http.NewServeMux()

	// ── Public endpoints ──────────────────────────────────────────────
	mux.Handle("POST /webhooks/{gateway}", webhook.NewHandler(db, cfg))
	mux.HandleFunc("GET /api/v1/transactions/{txn_id}/status", holdHandler.HandleStatus)
	mux.HandleFunc("GET /api/v1/transactions/{txn_id}/stream", sse.NewHandler(db).ServeHTTP)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("GET /metrics", promhttp.Handler())

	// ── Authenticated merchant endpoints ─────────────────────────────
	mux.Handle("POST /api/v1/hold", authMiddleware(cfg.AdminAPIKey, http.HandlerFunc(holdHandler.HandleCreate)))

	// ── Admin API (localhost-only, all routes registered inside) ──────
	adminHandler := adminapi.New(db, cfg)
	adminHandler.Register(mux)

	// ── Ops dashboard SPA (localhost-only, embedded in binary) ────────
	ui.Register(mux)

	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: mux,
	}

	go func() {
		slog.Info("paystable starting", "version", Version, "port", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for shutdown signal
	<-ctx.Done()
	slog.Info("shutdown signal received, draining (30s max)")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("server shutdown error", "error", err)
	}
	slog.Info("paystable stopped")
}

func handleCommand(args []string) bool {
	if len(args) == 0 {
		return false
	}

	switch args[0] {
	case "doctor":
		if err := runDoctor(args[1:]); err != nil {
			fmt.Printf("[ERROR] %v\n", err)
			os.Exit(1)
		}
		return true
	case "version", "--version", "-v":
		fmt.Println(Version)
		return true
	case "help", "--help", "-h":
		printUsage()
		return true
	default:
		fmt.Printf("[ERROR] unknown command: %s\n", args[0])
		printUsage()
		os.Exit(1)
		return true
	}
}

func printUsage() {
	fmt.Println("usage: paystable [doctor|version]")
	fmt.Println()
	fmt.Println("commands:")
	fmt.Println("  doctor   check .env, Postgres connectivity, and database migrations")
	fmt.Println("  version  print the paystable version")
}

func authMiddleware(apiKey string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("Authorization")
		if token != "Bearer "+apiKey {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func setupLogger(level string) {
	var l slog.Level
	switch level {
	case "debug":
		l = slog.LevelDebug
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: l})))
}
