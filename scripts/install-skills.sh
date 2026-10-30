#!/usr/bin/env bash
# Installs the den-wiki skills for one harness from this checkout.
#   scripts/install-skills.sh <harness> [--dest <dir>] [--copy]
# <harness> is a name from skills/harnesses.yml. Each skill in
# skills/packages/<harness>/ is linked into the harness's skills folder (its
# `install` entry, or --dest), so a `git pull` updates it. --copy copies
# instead, for a user who cannot read this checkout. Re-running replaces
# earlier installs of the same skills and touches nothing else.
set -euo pipefail
repo="$(cd "$(dirname "$0")/.." && pwd)"
harness="${1:?usage: install-skills.sh <harness> [--dest <dir>] [--copy]}"
shift
dest=""
copy=0
while [[ $# -gt 0 ]]; do
	case "$1" in
	--dest) dest="${2:?--dest needs a folder}"; shift 2 ;;
	--copy) copy=1; shift ;;
	*) echo "unknown argument: $1" >&2; exit 2 ;;
	esac
done
pkg="$repo/skills/packages/$harness"
if [[ ! -d "$pkg" ]]; then
	echo "no packages for harness $harness; see skills/harnesses.yml" >&2
	exit 2
fi
if [[ -z "$dest" ]]; then
	dest="$(awk -v h="$harness" '$1 == "-" && $2 == "name:" { cur = $3 } cur == h && $1 == "install:" { print $2; exit }' "$repo/skills/harnesses.yml")"
	dest="${dest/#\~/$HOME}"
fi
mkdir -p "$dest"
for skill in "$pkg"/*/; do
	name="$(basename "$skill")"
	rm -rf "${dest:?}/$name"
	if [[ $copy -eq 1 ]]; then
		cp -r "$skill" "$dest/$name"
	else
		ln -s "${skill%/}" "$dest/$name"
	fi
	echo "installed $name -> $dest/$name"
done
