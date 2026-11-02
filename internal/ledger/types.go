// Package ledger is den-wiki's research job queue: a thin port of the
// den-services tasks model (job records, status transitions, parent links,
// bounded listing, coded errors) without review, CI or GitHub machinery.
// Claims are atomic and leased; every change is an append-only event.
package ledger

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Job statuses: open → claimed → done | abandoned. An expired or released
// claim returns the job to open.
const (
	StatusOpen      = "open"
	StatusClaimed   = "claimed"
	StatusDone      = "done"
	StatusAbandoned = "abandoned"
)

// Job kinds, the kind of run a job asks for. The names match den-wiki's
// trail kinds, so a job's kind says what its trail will be.
const (
	KindResearch       = "research"
	KindRoundSynthesis = "round-synthesis"
	KindCalibration    = "calibration"
	KindCuration       = "curation"
)

var kinds = []string{KindResearch, KindRoundSynthesis, KindCalibration, KindCuration}

func validKind(kind string) bool {
	for _, k := range kinds {
		if k == kind {
			return true
		}
	}
	return false
}

func checkKind(kind string) (string, error) {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return KindResearch, nil
	}
	if !validKind(kind) {
		return "", fmt.Errorf("unknown kind %q (want %s)", kind, strings.Join(kinds, ", "))
	}
	return kind, nil
}

// Event kinds, one per state change.
const (
	EventCreated   = "created"
	EventClaimed   = "claimed"
	EventRenewed   = "renewed"
	EventReleased  = "released"
	EventExpired   = "expired"
	EventFinished  = "finished"
	EventAbandoned = "abandoned"
	EventEdited    = "edited"
)

// Bounds on input. Lease lengths are seconds.
const (
	DefaultLeaseSeconds = 30 * 60
	MinLeaseSeconds     = 60
	MaxLeaseSeconds     = 24 * 60 * 60
	DefaultListLimit    = 50
	MaxListLimit        = 200
	maxTextLen          = 16 * 1024
	maxOutputPages      = 200
)

// Job is one research job as stored. Values returned by the service are
// snapshots; changing them changes nothing.
type Job struct {
	ID             int64      `json:"id"`
	Kind           string     `json:"kind"`
	Question       string     `json:"question"`
	ScopeNotes     string     `json:"scope_notes,omitempty"`
	RequestedBy    string     `json:"requested_by"`
	Assignee       string     `json:"assignee,omitempty"`
	Harness        string     `json:"harness,omitempty"`
	Status         string     `json:"status"`
	ParentID       *int64     `json:"parent_id,omitempty"`
	Priority       int        `json:"priority"`
	TrailSlug      string     `json:"trail_slug,omitempty"`
	OutputPages    []string   `json:"output_pages"`
	LeaseExpiresAt *time.Time `json:"lease_expires_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	ClaimedAt      *time.Time `json:"claimed_at,omitempty"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
}

// Claim is a job together with the lease token that proves the claim. Only
// the claimer sees the token; it is needed to renew, release or finish.
type Claim struct {
	Job        Job    `json:"job"`
	LeaseToken string `json:"lease_token"`
}

// Event is one append-only log entry. Data holds the fields the event set,
// so replaying a job's events reconstructs it.
type Event struct {
	ID    int64          `json:"id"`
	JobID int64          `json:"job_id"`
	Kind  string         `json:"kind"`
	Actor string         `json:"actor"`
	Note  string         `json:"note,omitempty"`
	At    time.Time      `json:"at"`
	Data  map[string]any `json:"data,omitempty"`
}

// Page is one bounded slice of a listing. NextOffset is nil on the last page.
type Page struct {
	Jobs       []Job `json:"jobs"`
	NextOffset *int  `json:"next_offset"`
}

func terminal(status string) bool { return status == StatusDone || status == StatusAbandoned }

func validStatus(status string) bool {
	switch status {
	case StatusOpen, StatusClaimed, StatusDone, StatusAbandoned:
		return true
	}
	return false
}

var slugRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

var (
	errMissingActor = errors.New("actor is required")
	errMissingToken = errors.New("lease_token is required")
	errPriority     = errors.New("priority must be 1 through 5")
)

func requireText(field, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s is required", field)
	}
	if len(value) > maxTextLen {
		return "", fmt.Errorf("%s is longer than %d bytes", field, maxTextLen)
	}
	return value, nil
}

func optionalText(field, value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) > maxTextLen {
		return "", fmt.Errorf("%s is longer than %d bytes", field, maxTextLen)
	}
	return value, nil
}

func checkPriority(p int) (int, error) {
	if p == 0 {
		return 3, nil
	}
	if p < 1 || p > 5 {
		return 0, errPriority
	}
	return p, nil
}

// normalisePages trims, de-duplicates and checks output page paths, which are
// content-root-relative paths under wiki/.
func normalisePages(pages []string) ([]string, error) {
	if len(pages) > maxOutputPages {
		return nil, fmt.Errorf("output_pages has more than %d entries", maxOutputPages)
	}
	out := []string{}
	seen := map[string]bool{}
	for _, p := range pages {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		if !strings.HasPrefix(p, "wiki/") || !strings.HasSuffix(p, ".md") || strings.Contains(p, "..") {
			return nil, fmt.Errorf("output page %q is not a wiki/*.md path", p)
		}
		seen[p] = true
		out = append(out, p)
	}
	return out, nil
}
