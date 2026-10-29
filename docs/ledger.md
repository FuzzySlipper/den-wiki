# Research ledger

The ledger is den-wiki's queue of research jobs (`internal/ledger`). It is a
thin port of the den-services tasks model: job records, status transitions,
parent links, bounded listing and coded errors, without review, CI or GitHub
machinery. The ledger holds the queue and its status; the job's trail page
(`trails/<slug>.md` in the content repo) holds the narrative.

Storage is SQLite through the pure-Go `modernc.org/sqlite` driver (no CGO).

## Jobs

| Field | Meaning |
|---|---|
| `id` | Ledger-assigned integer |
| `question`, `scope_notes` | What to research and its limits |
| `requested_by` | Who asked |
| `assignee`, `harness` | Current claimer and its harness (empty unless claimed) |
| `status` | `open`, `claimed`, `done` or `abandoned` |
| `parent_id` | Optional parent job; cycles are rejected |
| `priority` | 1 (highest) to 5; default 3 |
| `trail_slug` | Set by `finish`; names `trails/<slug>.md` |
| `output_pages` | `wiki/*.md` paths created or edited |
| `lease_expires_at`, `claimed_at`, `finished_at`, `created_at`, `updated_at` | Timestamps (UTC) |

## Statuses and leases

`open` → `claimed` → `done` | `abandoned`. A released or expired claim returns
the job to `open`; `abandoned` is also reachable from `open`.

- **claim** is atomic: of several concurrent claimers exactly one wins, the rest
  get `claim_conflict`. It takes a lease (`lease_seconds`, 60 to 86400, default
  1800) and returns a `lease_token` that only the claimer sees.
- **renew**, **release** and **finish** need the current `lease_token`.
  **abandon** needs it while the job is claimed.
- A lease ends at `lease_expires_at`. The ledger expires lapsed leases before
  every read and write (and `ExpireLeases` sweeps all of them), returning the
  job to `open` with an `expired` event by actor `ledger`. The old holder's next
  call gets `lease_expired`.
- **finish** requires `trail_slug`. When the service has a content directory, it
  also requires `trails/<slug>.md` to exist there.
- **edit** changes question, scope notes, priority or parent while the job is not
  done or abandoned.

## Event log

Every change appends one event: `created`, `claimed`, `renewed`, `released`,
`expired`, `finished`, `abandoned` or `edited`, with `actor`, optional `note`,
time, and `data` holding the fields the event set. Replaying a job's events in
order reconstructs it. The table rejects updates and deletes.

## Errors

Errors carry a stable code and an HTTP status:

| Code | HTTP | When |
|---|---|---|
| `not_found` | 404 | No such job |
| `invalid_transition` | 409 | The job's status does not allow the operation |
| `claim_conflict` | 409 | Someone else holds the claim, or the token is not the current lease |
| `lease_expired` | 409 | The caller's lease lapsed and the job returned to `open` |
| `validation_failed` | 400 | Bad or missing input, unknown fields, missing trail |
| `internal_error` | 500 | Anything else |

## HTTP API

JSON in and out. Bodies are decoded strictly: unknown fields and trailing
values are `validation_failed`. Errors are `{"error":{"code","message"}}`.

| Method and path | Operation |
|---|---|
| `POST /v1/jobs` | create |
| `GET /v1/jobs?status=&assignee=&requested_by=&parent_id=&max_priority=&limit=&offset=` | list: `{jobs, next_offset}`, `next_offset` null on the last page; `limit` 1 to 200, default 50 |
| `GET /v1/jobs/{id}` | get |
| `PATCH /v1/jobs/{id}` | edit |
| `GET /v1/jobs/{id}/events` | events, oldest first |
| `POST /v1/jobs/{id}/claim` | claim |
| `POST /v1/jobs/{id}/renew` | renew |
| `POST /v1/jobs/{id}/release` | release |
| `POST /v1/jobs/{id}/finish` | finish |
| `POST /v1/jobs/{id}/abandon` | abandon |

The ledger binds to LAN or loopback only and has no auth of its own; the
den-wiki facade is its intended caller.
