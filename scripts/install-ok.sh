#!/usr/bin/env bash
# Installs the pinned OpenKnowledge (scripts/ok-version) into a directory, for
# the OK integration tests. OK is a GPL-3 npm package that den-wiki only runs
# and talks to over HTTP; it is never vendored or linked.
#   scripts/install-ok.sh <dir>    then  <dir>/node_modules/.bin/ok
set -euo pipefail
cd "$(dirname "$0")/.."
dir="${1:?usage: scripts/install-ok.sh <dir>}"
version="$(tr -d '[:space:]' < scripts/ok-version)"
mkdir -p "$dir"
npm install --prefix "$dir" --no-audit --no-fund --loglevel=error "@inkeep/open-knowledge@${version}"
"$dir/node_modules/.bin/ok" --version
