#!/usr/bin/env bash
# Runs the OpenKnowledge integration tests (build tag okintegration) against
# the pinned OK from scripts/install-ok.sh. CI runs this as the ok-integration
# job. On a workstation pass --isolate-home: OK writes ~/.ok/local under the
# real home directory, and bwrap keeps that in a throwaway tmpfs.
#   scripts/ok-integration.sh <ok-install-dir> [--isolate-home]
set -euo pipefail
cd "$(dirname "$0")/.."
dir="${1:?usage: scripts/ok-integration.sh <ok-install-dir> [--isolate-home]}"
extra=()
if [[ "${2:-}" == "--isolate-home" ]]; then
	extra+=(-ok-isolate-home)
fi
[[ -x "$dir/node_modules/.bin/ok" ]] || scripts/install-ok.sh "$dir"
mapfile -t pkgs < <(grep -rl --include='*_test.go' '^//go:build okintegration' internal cmd | xargs -n1 dirname | sort -u | sed 's#^#./#')
go test -tags okintegration -count=1 -timeout 10m "${pkgs[@]}" -args -ok-bin="$(cd "$dir" && pwd)/node_modules/.bin/ok" "${extra[@]}"
