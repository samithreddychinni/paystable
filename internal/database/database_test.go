package database

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
)

func migrationTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	schema := fmt.Sprintf("migration_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE SCHEMA " + pq.QuoteIdentifier(schema)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec("DROP SCHEMA " + pq.QuoteIdentifier(schema) + " CASCADE"); err != nil {
			t.Error(err)
		}
	})
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		if err != nil {
			t.Fatal("invalid test database URL")
		}
		q := u.Query()
		q.Set("search_path", schema)
		q.Set("statement_timeout", "10000")
		u.RawQuery = q.Encode()
		dsn = u.String()
	} else {
		dsn += " search_path=" + schema + " statement_timeout=10000"
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestMigrateConcurrent(t *testing.T) {
	db := migrationTestDB(t)
	const callers = 8
	db.SetMaxOpenConns(callers)
	var tables int
	if err := db.QueryRow("SELECT count(*) FROM information_schema.tables WHERE table_schema=current_schema()").Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 0 {
		t.Fatalf("schema contains %d tables before migration", tables)
	}
	start := make(chan struct{})
	results := make(chan error, callers)
	for range callers {
		go func() {
			<-start
			results <- Migrate(db)
		}()
	}
	close(start)
	for range callers {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".up.sql") {
			continue
		}
		var count int
		version := strings.TrimSuffix(entry.Name(), ".up.sql")
		if err := db.QueryRow("SELECT count(*) FROM schema_migrations WHERE version=$1", version).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Errorf("migration %s recorded %d times", version, count)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	probe, err := sql.Open("postgres", os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = probe.Close() }()
	conn, err := probe.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	// Use a new session; another package can migrate this database.
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", migrationLockID); err != nil {
		t.Fatalf("migration lock was not released: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", migrationLockID); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateFailureReleasesSession(t *testing.T) {
	db := migrationTestDB(t)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(0)
	if _, err := db.Exec("CREATE TABLE holds (id bigint)"); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err == nil {
		t.Fatal("expected migration failure for an existing holds table")
	}
	if _, err := db.Exec("DROP TABLE holds"); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("migration after failure: %v", err)
	}
}

func TestWebhookActionableMigrationPreservesEvidence(t *testing.T) {
	db := migrationTestDB(t)
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`ALTER TABLE webhooks DROP COLUMN actionable; DELETE FROM schema_migrations WHERE version='006_webhook_actionable'; INSERT INTO webhooks (txn_id,gateway,event_type,payload) VALUES ('migration-evidence','payu','payment.success','{}')`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	var actionable bool
	if err := db.QueryRow(`SELECT actionable FROM webhooks WHERE txn_id='migration-evidence'`).Scan(&actionable); err != nil {
		t.Fatal(err)
	}
	if !actionable {
		t.Fatal("existing PayU evidence became non-actionable")
	}
}
