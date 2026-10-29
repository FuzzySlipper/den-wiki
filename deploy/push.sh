#!/usr/bin/env bash
# Pushes the content repo's main branch to every configured git remote.
# With no remote configured it says so and succeeds, so the timer stays green
# until a backup remote is added (git -C <content> remote add <name> <url>).
#   push.sh <content-dir>
set -euo pipefail
dir="${1:?usage: push.sh <content-dir>}"
mapfile -t remotes < <(git -C "$dir" remote)
if [[ ${#remotes[@]} -eq 0 ]]; then
	echo "no backup remote configured for $dir; nothing pushed"
	exit 0
fi
failed=0
for r in "${remotes[@]}"; do
	if git -C "$dir" push --quiet "$r" main; then
		echo "pushed main to $r ($(git -C "$dir" rev-parse --short main))"
	else
		echo "push to $r failed" >&2
		failed=1
	fi
done
exit "$failed"
