#!/bin/sh
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
check_dir=$(mktemp -d)
trap 'rm -rf "$check_dir"' EXIT HUP INT TERM
cd "$repo_dir"

printf '%s\n' 'Check tracked credentials.'
if git ls-files --error-unmatch .env .env.testkit rzp-test-key.csv >/dev/null 2>&1; then
	printf '%s\n' 'A credential file is tracked.' >&2
	exit 1
fi
if git grep -n -E 'rzp_(test|live)_[[:alnum:]]{12,}|BEGIN ([A-Z]+ )?PRIVATE KEY' -- . ':!*.example' ':!scripts/release-check.sh'; then
	printf '%s\n' 'A tracked file can contain a credential.' >&2
	exit 1
fi

printf '%s\n' 'Check repository changes.'
git diff --check HEAD
gofmt -l cmd internal testkit >"$check_dir/gofmt.txt"
if [ -s "$check_dir/gofmt.txt" ]; then
	printf '%s\n' 'Format these Go files:' >&2
	cat "$check_dir/gofmt.txt" >&2
	exit 1
fi

printf '%s\n' 'Check Go dependencies and code.'
export GOCACHE="$check_dir/go-cache"
go mod verify
go vet ./...
go test -race ./...
go build -o "$check_dir/paystable" ./cmd/paystable

printf '%s\n' 'Check deterministic demonstration output.'
go run ./testkit/lab demo >"$check_dir/demo-one.json"
go run ./testkit/lab demo >"$check_dir/demo-two.json"
cmp "$check_dir/demo-one.json" "$check_dir/demo-two.json"
grep -q '"passed": true' "$check_dir/demo-one.json"

printf '%s\n' 'Check Compose files.'
docker compose -f docker-compose.lab.yml config -q
docker compose -f docker-compose.razorpay.yml config -q
docker compose -f docker-compose.testkit.yml config -q

printf '%s\n' 'Check the dashboard.'
npm --prefix dashboard run lint
npm --prefix dashboard run build

printf '%s\n' 'Release checks passed.'
