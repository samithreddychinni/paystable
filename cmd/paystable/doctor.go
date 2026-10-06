package main

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"runtime"
	"strings"

	"github.com/IDEA-Amrita/paystable/internal/config"
	"github.com/IDEA-Amrita/paystable/internal/database"
	"github.com/lib/pq"
)

// doctorOut is the writer for doctor/init status lines (overridable in tests).
var doctorOut io.Writer = os.Stdout

var generatedSecretEnv = []string{
	"WEBHOOK_SECRET",
	"MERCHANT_CALLBACK_SECRET",
	"ADMIN_API_KEY",
	"SECRET_ENCRYPTION_KEY",
}

var gatewayCredentialEnv = []string{
	"GATEWAY_API_KEY",
	"PAYU_STATUS_URL",
}

type dbTarget struct {
	User     string
	Database string
	Host     string
}

func runDoctor(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "help", "--help", "-h":
			printDoctorUsage()
			return nil
		default:
			return fmt.Errorf("unknown doctor option: %s", args[0])
		}
	}

	infoLine("starting paystable doctor (local setup check)")
	config.LoadDotEnv()
	infoLine("loaded .env if present")

	var failed bool

	// Environment
	fmt.Println()
	fmt.Println("Environment")

	genMissing := missingKeys(generatedSecretEnv)
	if len(genMissing) > 0 {
		failed = true
		warnLine("missing generated secrets: " + strings.Join(genMissing, ", "))
		infoLine("create them with: ./paystable init")
		infoLine("(init refuses to overwrite an existing .env — rename it first if needed)")
	} else {
		okLine("generated secrets are set")
	}

	gatewayName := os.Getenv("GATEWAY")
	credentialKeys, supported := config.CredentialKeys(gatewayName)
	if gatewayName == "" {
		credentialKeys = gatewayCredentialEnv
	} else if !supported {
		failed = true
		warnLine("GATEWAY must be payu or razorpay")
	}
	if gatewayName == "razorpay" {
		infoLine("Razorpay requires auto-capture. Enable auto-capture in the Razorpay dashboard.")
	}
	gatewayMissing := missingKeys(credentialKeys)
	if len(gatewayMissing) > 0 {
		warnLine("missing gateway credentials: " + strings.Join(gatewayMissing, ", "))
		infoLine("get these from the selected gateway dashboard and set them in .env")
		infoLine("gateway gaps are warnings only — doctor continues")
	} else {
		okLine("gateway credentials are set")
	}

	if os.Getenv("GATEWAY") == "" {
		warnLine("GATEWAY is not set (expected: payu or razorpay)")
		infoLine("edit .env and set GATEWAY=payu")
	}

	dsn := os.Getenv("DATABASE_URL")

	if dsn == "" {
		failed = true

		warnLine("DATABASE_URL is not set")
		infoLine("edit .env and set DATABASE_URL, or run: ./paystable init")

		fmt.Println()
		fmt.Println("=== Database ===")

		warnLine("skipped — DATABASE_URL missing")

		fmt.Println()
		fmt.Println("=== Migrations ===")
		warnLine("skipped — no database connection")
	} else {
		okLine("DATABASE_URL is set")

		target := parseDatabaseURL(dsn)

		// Database
		fmt.Println()
		fmt.Println("=== Database ===")
		infoLine("database target: " + formatDBTarget(target))

		db, err := database.Connect(dsn)
		if err != nil {
			explainDatabaseConnectionError(err, target)
			return fmt.Errorf("database connection failed: %w", err)
		}
		defer closeDB(db)
		okLine("connected to postgres")

		// Migrations
		fmt.Println()
		fmt.Println("=== Migrations ===")

		pending, err := database.PendingMigrations(db)
		if err != nil {
			warnLine("could not list pending migrations: " + err.Error())
			return fmt.Errorf("migration check failed: %w", err)
		}
		if len(pending) == 0 {
			okLine("no pending migrations")
		} else {
			infoLine(fmt.Sprintf("%d pending migration(s):", len(pending)))
			for _, name := range pending {
				infoLine("  " + name)
			}
			infoLine("applying pending migrations")
		}

		if err := migrateQuietly(db); err != nil {
			warnLine("migration apply failed: " + err.Error())
			infoLine("fix DB permissions for the DATABASE_URL user, then rerun: ./paystable doctor")
			return fmt.Errorf("migration check failed: %w", err)
		}
		okLine("database migrations are ready")
	}

	fmt.Println()
	if failed {
		return fmt.Errorf("doctor found configuration problems")
	}

	if len(gatewayMissing) > 0 {
		okLine("database is ready")
		infoLine("set the selected gateway credentials in .env, then start: ./paystable")
		return nil
	}
	okLine("paystable is ready to start")
	return nil
}

func printDoctorUsage() {
	fmt.Println("usage: paystable doctor")
	fmt.Println()
	fmt.Println("checks:")
	fmt.Println("  Environment — generated secrets and gateway credentials")
	fmt.Println("  Database    — Postgres connectivity")
	fmt.Println("  Migrations  — pending/applied schema migrations")
}

func missingKeys(keys []string) []string {
	var missing []string
	for _, key := range keys {
		if os.Getenv(key) == "" {
			missing = append(missing, key)
		}
	}
	return missing
}

func parseDatabaseURL(raw string) dbTarget {
	u, err := url.Parse(raw)
	if err != nil {
		return dbTarget{
			User:     "paystable",
			Database: "paystable",
			Host:     "(unparseable)",
		}
	}

	dbName := strings.TrimPrefix(u.Path, "/")
	if dbName == "" {
		dbName = "paystable"
	}

	user := ""
	if u.User != nil {
		user = u.User.Username()
	}
	if user == "" {
		user = "paystable"
	}

	host := u.Host
	if host == "" {
		host = "(missing host)"
	}

	return dbTarget{User: user, Database: dbName, Host: host}
}

func formatDBTarget(t dbTarget) string {
	return fmt.Sprintf("user=%s host=%s database=%s", t.User, t.Host, t.Database)
}

func explainDatabaseConnectionError(err error, target dbTarget) {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		message := strings.ToLower(pqErr.Message)
		switch {
		case strings.Contains(message, "ident authentication failed") ||
			strings.Contains(message, "peer authentication failed"):
			warnLine("postgres is using ident/peer auth for this connection")
			infoLine(`find pg_hba.conf with: sudo -u postgres psql -c "SHOW hba_file;"`)
			infoLine(fmt.Sprintf(
				"add before broader ident/peer rules: host %s %s 127.0.0.1/32 scram-sha-256",
				target.Database, target.User,
			))
			infoLine(fmt.Sprintf(
				"for IPv6 localhost, also add: host %s %s ::1/128 scram-sha-256",
				target.Database, target.User,
			))
			infoLine("reload postgres, then rerun: ./paystable doctor")
		case pqErr.Code == "28P01":
			warnLine("DATABASE_URL password was rejected")
			infoLine(fmt.Sprintf(
				"reset it with: ALTER USER %s WITH PASSWORD '<new-password>';",
				target.User,
			))
			infoLine("then update the password in .env DATABASE_URL and rerun: ./paystable doctor")
		case pqErr.Code == "3D000":
			warnLine("the database in DATABASE_URL does not exist")
			infoLine(fmt.Sprintf(
				"create it with: CREATE DATABASE %s OWNER %s;",
				target.Database, target.User,
			))
			infoLine("then rerun: ./paystable doctor")
		default:
			warnLine("postgres error: " + pqErr.Message)
			infoLine("fix DATABASE_URL in .env, then rerun: ./paystable doctor")
		}
		return
	}

	message := strings.ToLower(err.Error())
	if strings.Contains(message, "connection refused") {
		warnLine("postgres is not accepting connections at " + target.Host)
		infoLine(postgresStartHint())
		infoLine("or update DATABASE_URL host/port in .env, then rerun: ./paystable doctor")
	}
}

func postgresStartHint() string {
	switch runtime.GOOS {
	case "darwin":
		return "start postgres with: brew services start postgresql"
	case "linux":
		return "start postgres with: sudo systemctl start postgresql"
	default:
		return "start your local Postgres service, then rerun: ./paystable doctor"
	}
}

func closeDB(db *sql.DB) {
	if err := db.Close(); err != nil {
		warnLine("database close failed: " + err.Error())
	}
}

func migrateQuietly(db *sql.DB) error {
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer slog.SetDefault(previous)
	return database.Migrate(db)
}

func doctorPrintln(prefix, msg string) {
	_, _ = fmt.Fprintln(doctorOut, prefix+msg)
}

func infoLine(msg string) {
	doctorPrintln("[INFO] ", msg)
}

func okLine(msg string) {
	doctorPrintln("[OK] ", msg)
}

func warnLine(msg string) {
	doctorPrintln("[WARN] ", msg)
}
