package facade

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// ReadEntry is one durable read-log record: which agent asked which read
// tool what, and which page paths came back or were opened. It is training
// data for the future routing classifier, so it is kept from the start.
type ReadEntry struct {
	ID       int64     `json:"id"`
	At       time.Time `json:"at"`
	Agent    string    `json:"agent"`
	Tool     string    `json:"tool"`
	Query    string    `json:"query,omitempty"`
	Returned []string  `json:"returned"`
	Opened   []string  `json:"opened"`
}

// ReadLog stores read entries.
type ReadLog interface {
	Record(ctx context.Context, e ReadEntry) error
	Recent(ctx context.Context, limit int) ([]ReadEntry, error)
}

// SQLiteReadLog keeps the read log in a SQLite table.
type SQLiteReadLog struct {
	db *sql.DB
}

// OpenReadLog opens (creating if needed) the read log at path. It may share
// a file with the ledger.
func OpenReadLog(ctx context.Context, path string) (*SQLiteReadLog, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_txlock=immediate")
	if err != nil {
		return nil, fmt.Errorf("open read log: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS read_log (
		id       INTEGER PRIMARY KEY AUTOINCREMENT,
		at       TEXT NOT NULL,
		agent    TEXT NOT NULL,
		tool     TEXT NOT NULL,
		query    TEXT NOT NULL DEFAULT '',
		returned TEXT NOT NULL DEFAULT '[]',
		opened   TEXT NOT NULL DEFAULT '[]'
	)`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("create read log: %w", err)
	}
	return &SQLiteReadLog{db: db}, nil
}

func (l *SQLiteReadLog) Close() error { return l.db.Close() }

func (l *SQLiteReadLog) Record(ctx context.Context, e ReadEntry) error {
	ret, _ := json.Marshal(nonNil(e.Returned))
	op, _ := json.Marshal(nonNil(e.Opened))
	_, err := l.db.ExecContext(ctx, `INSERT INTO read_log (at, agent, tool, query, returned, opened) VALUES (?, ?, ?, ?, ?, ?)`,
		e.At.UTC().Format(time.RFC3339Nano), e.Agent, e.Tool, e.Query, string(ret), string(op))
	return err
}

func (l *SQLiteReadLog) Recent(ctx context.Context, limit int) ([]ReadEntry, error) {
	rows, err := l.db.QueryContext(ctx, `SELECT id, at, agent, tool, query, returned, opened FROM read_log ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReadEntry
	for rows.Next() {
		var e ReadEntry
		var at, ret, op string
		if err := rows.Scan(&e.ID, &at, &e.Agent, &e.Tool, &e.Query, &ret, &op); err != nil {
			return nil, err
		}
		e.At, _ = time.Parse(time.RFC3339Nano, at)
		_ = json.Unmarshal([]byte(ret), &e.Returned)
		_ = json.Unmarshal([]byte(op), &e.Opened)
		out = append(out, e)
	}
	return out, rows.Err()
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
