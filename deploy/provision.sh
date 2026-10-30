#!/usr/bin/env bash
# Provisions or updates the den-wiki container (CT 108). Run as root inside
# the container from a bundle made by deploy/bundle.sh:
#   tar xzf den-wiki-bundle.tgz -C /root/den-wiki-deploy && /root/den-wiki-deploy/deploy/provision.sh
# Safe to re-run. It installs the pinned Node (deploy/node-version) and
# OpenKnowledge (scripts/ok-version) beside any older versions and points the
# `current` symlinks at them, installs the den-wiki binary, units, config and
# firewall, seeds the content repo once, and restarts the services. Rolling
# back is pointing a `current` symlink at the older version and restarting.
set -euo pipefail
bundle="$(cd "$(dirname "$0")/.." && pwd)"
node_version="$(tr -d '[:space:]' < "$bundle/deploy/node-version")"
ok_version="$(tr -d '[:space:]' < "$bundle/scripts/ok-version")"
content=/srv/den-wiki-content
# The LAN Git host on den-nas (anonymous git://). Publishing anywhere else is
# a deliberate, manual step.
backup_remote=git://192.168.1.9/den-wiki-content.git

echo "== packages"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq git ca-certificates curl xz-utils nftables >/dev/null

echo "== denwiki user"
if ! id denwiki >/dev/null 2>&1; then
	useradd --system --create-home --home-dir /var/lib/den-wiki --shell /bin/bash denwiki
fi

echo "== Node $node_version"
node_dir="/opt/node/$node_version"
if [[ ! -x "$node_dir/bin/node" ]]; then
	tmp="$(mktemp -d)"
	base="https://nodejs.org/dist/$node_version"
	tarball="node-$node_version-linux-x64.tar.xz"
	curl -fsSL -o "$tmp/$tarball" "$base/$tarball"
	curl -fsSL -o "$tmp/SHASUMS256.txt" "$base/SHASUMS256.txt"
	(cd "$tmp" && grep " $tarball\$" SHASUMS256.txt | sha256sum -c --quiet -)
	mkdir -p "$node_dir"
	tar -xJf "$tmp/$tarball" -C "$node_dir" --strip-components=1
	rm -rf "$tmp"
fi
ln -sfn "$node_dir" /opt/node/current
export PATH="/opt/node/current/bin:$PATH"

echo "== OpenKnowledge $ok_version"
ok_dir="/opt/openknowledge/$ok_version"
if [[ ! -x "$ok_dir/node_modules/.bin/ok" ]]; then
	"$bundle/scripts/install-ok.sh" "$ok_dir"
fi
ln -sfn "$ok_dir" /opt/openknowledge/current

echo "== den-wiki binary and scripts"
install -m 0755 "$bundle/den-wiki" /usr/local/bin/den-wiki
install -D -m 0755 "$bundle/deploy/push.sh" /usr/local/lib/den-wiki/push.sh
install -D -m 0644 "$bundle/deploy/den-wiki.yml" /etc/den-wiki/den-wiki.yml
install -d -o denwiki -g denwiki /var/lib/den-wiki

echo "== content repo"
if [[ ! -d "$content/.git" ]]; then
	install -d -o denwiki -g denwiki "$content"
	cp -a "$bundle/templates/content/." "$content/"
	chown -R denwiki:denwiki "$content"
	runuser -u denwiki -- git -C "$content" init -q -b main
	runuser -u denwiki -- git -C "$content" add -A
	runuser -u denwiki -- git -C "$content" -c user.name=den-wiki -c user.email=den-wiki@den-wiki.local \
		commit -q -m "Seed den-wiki-content from the den-wiki content template"
fi
if ! runuser -u denwiki -- git -C "$content" remote get-url nas >/dev/null 2>&1; then
	runuser -u denwiki -- git -C "$content" remote add nas "$backup_remote"
fi
# OK keeps its lock and local state here, so the directory must be denwiki's.
install -d -o denwiki -g denwiki "$content/.ok/local"
install -o denwiki -g denwiki -m 0644 "$bundle/deploy/ok-local-config.yml" "$content/.ok/local/config.yml"

echo "== firewall"
install -m 0755 "$bundle/deploy/nftables.conf" /etc/nftables.conf
systemctl enable --quiet nftables
systemctl restart nftables

echo "== units"
install -m 0644 "$bundle"/deploy/systemd/*.service "$bundle"/deploy/systemd/*.timer /etc/systemd/system/
systemctl daemon-reload
systemctl enable --quiet ok-wiki.service den-wiki.service den-wiki-push.timer
systemctl restart ok-wiki.service
systemctl restart den-wiki.service
systemctl start den-wiki-push.timer

echo "== done: node $(node --version), ok $("$ok_dir/node_modules/.bin/ok" --version)"
