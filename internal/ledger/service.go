package ledger

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Service holds the ledger's rules: validation, status transitions, leases
// and the events that record them. The store only persists.
type Service struct {
	store Store
	clock func() time.Time
	// contentDir, when set, is the den-wiki content directory; finish then
	// requires trails/<slug>.md to exist in it.
	contentDir string
	newToken   func() string
}

// Option configures a Service.
type Option func(*Service)

// WithContentDir makes finish check that the named trail page exists.
func WithContentDir(dir string) Option { return func(s *Service) { s.contentDir = dir } }

// WithTokenSource replaces the random lease-token generator (tests only).
func WithTokenSource(fn func() string) Option { return func(s *Service) { s.newToken = fn } }

// NewService builds a Service over store, reading time from clock.
func NewService(store Store, clock func() time.Time, opts ...Option) *Service {
	s := &Service{store: store, clock: clock, newToken: randomToken}
	for _, o := range opts {
		o(s)
	}
	return s
}

func randomToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("read random lease token: %v", err))
	}
	return hex.EncodeToString(b)
}

func (s *Service) now() time.Time { return s.clock().UTC() }

// Ping checks the store.
func (s *Service) Ping(ctx context.Context) error { return s.store.Ping(ctx) }

// CreateRequest opens a new research job.
type CreateRequest struct {
	Question    string `json:"question"`
	ScopeNotes  string `json:"scope_notes,omitempty"`
	RequestedBy string `json:"requested_by"`
	ParentID    *int64 `json:"parent_id,omitempty"`
	Priority    int    `json:"priority,omitempty"`
}

// ClaimRequest claims an open job for Actor, leased for LeaseSeconds
// (default 30 minutes).
type ClaimRequest struct {
	Actor        string `json:"actor"`
	Harness      string `json:"harness,omitempty"`
	LeaseSeconds int    `json:"lease_seconds,omitempty"`
	Note         string `json:"note,omitempty"`
}

// RenewRequest extends a live lease.
type RenewRequest struct {
	Actor        string `json:"actor"`
	LeaseToken   string `json:"lease_token"`
	LeaseSeconds int    `json:"lease_seconds,omitempty"`
	Note         string `json:"note,omitempty"`
}

// ReleaseRequest gives a claimed job back to the queue.
type ReleaseRequest struct {
	Actor      string `json:"actor"`
	LeaseToken string `json:"lease_token"`
	Note       string `json:"note,omitempty"`
}

// FinishRequest completes a claimed job. A finished job must name its trail.
type FinishRequest struct {
	Actor       string   `json:"actor"`
	LeaseToken  string   `json:"lease_token"`
	TrailSlug   string   `json:"trail_slug"`
	OutputPages []string `json:"output_pages,omitempty"`
	Note        string   `json:"note,omitempty"`
}

// AbandonRequest closes a job without finishing it. A claimed job can be
// abandoned only by its lease holder.
type AbandonRequest struct {
	Actor      string `json:"actor"`
	LeaseToken string `json:"lease_token,omitempty"`
	Note       string `json:"note,omitempty"`
}

// EditRequest changes a job that is not finished or abandoned. Nil fields are
// left alone; ClearParent removes the parent link.
type EditRequest struct {
	Actor       string  `json:"actor"`
	Question    *string `json:"question,omitempty"`
	ScopeNotes  *string `json:"scope_notes,omitempty"`
	Priority    *int    `json:"priority,omitempty"`
	ParentID    *int64  `json:"parent_id,omitempty"`
	ClearParent bool    `json:"clear_parent,omitempty"`
	Note        string  `json:"note,omitempty"`
}

// ListRequest filters a bounded listing; see ListQuery.
type ListRequest struct {
	Statuses    []string `json:"statuses,omitempty"`
	Assignee    string   `json:"assignee,omitempty"`
	RequestedBy string   `json:"requested_by,omitempty"`
	ParentID    *int64   `json:"parent_id,omitempty"`
	MaxPriority int      `json:"max_priority,omitempty"`
	Limit       int      `json:"limit,omitempty"`
	Offset      int      `json:"offset,omitempty"`
}

func actor(a string) (string, error) {
	a = strings.TrimSpace(a)
	if a == "" {
		return "", validationFailed(errMissingActor)
	}
	return a, nil
}

func leaseLength(seconds int) (time.Duration, error) {
	if seconds == 0 {
		seconds = DefaultLeaseSeconds
	}
	if seconds < MinLeaseSeconds || seconds > MaxLeaseSeconds {
		return 0, validationFailed(fmt.Errorf("lease_seconds must be %d through %d", MinLeaseSeconds, MaxLeaseSeconds))
	}
	return time.Duration(seconds) * time.Second, nil
}

func timePtr(t time.Time) *time.Time { return &t }

// Create opens a job.
func (s *Service) Create(ctx context.Context, req CreateRequest) (Job, error) {
	question, err := requireText("question", req.Question)
	if err != nil {
		return Job{}, validationFailed(err)
	}
	by, err := requireText("requested_by", req.RequestedBy)
	if err != nil {
		return Job{}, validationFailed(err)
	}
	scope, err := optionalText("scope_notes", req.ScopeNotes)
	if err != nil {
		return Job{}, validationFailed(err)
	}
	priority, err := checkPriority(req.Priority)
	if err != nil {
		return Job{}, validationFailed(err)
	}
	now := s.now()
	j := stored{Job: Job{
		Question: question, ScopeNotes: scope, RequestedBy: by, Status: StatusOpen,
		ParentID: req.ParentID, Priority: priority, OutputPages: []string{},
		CreatedAt: now, UpdatedAt: now,
	}}
	ev := Event{Kind: EventCreated, Actor: by, At: now, Data: map[string]any{
		"question": question, "scope_notes": scope, "requested_by": by, "status": StatusOpen,
		"parent_id": req.ParentID, "priority": priority,
	}}
	created, err := s.store.Create(ctx, j, ev)
	return created.Job, err
}

// Get returns one job.
func (s *Service) Get(ctx context.Context, id int64) (Job, error) {
	if _, err := s.store.ExpireLeases(ctx, s.now()); err != nil {
		return Job{}, err
	}
	j, err := s.store.Get(ctx, id)
	return j.Job, err
}

// List returns one bounded page of jobs, highest priority (lowest number)
// first.
func (s *Service) List(ctx context.Context, req ListRequest) (Page, error) {
	for _, st := range req.Statuses {
		if !validStatus(st) {
			return Page{}, validationFailed(fmt.Errorf("unknown status %q (want open, claimed, done or abandoned)", st))
		}
	}
	if req.MaxPriority < 0 || req.MaxPriority > 5 {
		return Page{}, validationFailed(errPriority)
	}
	limit := req.Limit
	if limit == 0 {
		limit = DefaultListLimit
	}
	if limit < 1 || limit > MaxListLimit {
		return Page{}, validationFailed(fmt.Errorf("limit must be 1 through %d", MaxListLimit))
	}
	if req.Offset < 0 {
		return Page{}, validationFailed(errors.New("offset must not be negative"))
	}
	if _, err := s.store.ExpireLeases(ctx, s.now()); err != nil {
		return Page{}, err
	}
	jobs, err := s.store.List(ctx, ListQuery{
		Statuses: req.Statuses, Assignee: strings.TrimSpace(req.Assignee), RequestedBy: strings.TrimSpace(req.RequestedBy),
		ParentID: req.ParentID, MaxPriority: req.MaxPriority, Limit: limit + 1, Offset: req.Offset,
	})
	if err != nil {
		return Page{}, err
	}
	page := Page{Jobs: jobs}
	if len(jobs) > limit {
		page.Jobs = jobs[:limit]
		next := req.Offset + limit
		page.NextOffset = &next
	}
	return page, nil
}

// Events returns a job's full event log, oldest first.
func (s *Service) Events(ctx context.Context, id int64) ([]Event, error) {
	if _, err := s.store.ExpireLeases(ctx, s.now()); err != nil {
		return nil, err
	}
	return s.store.Events(ctx, id)
}

// ExpireLeases returns every lapsed claim to open. The service also does
// this lazily before each read and write, so calling it is optional.
func (s *Service) ExpireLeases(ctx context.Context) (int, error) {
	return s.store.ExpireLeases(ctx, s.now())
}

// Claim atomically claims an open job. Exactly one of several concurrent
// claimers wins; the rest get claim_conflict.
func (s *Service) Claim(ctx context.Context, id int64, req ClaimRequest) (Claim, error) {
	who, err := actor(req.Actor)
	if err != nil {
		return Claim{}, err
	}
	lease, err := leaseLength(req.LeaseSeconds)
	if err != nil {
		return Claim{}, err
	}
	harness, err := optionalText("harness", req.Harness)
	if err != nil {
		return Claim{}, validationFailed(err)
	}
	now := s.now()
	token := s.newToken()
	j, err := s.store.Update(ctx, id, now, func(cur stored) (stored, []Event, error) {
		switch cur.Status {
		case StatusClaimed:
			return stored{}, nil, claimConflict("job %d is already claimed by %s until %s", id, cur.Assignee, cur.LeaseExpiresAt.Format(time.RFC3339))
		case StatusOpen:
		default:
			return stored{}, nil, invalidTransition("job %d is %s and cannot be claimed", id, cur.Status)
		}
		next := cur
		expires := now.Add(lease)
		next.Status, next.Assignee, next.Harness = StatusClaimed, who, harness
		next.LeaseToken, next.LeaseExpiresAt = token, &expires
		next.ClaimedAt, next.UpdatedAt = timePtr(now), now
		return next, []Event{{JobID: id, Kind: EventClaimed, Actor: who, Note: strings.TrimSpace(req.Note), At: now, Data: map[string]any{
			"status": StatusClaimed, "assignee": who, "harness": harness, "lease_expires_at": formatTime(expires),
		}}}, nil
	})
	if err != nil {
		return Claim{}, err
	}
	return Claim{Job: j.Job, LeaseToken: token}, nil
}

// holder checks that the caller holds the job's current lease.
func holder(id int64, cur stored, token string) error {
	if strings.TrimSpace(token) == "" {
		return validationFailed(errMissingToken)
	}
	if cur.Status == StatusClaimed && cur.LeaseToken == token {
		return nil
	}
	if cur.Status == StatusOpen && cur.LeaseToken == token {
		// Update kept the token when the lease lapsed.
		return leaseExpired(id)
	}
	if cur.Status == StatusClaimed {
		return claimConflict("job %d is claimed under a different lease", id)
	}
	return invalidTransition("job %d is %s, not claimed", id, cur.Status)
}

// Renew extends the caller's live lease.
func (s *Service) Renew(ctx context.Context, id int64, req RenewRequest) (Job, error) {
	who, err := actor(req.Actor)
	if err != nil {
		return Job{}, err
	}
	lease, err := leaseLength(req.LeaseSeconds)
	if err != nil {
		return Job{}, err
	}
	now := s.now()
	j, err := s.store.Update(ctx, id, now, func(cur stored) (stored, []Event, error) {
		if err := holder(id, cur, req.LeaseToken); err != nil {
			return stored{}, nil, err
		}
		next := cur
		expires := now.Add(lease)
		next.LeaseExpiresAt, next.UpdatedAt = &expires, now
		return next, []Event{{JobID: id, Kind: EventRenewed, Actor: who, Note: strings.TrimSpace(req.Note), At: now, Data: map[string]any{
			"lease_expires_at": formatTime(expires),
		}}}, nil
	})
	return j.Job, err
}

// Release returns the caller's claimed job to open.
func (s *Service) Release(ctx context.Context, id int64, req ReleaseRequest) (Job, error) {
	who, err := actor(req.Actor)
	if err != nil {
		return Job{}, err
	}
	now := s.now()
	j, err := s.store.Update(ctx, id, now, func(cur stored) (stored, []Event, error) {
		if err := holder(id, cur, req.LeaseToken); err != nil {
			return stored{}, nil, err
		}
		next := reopen(cur, now)
		next.LeaseToken = ""
		return next, []Event{{JobID: id, Kind: EventReleased, Actor: who, Note: strings.TrimSpace(req.Note), At: now, Data: reopenedData()}}, nil
	})
	return j.Job, err
}

// Finish completes the caller's claimed job. It needs a trail slug and, when
// the service has a content directory, the trail page itself.
func (s *Service) Finish(ctx context.Context, id int64, req FinishRequest) (Job, error) {
	who, err := actor(req.Actor)
	if err != nil {
		return Job{}, err
	}
	slug := strings.TrimSpace(req.TrailSlug)
	if slug == "" {
		return Job{}, validationFailed(errors.New("trail_slug is required: a finished job must name its trail"))
	}
	if !slugRe.MatchString(slug) {
		return Job{}, validationFailed(fmt.Errorf("trail_slug %q is not a lowercase slug", slug))
	}
	if s.contentDir != "" {
		p := filepath.Join(s.contentDir, "trails", slug+".md")
		if _, err := os.Stat(p); err != nil {
			return Job{}, validationFailed(fmt.Errorf("trail page trails/%s.md does not exist in the content directory; write the trail before finishing", slug))
		}
	}
	pages, err := normalisePages(req.OutputPages)
	if err != nil {
		return Job{}, validationFailed(err)
	}
	now := s.now()
	j, err := s.store.Update(ctx, id, now, func(cur stored) (stored, []Event, error) {
		if err := holder(id, cur, req.LeaseToken); err != nil {
			return stored{}, nil, err
		}
		next := cur
		next.Status, next.TrailSlug, next.OutputPages = StatusDone, slug, pages
		next.LeaseExpiresAt, next.FinishedAt, next.UpdatedAt = nil, timePtr(now), now
		return next, []Event{{JobID: id, Kind: EventFinished, Actor: who, Note: strings.TrimSpace(req.Note), At: now, Data: map[string]any{
			"status": StatusDone, "trail_slug": slug, "output_pages": pages,
		}}}, nil
	})
	return j.Job, err
}

// Abandon closes a job without a result.
func (s *Service) Abandon(ctx context.Context, id int64, req AbandonRequest) (Job, error) {
	who, err := actor(req.Actor)
	if err != nil {
		return Job{}, err
	}
	now := s.now()
	j, err := s.store.Update(ctx, id, now, func(cur stored) (stored, []Event, error) {
		switch {
		case terminal(cur.Status):
			return stored{}, nil, invalidTransition("job %d is already %s", id, cur.Status)
		case cur.Status == StatusClaimed:
			if err := holder(id, cur, req.LeaseToken); err != nil {
				return stored{}, nil, err
			}
		}
		next := cur
		next.Status, next.LeaseExpiresAt = StatusAbandoned, nil
		next.FinishedAt, next.UpdatedAt = timePtr(now), now
		return next, []Event{{JobID: id, Kind: EventAbandoned, Actor: who, Note: strings.TrimSpace(req.Note), At: now, Data: map[string]any{
			"status": StatusAbandoned,
		}}}, nil
	})
	return j.Job, err
}

// Edit changes the question, scope, priority or parent of a job that is not
// finished or abandoned.
func (s *Service) Edit(ctx context.Context, id int64, req EditRequest) (Job, error) {
	who, err := actor(req.Actor)
	if err != nil {
		return Job{}, err
	}
	if req.Question == nil && req.ScopeNotes == nil && req.Priority == nil && req.ParentID == nil && !req.ClearParent {
		return Job{}, validationFailed(errors.New("edit changes nothing: give question, scope_notes, priority, parent_id or clear_parent"))
	}
	if req.ParentID != nil && req.ClearParent {
		return Job{}, validationFailed(errors.New("give parent_id or clear_parent, not both"))
	}
	changes := map[string]any{}
	if req.Question != nil {
		q, err := requireText("question", *req.Question)
		if err != nil {
			return Job{}, validationFailed(err)
		}
		changes["question"] = q
	}
	if req.ScopeNotes != nil {
		sc, err := optionalText("scope_notes", *req.ScopeNotes)
		if err != nil {
			return Job{}, validationFailed(err)
		}
		changes["scope_notes"] = sc
	}
	if req.Priority != nil {
		if *req.Priority < 1 || *req.Priority > 5 {
			return Job{}, validationFailed(errPriority)
		}
		changes["priority"] = *req.Priority
	}
	if req.ParentID != nil {
		if err := s.checkParent(ctx, id, *req.ParentID); err != nil {
			return Job{}, err
		}
		changes["parent_id"] = *req.ParentID
	}
	if req.ClearParent {
		changes["parent_id"] = nil
	}
	now := s.now()
	j, err := s.store.Update(ctx, id, now, func(cur stored) (stored, []Event, error) {
		if terminal(cur.Status) {
			return stored{}, nil, invalidTransition("job %d is %s and can no longer be edited", id, cur.Status)
		}
		next := cur
		if v, ok := changes["question"].(string); ok {
			next.Question = v
		}
		if v, ok := changes["scope_notes"].(string); ok {
			next.ScopeNotes = v
		}
		if v, ok := changes["priority"].(int); ok {
			next.Priority = v
		}
		if req.ParentID != nil {
			p := *req.ParentID
			next.ParentID = &p
		}
		if req.ClearParent {
			next.ParentID = nil
		}
		next.UpdatedAt = now
		return next, []Event{{JobID: id, Kind: EventEdited, Actor: who, Note: strings.TrimSpace(req.Note), At: now, Data: changes}}, nil
	})
	return j.Job, err
}

// checkParent rejects a parent that does not exist or would make a cycle.
func (s *Service) checkParent(ctx context.Context, id, parent int64) error {
	seen := map[int64]bool{id: true}
	for cur := parent; ; {
		if seen[cur] {
			return validationFailed(fmt.Errorf("parent %d would make a cycle", parent))
		}
		seen[cur] = true
		j, err := s.store.Get(ctx, cur)
		if err != nil {
			if CodeOf(err) == CodeNotFound {
				return validationFailed(fmt.Errorf("parent job %d does not exist", cur))
			}
			return err
		}
		if j.ParentID == nil {
			return nil
		}
		cur = *j.ParentID
	}
}

// leaseOver reports whether a claimed job's lease has run out at now.
func leaseOver(j stored, now time.Time) bool {
	return j.Status == StatusClaimed && j.LeaseExpiresAt != nil && !now.Before(*j.LeaseExpiresAt)
}

// reopen returns a claimed job to open.
func reopen(cur stored, now time.Time) stored {
	next := cur
	next.Status, next.Assignee, next.Harness = StatusOpen, "", ""
	next.LeaseExpiresAt, next.UpdatedAt = nil, now
	return next
}

func reopenedData() map[string]any {
	return map[string]any{"status": StatusOpen, "assignee": "", "harness": "", "lease_expires_at": nil}
}

// expire returns a lapsed claim to open. It keeps the lease token so the old
// holder gets lease_expired, not a generic conflict, on its next call.
func expire(cur stored, now time.Time) (stored, Event) {
	return reopen(cur, now), Event{
		JobID: cur.ID, Kind: EventExpired, Actor: "ledger", At: now,
		Note: "lease held by " + cur.Assignee + " expired", Data: reopenedData(),
	}
}
