#!/bin/sh
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
source_dir=$(mktemp -d)
trap 'rm -rf "$source_dir"' EXIT HUP INT TERM
commit=e4b5303fde24581003faf0b05c08feb605c2ef93

git init --quiet "$source_dir"
git -C "$source_dir" remote add origin https://github.com/flexprice/flexprice.git
git -C "$source_dir" fetch --quiet --depth 1 origin "$commit"
git -C "$source_dir" checkout --quiet --detach FETCH_HEAD
test "$(git -C "$source_dir" rev-parse HEAD)" = "$commit"

cp "$repo_dir/testkit/external/flexprice/amount_mismatch_test.go.txt" \
	"$source_dir/internal/integration/razorpay/webhook/paystable_probe_test.go"

cd "$source_dir"
go test ./internal/integration/razorpay/webhook \
	-run '^TestPaystableProbeFindsAmountMismatch$' -count=1
printf '%s\n' 'The external amount mismatch was reproduced.'
