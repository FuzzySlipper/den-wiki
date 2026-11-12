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
| `den-wiki.service` | `den-wiki serve --config /etc/den-wiki/den-wiki.yml` (MCP facade, ledger, content commits, the background commits of UI changes, and the read-only reader), user `denwiki` | `192.168.1.13:7341` |
| `ok-wiki-watchdog.service` | `den-wiki ok-watchdog`, root: pings OK's MCP every minute and restarts `ok-wiki.service` after 3 failed pings in a row (see below) | — |
| `den-wiki-push.timer` | `den-wiki-push.service` every 15 minutes: `git push` of `main` to every remote of the content repo | — |
| `nftables.service` | `/etc/nftables.conf`: ports 4800 and 7341 accept only loopback and Caddy (192.168.1.6) | — |

The facade reaches OK's MCP on loopback (`http://127.0.0.1:4800/mcp`). OK
serves the UI to Caddy, which needs `server.allowExternal` (in the untracked
`/srv/den-wiki-content/.ok/local/config.yml`) and `--external-url
https://edit.wiki.dragonden.stream`. `KillMode=control-group` stops OK's detached
server process with the unit.

OK can hang with its unit still active, so `Restart=on-failure` never fires:
on 2026-11-11 its file watcher lost its subscription during a bulk import and
OK stopped answering for 35 minutes (#10740). `ok-wiki-watchdog` covers that.
It skips a stopped unit (that is systemd's or a person's doing) and the first
two minutes after OK starts, so a restart has time to come up; a hang is
restarted within about four minutes. Each failed ping, recovery and restart is
in its journal:

```bash
journalctl -u ok-wiki-watchdog -g 'restarting OpenKnowledge'   # the restarts
journalctl -u ok-wiki-watchdog --since today                   # everything
```

`den-wiki ok-watchdog --help` lists its flags (endpoint, unit, interval,
timeout, failures, grace, and `--user` for a unit in the user's service
manager).

Checked on 2026-11-11 on den-agents with the pinned OK as a user unit and the
watchdog at its production defaults: `SIGSTOP` to every OK process at
18:02:34; failed pings at 18:02:50, 18:03:50 and 18:04:50; "restarting
OpenKnowledge after 3 failed pings" at 18:04:50; OK answering again 139 s
after the stop, and the watchdog's next ping after the grace period
succeeded.

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
| `/srv/den-wiki-media` | the media store (video and files over 50 MiB): den-nas `/mnt/storage/den-wiki-media`, bind-mounted from the host |

## Caddy

CT 104's `/etc/caddy/Caddyfile` has the two blocks in
`deploy/caddy/wiki.dragonden.stream.caddy`. Certificates come from the global
Cloudflare DNS issuer.

- **`wiki.dragonden.stream`** goes to the den-wiki service: the read-only
  reader ([reader.md](reader.md)) at `/`, plus `/mcp`, `/healthz`,
  `/version`, `/v1/*` and `/media/*`. den-services doc-publish (CT 106)
  fetches pages shared with `wiki_publish` through it, from `/v1/export`.
- **`edit.wiki.dragonden.stream`** goes to the OK editor, with its `/collab`
  WebSocket (#10558). The exception is `/media/*`, which goes to den-wiki:
  pages embed media store files as root-relative `/media/<sha256>.<ext>`, and
  only den-wiki serves them. OK's single-page app cannot run under a sub-path,
  so it needs its own host. The host needs a DNS record like the other LAN
  names.

Neither host has auth: both are LAN-only, like `den.dragonden.stream`. OK has
no user accounts and gives full control to whoever reaches it. The container
firewall makes Caddy the only way in, so a login can be added back in one
place: a `basic_auth` block (one line per user, `caddy hash-password` for
each hash, no complexity rules) in the editor's block, then
`systemctl reload caddy`.

After editing the Caddyfile, apply it with
`systemctl reload caddy`. Every `reverse_proxy` on CT 104 sets
`stream_close_delay 5m`: without it a reload closes proxied WebSockets (such
as OK's `/collab`) synchronously, and one unresponsive client can hang the
reload. With it, open streams survive a reload for up to 5 minutes.

## Media store

`wiki_attach` keeps video, and any file over `media.git_max_bytes` (50 MiB),
out of git: the content repo would keep every byte forever, in OK's shadow
history, the NAS mirror and every daily vzdump. The store is a directory of
`<sha256>.<ext>` files on den-nas, which `den-wiki serve` serves at
`/media/`. `provision.sh` refuses to run until `/srv/den-wiki-media` is a mount
point writable by `denwiki`, and the unit has `RequiresMountsFor`, so den-wiki
never fills the container's own disk.

| Layer | Setting |
|---|---|
| den-nas | `/mnt/storage/den-wiki-media`, owned `100999:100991` (`denwiki`, 999:991 in the unprivileged CT, shifted by the default 100000 ID map). It is inside the existing `/mnt/storage` export to the LAN (`rw,root_squash`, AUTH_SYS) |
| den-prox-mini | `/etc/fstab`: `192.168.1.9:/mnt/storage/den-wiki-media /mnt/den-wiki-media nfs vers=3,hard,_netdev 0 0` |
| CT 108 | `pct set 108 -mp0 /mnt/den-wiki-media,mp=/srv/den-wiki-media,backup=0` |

Set it up once, in that order (the bind mount needs a container restart if
it does not hot-plug), then deploy. Backups: the store is not in the vzdump
(`backup=0`); it is on the NAS with the rest of `/mnt/storage`, under the
NAS's own backup. A store file is never changed, so a copy is a plain
`rsync -a --ignore-existing`. Nothing deletes from the store; removing a file
no page uses is a manual step.

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
exist, and restarts the services. On every run it also brings the content
repo's contract files (`.ok/schemas/`, each zone's `.ok/frontmatter.yml` and
`.ok/templates/`) up to the template's, committing only those paths, so other
pending edits in the content repo are left alone; `.ok/config.yml` is not
touched. Then check `den-wiki health`.

## Upgrade OpenKnowledge

1. Change `scripts/ok-version` and pass the OK integration tests
   (`scripts/ok-integration.sh`, CI job `ok-integration`).
2. Deploy as above. The new version is staged in `/opt/openknowledge/<new>`,
   `current` flips to it, and OK restarts. The old version stays installed.
3. Check `den-wiki health`, a `wiki_read`, the reader at
   `https://wiki.dragonden.stream/`, and the editor at
   `https://edit.wiki.dragonden.stream/` on a page that embeds a `/media/`
   clip, which must play there too.
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
show in `journalctl -u den-wiki` as `ui_commit`.
