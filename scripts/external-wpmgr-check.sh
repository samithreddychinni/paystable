#!/bin/sh
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
source_dir=$(mktemp -d)
trap 'rm -rf "$source_dir"' EXIT HUP INT TERM
commit=e2fd78e9829a112ac229b1586e66ba3fd39aeaf7

git init --quiet "$source_dir"
git -C "$source_dir" remote add origin https://github.com/mosamlife/wpmgr.git
git -C "$source_dir" fetch --quiet --depth 1 origin "$commit"
git -C "$source_dir" checkout --quiet --detach FETCH_HEAD
test "$(git -C "$source_dir" rev-parse HEAD)" = "$commit"

cp "$repo_dir/testkit/external/wpmgr/unicode_tampering_test.go.txt" \
	"$source_dir/apps/api/internal/billing/razorpay/paystable_probe_test.go"
cp "$repo_dir/testkit/external/wpmgr/http_postgres_retry_test.go.txt" \
	"$source_dir/apps/api/internal/billing/paystable_probe_test.go"

cd "$source_dir/apps/api"
GOTOOLCHAIN=auto go test ./internal/billing/razorpay ./internal/billing \
	-run '^TestPaystable(ProbeChecksUnicodeAndTampering|RouteRetriesAfterPostgresWriteFailure)$' -count=1
printf '%s\n' 'The external signature, HTTP, and PostgreSQL checks passed.'
