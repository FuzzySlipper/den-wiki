# Deployment

den-wiki runs in its own LXC on den-prox-mini (Proxmox host `prox`,
192.168.1.2), behind the LAN Caddy. Everything here is in `deploy/`.

## Container

| | |
|---|---|
| Guest | CT 108 `den-wiki`, unprivileged Debian 13, nesting on |
| Size | 2 cores, 4096 MiB RAM, 512 MiB swap, 32 GiB `local-zfs` root |
| Network | `vmbr1`, static 192.168.1.13/24, gateway and DNS 192.168.1.1 |
| Boot | `onboot=1`, startup order 5 |
| Backup | daily 06:00 vzdump job `backup-9fc38717-9caf` (storage `backup-server`) |
| Access | `~/server-access/den-wiki/connect` on den-agents (root, own key) |

## What runs

| Unit | Runs | Listens |
|---|---|---|
| `ok-wiki.service` | OpenKnowledge (UI and MCP), user `denwiki`, cwd `/srv/den-wiki-content` | `0.0.0.0:4800` |
| `den-wiki.service` | `den-wiki serve --config /etc/den-wiki/den-wiki.yml` (MCP facade, ledger, content commits, and the background commits of UI changes), user `denwiki` | `192.168.1.13:7341` |
| `den-wiki-push.timer` | `den-wiki-push.service` every 15 minutes: `git push` of `main` to every remote of the content repo | — |
| `nftables.service` | `/etc/nftables.conf`: ports 4800 and 7341 accept only loopback and Caddy (192.168.1.6) | — |

The facade reaches OK's MCP on loopback (`http://127.0.0.1:4800/mcp`). OK
serves the UI to Caddy, which needs `server.allowExternal` (in the untracked
`/srv/den-wiki-content/.ok/local/config.yml`) and `--external-url
https://wiki.dragonden.stream`. `KillMode=control-group` stops OK's detached
server process with the unit.

## Paths

| Path | Holds |
|---|---|
| `/opt/node/<version>`, `/opt/node/current` | Node (pinned in `deploy/node-version`) |
| `/opt/openknowledge/<version>`, `/opt/openknowledge/current` | OK (pinned in `scripts/ok-version`) |
| `/usr/local/bin/den-wiki` | the static den-wiki binary |
| `/usr/local/lib/den-wiki/push.sh` | the backup push |
| `/etc/den-wiki/den-wiki.yml` | service config (`deploy/den-wiki.yml`) |
| `/var/lib/den-wiki/den-wiki.db` | ledger and read log (SQLite); also `denwiki`'s home, with OK's `~/.ok` |
| `/srv/den-wiki-content` | the content repo (branch `main`); OK's shadow history is in `.git/ok` |

## Caddy

CT 104's `/etc/caddy/Caddyfile` has the block in
`deploy/caddy/wiki.dragonden.stream.caddy`. Certificates come from the global
Cloudflare DNS issuer. `/mcp`, `/healthz`, `/version` and `/v1/*` go to the
den-wiki service, and everything else to the OK UI. Neither has auth: the site
is LAN-only, like `den.dragonden.stream`. OK has no user accounts and gives
full control to whoever reaches it. The container firewall makes Caddy the
only way in, so a login can be added back in one place: a `basic_auth` block
(one line per user, `caddy hash-password` for each hash, no complexity rules)
in the UI's `handle`, then `systemctl reload caddy`.

After editing the Caddyfile, apply it with
`systemctl reload caddy`. Every `reverse_proxy` on CT 104 sets
`stream_close_delay 5m`: without it a reload closes proxied WebSockets (such
as OK's `/collab`) synchronously, and one unresponsive client can hang the
reload. With it, open streams survive a reload for up to 5 minutes.

## Harness hosts

den-agents and den-k8 have `~/.local/bin/den-wiki` and
`~/.config/den-wiki/cli.yml` (`endpoint: https://wiki.dragonden.stream/mcp`,
`agent:` the host name). `den-wiki health` checks the service and OK;
`den-wiki help` lists operations.

## Deploy or update

From a den-wiki checkout on den-agents:

```bash
deploy/bundle.sh /tmp/den-wiki-bundle.tgz
~/server-access/den-wiki/connect 'cat > /root/den-wiki-bundle.tgz' < /tmp/den-wiki-bundle.tgz
~/server-access/den-wiki/connect 'rm -rf /root/den-wiki-deploy && mkdir /root/den-wiki-deploy && tar xzf /root/den-wiki-bundle.tgz -C /root/den-wiki-deploy && /root/den-wiki-deploy/deploy/provision.sh'
```

`provision.sh` is idempotent: it installs missing Node and OK versions beside
the old ones, points the `current` symlinks at the pinned ones, installs the
binary, config, units and firewall, seeds the content repo only if it does not
exist, and restarts the services. Then check `den-wiki health`.

## Upgrade OpenKnowledge

1. Change `scripts/ok-version` and pass the OK integration tests
   (`scripts/ok-integration.sh`, CI job `ok-integration`).
2. Deploy as above. The new version is staged in `/opt/openknowledge/<new>`,
   `current` flips to it, and OK restarts. The old version stays installed.
3. Check `den-wiki health`, a `wiki_read`, and the UI.
4. Roll back: `ln -sfn /opt/openknowledge/<old> /opt/openknowledge/current &&
   systemctl restart ok-wiki den-wiki`, and revert `scripts/ok-version`.

Node upgrades work the same way with `deploy/node-version` and `/opt/node`.
Never run `ok init` in the container: it edits editor configs and
`.git/info/exclude`, and the content template already ships what it adds.

## Backup and restore

- **Container:** the daily vzdump covers everything, including OK's shadow
  history (`.git/ok`), `.ok/local` and the ledger database. Restore with
  `pct restore 108 <archive>` on `prox` (stop or remove the old CT 108 first;
  never run two copies on .13).
- **Content repo:** `den-wiki-push.timer` pushes `main` to each remote of
  `/srv/den-wiki-content`. Its one remote is `nas`,
  `git://192.168.1.9/den-wiki-content.git`: a bare repository on the den-nas
  LAN Git host (anonymous `git://`, the same host as `server-access`; see
  `~/server-access/git-host/README.md`). `provision.sh` adds it when missing.
  Offsite copies are the NAS's job. Nothing pushes to GitHub: publishing
  content anywhere else is a deliberate, manual step. Check pushes with
  `journalctl -u den-wiki-push` or `git ls-remote
  git://192.168.1.9/den-wiki-content.git`. A clone of the NAS repo is a
  working content repo: clone it to `/srv/den-wiki-content` as `denwiki`,
  re-run `provision.sh` (it adds `.ok/local/config.yml`) and restart. OK
  rebuilds its index; its shadow history starts fresh.
- **Ledger:** `/var/lib/den-wiki/den-wiki.db` is only in the container backup.
  For a point-in-time copy, stop `den-wiki` and copy the file.

## Changes made in the UI

`den-wiki serve` commits what people change in the OK UI, including pages the
editor re-saves when they are only viewed, so it reaches the 15-minute backup
push. The `ui_commits` section of `/etc/den-wiki/den-wiki.yml` sets the author
(`ui`), how often it looks (`interval`, 30 s) and how long a file must go
unchanged first (`quiet`, 60 s); `disabled: true` turns it off. What it
commits and how is in [facade](facade.md#changes-made-in-the-ui). Its commits
show in `journalctl -u den-wiki` as `ui_commit`, and source re-saves it
restored as `ui_format_restored`.
