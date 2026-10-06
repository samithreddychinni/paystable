package main

import (
	"bytes"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/lib/pq"
)

func captureDoctorOutput(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prev := doctorOut
	doctorOut = &buf
	t.Cleanup(func() { doctorOut = prev })
	fn()
	return buf.String()
}

func TestParseDatabaseURL(t *testing.T) {
	got := parseDatabaseURL("postgres://alice:secret@localhost:5432/payments?sslmode=disable")
	if got.User != "alice" {
		t.Errorf("User = %q, want alice", got.User)
	}
	if got.Database != "payments" {
		t.Errorf("Database = %q, want payments", got.Database)
	}
	if got.Host != "localhost:5432" {
		t.Errorf("Host = %q, want localhost:5432", got.Host)
	}
}

func TestParseDatabaseURL_Defaults(t *testing.T) {
	got := parseDatabaseURL("postgres://localhost:5432/")
	if got.User != "paystable" {
		t.Errorf("User = %q, want paystable", got.User)
	}
	if got.Database != "paystable" {
		t.Errorf("Database = %q, want paystable", got.Database)
	}
}

func TestExplainDatabaseConnectionError_MissingDatabase(t *testing.T) {
	target := dbTarget{User: "alice", Database: "payments", Host: "localhost:5432"}
	err := &pq.Error{Code: "3D000", Message: `database "payments" does not exist`}

	out := captureDoctorOutput(t, func() {
		explainDatabaseConnectionError(err, target)
	})

	want := "CREATE DATABASE payments OWNER alice;"
	if !strings.Contains(out, want) {
		t.Fatalf("output missing %q\nGot:\n%s", want, out)
	}
}

func TestExplainDatabaseConnectionError_BadPassword(t *testing.T) {
	target := dbTarget{User: "alice", Database: "payments", Host: "localhost:5432"}
	err := &pq.Error{Code: "28P01", Message: "password authentication failed for user \"alice\""}

	out := captureDoctorOutput(t, func() {
		explainDatabaseConnectionError(err, target)
	})

	want := "ALTER USER alice WITH PASSWORD '<new-password>';"
	if !strings.Contains(out, want) {
		t.Fatalf("output missing %q\nGot:\n%s", want, out)
	}
	if !strings.Contains(out, "DATABASE_URL") {
		t.Fatalf("expected DATABASE_URL edit hint\nGot:\n%s", out)
	}
}

func TestExplainDatabaseConnectionError_PeerIdent(t *testing.T) {
	target := dbTarget{User: "alice", Database: "payments", Host: "localhost:5432"}
	err := &pq.Error{Code: "28000", Message: "Ident authentication failed for user \"alice\""}

	out := captureDoctorOutput(t, func() {
		explainDatabaseConnectionError(err, target)
	})

	if !strings.Contains(out, `SHOW hba_file`) {
		t.Fatalf("expected SHOW hba_file lookup\nGot:\n%s", out)
	}
	wantRule := "host payments alice 127.0.0.1/32 scram-sha-256"
	if !strings.Contains(out, wantRule) {
		t.Fatalf("output missing rule %q\nGot:\n%s", wantRule, out)
	}
}

func TestExplainDatabaseConnectionError_ConnectionRefused(t *testing.T) {
	target := dbTarget{User: "alice", Database: "payments", Host: "localhost:5432"}
	err := errors.New("dial tcp 127.0.0.1:5432: connect: connection refused")

	out := captureDoctorOutput(t, func() {
		explainDatabaseConnectionError(err, target)
	})

	if !strings.Contains(out, "localhost:5432") {
		t.Fatalf("expected host in output\nGot:\n%s", out)
	}
	hint := postgresStartHint()
	if !strings.Contains(out, hint) {
		t.Fatalf("expected start hint %q\nGot:\n%s", hint, out)
	}
}

func TestPostgresStartHint(t *testing.T) {
	got := postgresStartHint()
	switch runtime.GOOS {
	case "linux":
		if !strings.Contains(got, "systemctl start postgresql") {
			t.Fatalf("linux hint = %q", got)
		}
	case "darwin":
		if !strings.Contains(got, "brew services start postgresql") {
			t.Fatalf("darwin hint = %q", got)
		}
	default:
		if !strings.Contains(got, "Postgres") {
			t.Fatalf("default hint = %q", got)
		}
	}
}

func TestExplainDatabaseConnectionError_WrappedPQ(t *testing.T) {
	target := dbTarget{User: "bob", Database: "appdb", Host: "db:5432"}
	inner := &pq.Error{Code: "3D000", Message: `database "appdb" does not exist`}
	err := fmt.Errorf("ping db: %w", inner)

	out := captureDoctorOutput(t, func() {
		explainDatabaseConnectionError(err, target)
	})

	want := "CREATE DATABASE appdb OWNER bob;"
	if !strings.Contains(out, want) {
		t.Fatalf("output missing %q\nGot:\n%s", want, out)
	}
}

func TestDoctorRazorpayAutoCapture(t *testing.T) {
	t.Setenv("GATEWAY", "razorpay")
	t.Setenv("RAZORPAY_KEY_ID", "rzp_test_key")
	t.Setenv("RAZORPAY_KEY_SECRET", "test-key-secret")
	t.Setenv("DATABASE_URL", "")
	out := captureDoctorOutput(t, func() { _ = runDoctor(nil) })
	if !strings.Contains(out, "Razorpay requires auto-capture") {
		t.Fatal("missing capture requirement")
	}
	if strings.Contains(out, "test-key-secret") {
		t.Fatal("doctor printed a credential")
	}
}
