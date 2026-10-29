#!/usr/bin/env bash
# Builds the static den-wiki binary and packs everything deploy/provision.sh
# needs into one tarball.
#   deploy/bundle.sh <out.tgz>
set -euo pipefail
cd "$(dirname "$0")/.."
out="${1:?usage: deploy/bundle.sh <out.tgz>}"
stage="$(mktemp -d)"
trap 'rm -rf "$stage"' EXIT
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o "$stage/den-wiki" ./cmd/den-wiki
mkdir -p "$stage/scripts" "$stage/templates"
cp -a deploy "$stage/"
cp scripts/ok-version scripts/install-ok.sh "$stage/scripts/"
cp -a templates/content "$stage/templates/"
tar -czf "$out" -C "$stage" .
echo "$out: den-wiki $(git rev-parse --short HEAD)$(git diff --quiet HEAD -- . || echo '+dirty')"
