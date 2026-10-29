#!/usr/bin/env bash
# The den-wiki gate: formatting, vet and tests. CI runs this as the verify job.
set -euo pipefail
cd "$(dirname "$0")/.."

GOFUMPT_VERSION=v0.12.0
if ! command -v gofumpt >/dev/null 2>&1; then
	go install "mvdan.cc/gofumpt@${GOFUMPT_VERSION}"
	PATH="$(go env GOPATH)/bin:$PATH"
fi

echo "== gofumpt"
unformatted="$(gofumpt -l .)"
if [[ -n "$unformatted" ]]; then
	echo "files need gofumpt -w:" >&2
	echo "$unformatted" >&2
	exit 1
fi

echo "== go vet"
go vet ./...

echo "== go test"
go test ./...

echo "== static binary builds"
CGO_ENABLED=0 go build -trimpath -o /dev/null ./cmd/den-wiki

echo "== CLI catalog matches the registry"
go run ./cmd/den-wiki-catalog -output /dev/stdout | cmp -s - internal/cli/catalog.json || {
	echo "internal/cli/catalog.json is stale; run go generate ./internal/cli" >&2
	exit 1
}

echo "== content template lints clean"
go run ./cmd/den-wiki lint --format text --fail-on info templates/content
