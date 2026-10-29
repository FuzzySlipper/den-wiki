package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// stored is a job row together with its secret lease token.
type stored struct {
	Job
	LeaseToken string
}

// mutation is what a transition decides: the job's next state and the events
// that record the change. Returning an error rolls the transaction back.
type mutation func(cur stored) (next stored, events []Event, err error)

// Store persists jobs and events. Every write runs in one transaction, and
// writes are serialised, so a mutation sees the current row and nothing else
// can change it until the mutation commits.
type Store interface {
	Ping(ctx context.Context) error
	Create(ctx context.Context, job stored, ev Event) (stored, error)
	Get(ctx context.Context, id int64) (stored, error)
	List(ctx context.Context, q ListQuery) ([]Job, error)
	Events(ctx context.Context, id int64) ([]Event, error)
	// Update expires the job's lease if it ended at or before now, then
	// applies fn to the current row, all in one transaction.
	Update(ctx context.Context, id int64, now time.Time, fn mutation) (stored, error)
	// ExpireLeases returns every job whose lease ended at or before now to
	// open and records an expired event for each.
	ExpireLeases(ctx context.Context, now time.Time) (int, error)
	Close() error
}

// ListQuery filters a bounded listing. Zero values mean "any".
type ListQuery struct {
	Statuses    []string
	Assignee    string
	RequestedBy string
	ParentID    *int64
	MaxPriority int
	Limit       int
	Offset      int
}

const schema = `
CREATE TABLE IF NOT EXISTS jobs (
	id               INTEGER PRIMARY KEY AUTOINCREMENT,
	question         TEXT NOT NULL,
	scope_notes      TEXT NOT NULL DEFAULT '',
	requested_by     TEXT NOT NULL,
	assignee         TEXT NOT NULL DEFAULT '',
	harness          TEXT NOT NULL DEFAULT '',
	status           TEXT NOT NULL,
	parent_id        INTEGER REFERENCES jobs(id),
	priority         INTEGER NOT NULL,
	trail_slug       TEXT NOT NULL DEFAULT '',
	output_pages     TEXT NOT NULL DEFAULT '[]',
	lease_token      TEXT NOT NULL DEFAULT '',
	lease_expires_at TEXT,
	created_at       TEXT NOT NULL,
	updated_at       TEXT NOT NULL,
	claimed_at       TEXT,
	finished_at      TEXT
);
CREATE INDEX IF NOT EXISTS jobs_status_priority ON jobs(status, priority, id);
CREATE INDEX IF NOT EXISTS jobs_lease ON jobs(status, lease_expires_at);
CREATE TABLE IF NOT EXISTS job_events (
	id     INTEGER PRIMARY KEY AUTOINCREMENT,
	job_id INTEGER NOT NULL REFERENCES jobs(id),
	kind   TEXT NOT NULL,
	actor  TEXT NOT NULL,
	note   TEXT NOT NULL DEFAULT '',
	at     TEXT NOT NULL,
	data   TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS job_events_job ON job_events(job_id, id);
CREATE TRIGGER IF NOT EXISTS job_events_append_only_update BEFORE UPDATE ON job_events
BEGIN SELECT RAISE(ABORT, 'job_events is append-only'); END;
CREATE TRIGGER IF NOT EXISTS job_events_append_only_delete BEFORE DELETE ON job_events
BEGIN SELECT RAISE(ABORT, 'job_events is append-only'); END;
`

// SQLiteStore is the ledger's SQLite Store. It uses the pure-Go modernc
// driver, so the binary needs no CGO.
type SQLiteStore struct {
	db *sql.DB
}

// OpenSQLite opens (creating if needed) the database at path. Use ":memory:"
// only in tests.
func OpenSQLite(ctx context.Context, path string) (*SQLiteStore, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open ledger database: %w", err)
	}
	// One connection serialises writers inside this process; _txlock=immediate
	// and busy_timeout cover a second process on the same file.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.ExecContext(ctx, schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("create ledger schema: %w", err)
	}
	return &SQLiteStore{db: db}, nil
}

func (s *SQLiteStore) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

func (s *SQLiteStore) Close() error { return s.db.Close() }

const jobColumns = `id, question, scope_notes, requested_by, assignee, harness, status, parent_id, priority,
	trail_slug, output_pages, lease_token, lease_expires_at, created_at, updated_at, claimed_at, finished_at`

type scanner interface{ Scan(dest ...any) error }

func scanJob(row scanner) (stored, error) {
	var (
		j                                   stored
		parent                              sql.NullInt64
		pages, created, updated             string
		leaseExpires, claimedAt, finishedAt sql.NullString
	)
	err := row.Scan(&j.ID, &j.Question, &j.ScopeNotes, &j.RequestedBy, &j.Assignee, &j.Harness, &j.Status, &parent, &j.Priority,
		&j.TrailSlug, &pages, &j.LeaseToken, &leaseExpires, &created, &updated, &claimedAt, &finishedAt)
	if err != nil {
		return stored{}, err
	}
	if parent.Valid {
		p := parent.Int64
		j.ParentID = &p
	}
	if err := json.Unmarshal([]byte(pages), &j.OutputPages); err != nil {
		return stored{}, fmt.Errorf("decode output_pages of job %d: %w", j.ID, err)
	}
	if j.CreatedAt, err = parseTime(created); err != nil {
		return stored{}, err
	}
	if j.UpdatedAt, err = parseTime(updated); err != nil {
		return stored{}, err
	}
	if j.LeaseExpiresAt, err = parseNullTime(leaseExpires); err != nil {
		return stored{}, err
	}
	if j.ClaimedAt, err = parseNullTime(claimedAt); err != nil {
		return stored{}, err
	}
	if j.FinishedAt, err = parseNullTime(finishedAt); err != nil {
		return stored{}, err
	}
	return j, nil
}

// timeLayout is fixed-width so stored times compare correctly as strings.
const timeLayout = "2006-01-02T15:04:05.000000000Z"

func formatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(timeLayout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse stored time %q: %w", s, err)
	}
	return t.UTC(), nil
}

func parseNullTime(s sql.NullString) (*time.Time, error) {
	if !s.Valid || s.String == "" {
		return nil, nil
	}
	t, err := parseTime(s.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return formatTime(*t)
}

func nullInt(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

func (s *SQLiteStore) Create(ctx context.Context, j stored, ev Event) (stored, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return stored{}, fmt.Errorf("begin create: %w", err)
	}
	defer tx.Rollback()
	if j.ParentID != nil {
		var one int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM jobs WHERE id = ?`, *j.ParentID).Scan(&one); errors.Is(err, sql.ErrNoRows) {
			return stored{}, validationFailed(fmt.Errorf("parent job %d does not exist", *j.ParentID))
		} else if err != nil {
			return stored{}, err
		}
	}
	pages, _ := json.Marshal(j.OutputPages)
	res, err := tx.ExecContext(ctx, `INSERT INTO jobs (question, scope_notes, requested_by, assignee, harness, status, parent_id, priority,
		trail_slug, output_pages, lease_token, lease_expires_at, created_at, updated_at, claimed_at, finished_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		j.Question, j.ScopeNotes, j.RequestedBy, j.Assignee, j.Harness, j.Status, nullInt(j.ParentID), j.Priority,
		j.TrailSlug, string(pages), j.LeaseToken, nullTime(j.LeaseExpiresAt), formatTime(j.CreatedAt), formatTime(j.UpdatedAt),
		nullTime(j.ClaimedAt), nullTime(j.FinishedAt))
	if err != nil {
		return stored{}, fmt.Errorf("insert job: %w", err)
	}
	if j.ID, err = res.LastInsertId(); err != nil {
		return stored{}, err
	}
	ev.JobID = j.ID
	if ev.Data == nil {
		ev.Data = map[string]any{}
	}
	ev.Data["id"] = j.ID
	if err := insertEvent(ctx, tx, ev); err != nil {
		return stored{}, err
	}
	if err := tx.Commit(); err != nil {
		return stored{}, fmt.Errorf("commit create: %w", err)
	}
	return j, nil
}

func insertEvent(ctx context.Context, tx *sql.Tx, ev Event) error {
	data := ev.Data
	if data == nil {
		data = map[string]any{}
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("encode event data: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO job_events (job_id, kind, actor, note, at, data) VALUES (?, ?, ?, ?, ?, ?)`,
		ev.JobID, ev.Kind, ev.Actor, ev.Note, formatTime(ev.At), string(raw)); err != nil {
		return fmt.Errorf("insert %s event: %w", ev.Kind, err)
	}
	return nil
}

func (s *SQLiteStore) Get(ctx context.Context, id int64) (stored, error) {
	j, err := scanJob(s.db.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return stored{}, notFound(id)
	}
	return j, err
}

func (s *SQLiteStore) List(ctx context.Context, q ListQuery) ([]Job, error) {
	var where []string
	var args []any
	if len(q.Statuses) > 0 {
		where = append(where, "status IN ("+strings.TrimSuffix(strings.Repeat("?,", len(q.Statuses)), ",")+")")
		for _, st := range q.Statuses {
			args = append(args, st)
		}
	}
	if q.Assignee != "" {
		where = append(where, "assignee = ?")
		args = append(args, q.Assignee)
	}
	if q.RequestedBy != "" {
		where = append(where, "requested_by = ?")
		args = append(args, q.RequestedBy)
	}
	if q.ParentID != nil {
		where = append(where, "parent_id = ?")
		args = append(args, *q.ParentID)
	}
	if q.MaxPriority > 0 {
		where = append(where, "priority <= ?")
		args = append(args, q.MaxPriority)
	}
	query := `SELECT ` + jobColumns + ` FROM jobs`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY priority, id LIMIT ? OFFSET ?"
	args = append(args, q.Limit, q.Offset)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	defer rows.Close()
	jobs := []Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, j.Job)
	}
	return jobs, rows.Err()
}

func (s *SQLiteStore) Events(ctx context.Context, id int64) ([]Event, error) {
	if _, err := s.Get(ctx, id); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, job_id, kind, actor, note, at, data FROM job_events WHERE job_id = ? ORDER BY id`, id)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()
	events := []Event{}
	for rows.Next() {
		var ev Event
		var at, data string
		if err := rows.Scan(&ev.ID, &ev.JobID, &ev.Kind, &ev.Actor, &ev.Note, &at, &data); err != nil {
			return nil, err
		}
		if ev.At, err = parseTime(at); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(data), &ev.Data); err != nil {
			return nil, fmt.Errorf("decode event %d data: %w", ev.ID, err)
		}
		events = append(events, ev)
	}
	return events, rows.Err()
}

func (s *SQLiteStore) Update(ctx context.Context, id int64, now time.Time, fn mutation) (stored, error) {
	// Expire first, in its own transaction, so the expiry stands even when fn
	// then rejects the call (for example a renew on the lapsed lease).
	if err := s.expireOne(ctx, id, now); err != nil {
		return stored{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return stored{}, fmt.Errorf("begin update: %w", err)
	}
	defer tx.Rollback()
	cur, err := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return stored{}, notFound(id)
	}
	if err != nil {
		return stored{}, err
	}
	next, events, err := fn(cur)
	if err != nil {
		return stored{}, err
	}
	if err := writeJob(ctx, tx, next); err != nil {
		return stored{}, err
	}
	for _, ev := range events {
		if err := insertEvent(ctx, tx, ev); err != nil {
			return stored{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return stored{}, fmt.Errorf("commit update: %w", err)
	}
	return next, nil
}

func (s *SQLiteStore) expireOne(ctx context.Context, id int64, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin expiry: %w", err)
	}
	defer tx.Rollback()
	cur, err := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return notFound(id)
	}
	if err != nil {
		return err
	}
	if !leaseOver(cur, now) {
		return nil
	}
	next, ev := expire(cur, now)
	if err := writeJob(ctx, tx, next); err != nil {
		return err
	}
	if err := insertEvent(ctx, tx, ev); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit expiry: %w", err)
	}
	return nil
}

func writeJob(ctx context.Context, tx *sql.Tx, j stored) error {
	pages, _ := json.Marshal(j.OutputPages)
	_, err := tx.ExecContext(ctx, `UPDATE jobs SET question = ?, scope_notes = ?, assignee = ?, harness = ?, status = ?, parent_id = ?,
		priority = ?, trail_slug = ?, output_pages = ?, lease_token = ?, lease_expires_at = ?, updated_at = ?, claimed_at = ?, finished_at = ?
		WHERE id = ?`,
		j.Question, j.ScopeNotes, j.Assignee, j.Harness, j.Status, nullInt(j.ParentID),
		j.Priority, j.TrailSlug, string(pages), j.LeaseToken, nullTime(j.LeaseExpiresAt), formatTime(j.UpdatedAt),
		nullTime(j.ClaimedAt), nullTime(j.FinishedAt), j.ID)
	if err != nil {
		return fmt.Errorf("update job %d: %w", j.ID, err)
	}
	return nil
}

func (s *SQLiteStore) ExpireLeases(ctx context.Context, now time.Time) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin expiry: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE status = ? AND lease_expires_at <= ?`, StatusClaimed, formatTime(now))
	if err != nil {
		return 0, fmt.Errorf("find expired leases: %w", err)
	}
	var due []stored
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			rows.Close()
			return 0, err
		}
		due = append(due, j)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, j := range due {
		next, ev := expire(j, now)
		if err := writeJob(ctx, tx, next); err != nil {
			return 0, err
		}
		if err := insertEvent(ctx, tx, ev); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit expiry: %w", err)
	}
	return len(due), nil
}
