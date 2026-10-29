package ledger

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func openStore(t *testing.T, path string) *SQLiteStore {
	t.Helper()
	st, err := OpenSQLite(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func newTestService(t *testing.T, opts ...Option) (*Service, *fakeClock, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ledger.db")
	clock := &fakeClock{now: time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)}
	return NewService(openStore(t, path), clock.Now, opts...), clock, path
}

func mustCreate(t *testing.T, s *Service, question string) Job {
	t.Helper()
	j, err := s.Create(context.Background(), CreateRequest{Question: question, RequestedBy: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("want %s, got success", code)
	}
	if got := CodeOf(err); got != code {
		t.Fatalf("want %s, got %s (%v)", code, got, err)
	}
}

func TestCreateValidates(t *testing.T) {
	s, _, _ := newTestService(t)
	ctx := context.Background()
	parent := int64(99)
	cases := []CreateRequest{
		{RequestedBy: "owner"},
		{Question: "  ", RequestedBy: "owner"},
		{Question: "q"},
		{Question: "q", RequestedBy: "owner", Priority: 6},
		{Question: "q", RequestedBy: "owner", ParentID: &parent},
	}
	for _, c := range cases {
		_, err := s.Create(ctx, c)
		wantCode(t, err, CodeValidationFailed)
	}
	j := mustCreate(t, s, "How do agents keep memory?")
	if j.ID == 0 || j.Status != StatusOpen || j.Priority != 3 || j.OutputPages == nil {
		t.Fatalf("unexpected job %+v", j)
	}
}

func TestConcurrentClaimsExactlyOneWins(t *testing.T) {
	s, _, _ := newTestService(t)
	j := mustCreate(t, s, "q")
	const n = 32
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Claim(context.Background(), j.ID, ClaimRequest{Actor: fmt.Sprintf("agent-%d", i)})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	wins := 0
	for err := range errs {
		if err == nil {
			wins++
			continue
		}
		wantCode(t, err, CodeClaimConflict)
	}
	if wins != 1 {
		t.Fatalf("%d claims won, want exactly 1", wins)
	}
}

// Two services on one database file stand in for two processes.
func TestConcurrentClaimsAcrossStoresExactlyOneWins(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	clock := &fakeClock{now: time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)}
	a := NewService(openStore(t, path), clock.Now)
	b := NewService(openStore(t, path), clock.Now)
	for round := range 10 {
		j := mustCreate(t, a, fmt.Sprintf("q%d", round))
		var wg sync.WaitGroup
		results := make(chan error, 8)
		for i := range 8 {
			svc := a
			if i%2 == 1 {
				svc = b
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := svc.Claim(context.Background(), j.ID, ClaimRequest{Actor: fmt.Sprintf("agent-%d", i)})
				results <- err
			}()
		}
		wg.Wait()
		close(results)
		wins := 0
		for err := range results {
			if err == nil {
				wins++
			} else if CodeOf(err) != CodeClaimConflict {
				t.Fatalf("round %d: unexpected error %v", round, err)
			}
		}
		if wins != 1 {
			t.Fatalf("round %d: %d claims won, want 1", round, wins)
		}
	}
}

func TestLeaseExpiryReturnsJobToOpen(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	j := mustCreate(t, s, "q")
	c, err := s.Claim(ctx, j.ID, ClaimRequest{Actor: "alice", LeaseSeconds: 60})
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(59 * time.Second)
	if got, _ := s.Get(ctx, j.ID); got.Status != StatusClaimed {
		t.Fatalf("lease ended early: %+v", got)
	}
	clock.Advance(time.Second)
	got, err := s.Get(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusOpen || got.Assignee != "" || got.LeaseExpiresAt != nil {
		t.Fatalf("job not reopened: %+v", got)
	}
	evs, _ := s.Events(ctx, j.ID)
	if last := evs[len(evs)-1]; last.Kind != EventExpired || last.Actor != "ledger" {
		t.Fatalf("no expired event: %+v", evs)
	}
	_, err = s.Renew(ctx, j.ID, RenewRequest{Actor: "alice", LeaseToken: c.LeaseToken})
	wantCode(t, err, CodeLeaseExpired)
	_, err = s.Finish(ctx, j.ID, FinishRequest{Actor: "alice", LeaseToken: c.LeaseToken, TrailSlug: "run"})
	wantCode(t, err, CodeLeaseExpired)

	c2, err := s.Claim(ctx, j.ID, ClaimRequest{Actor: "bob"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Renew(ctx, j.ID, RenewRequest{Actor: "alice", LeaseToken: c.LeaseToken})
	wantCode(t, err, CodeClaimConflict)
	if _, err := s.Renew(ctx, j.ID, RenewRequest{Actor: "bob", LeaseToken: c2.LeaseToken, LeaseSeconds: 120}); err != nil {
		t.Fatal(err)
	}
}

// Expiry found by a mutation stands even though the mutation is rejected.
func TestExpiryOnMutationPathIsRecorded(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	j := mustCreate(t, s, "q")
	c, _ := s.Claim(ctx, j.ID, ClaimRequest{Actor: "alice", LeaseSeconds: 60})
	clock.Advance(2 * time.Minute)
	_, err := s.Renew(ctx, j.ID, RenewRequest{Actor: "alice", LeaseToken: c.LeaseToken})
	wantCode(t, err, CodeLeaseExpired)
	evs, _ := s.store.Events(ctx, j.ID)
	if evs[len(evs)-1].Kind != EventExpired {
		t.Fatalf("expiry not recorded: %+v", evs)
	}
}

func TestExpireLeasesSweepsAll(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	for i := range 3 {
		j := mustCreate(t, s, fmt.Sprintf("q%d", i))
		if _, err := s.Claim(ctx, j.ID, ClaimRequest{Actor: "a", LeaseSeconds: 60 * (i + 1)}); err != nil {
			t.Fatal(err)
		}
	}
	clock.Advance(150 * time.Second)
	n, err := s.ExpireLeases(ctx)
	if err != nil || n != 2 {
		t.Fatalf("expired %d (%v), want 2", n, err)
	}
}

func TestInvalidTransitionsRejected(t *testing.T) {
	s, _, _ := newTestService(t)
	ctx := context.Background()

	open := mustCreate(t, s, "open")
	_, err := s.Renew(ctx, open.ID, RenewRequest{Actor: "a", LeaseToken: "nope"})
	wantCode(t, err, CodeInvalidTransition)
	_, err = s.Release(ctx, open.ID, ReleaseRequest{Actor: "a", LeaseToken: "nope"})
	wantCode(t, err, CodeInvalidTransition)
	_, err = s.Finish(ctx, open.ID, FinishRequest{Actor: "a", LeaseToken: "nope", TrailSlug: "run"})
	wantCode(t, err, CodeInvalidTransition)

	done := mustCreate(t, s, "done")
	c, _ := s.Claim(ctx, done.ID, ClaimRequest{Actor: "a"})
	if _, err := s.Finish(ctx, done.ID, FinishRequest{Actor: "a", LeaseToken: c.LeaseToken, TrailSlug: "run"}); err != nil {
		t.Fatal(err)
	}
	_, err = s.Claim(ctx, done.ID, ClaimRequest{Actor: "b"})
	wantCode(t, err, CodeInvalidTransition)
	_, err = s.Abandon(ctx, done.ID, AbandonRequest{Actor: "b"})
	wantCode(t, err, CodeInvalidTransition)
	q := "new question"
	_, err = s.Edit(ctx, done.ID, EditRequest{Actor: "b", Question: &q})
	wantCode(t, err, CodeInvalidTransition)
	_, err = s.Finish(ctx, done.ID, FinishRequest{Actor: "a", LeaseToken: c.LeaseToken, TrailSlug: "run"})
	wantCode(t, err, CodeInvalidTransition)

	claimed := mustCreate(t, s, "claimed")
	c, _ = s.Claim(ctx, claimed.ID, ClaimRequest{Actor: "a"})
	_, err = s.Claim(ctx, claimed.ID, ClaimRequest{Actor: "b"})
	wantCode(t, err, CodeClaimConflict)
	_, err = s.Abandon(ctx, claimed.ID, AbandonRequest{Actor: "b", LeaseToken: "wrong"})
	wantCode(t, err, CodeClaimConflict)
	_, err = s.Abandon(ctx, claimed.ID, AbandonRequest{Actor: "b"})
	wantCode(t, err, CodeValidationFailed)
	if _, err := s.Release(ctx, claimed.ID, ReleaseRequest{Actor: "a", LeaseToken: c.LeaseToken}); err != nil {
		t.Fatal(err)
	}
	_, err = s.Release(ctx, claimed.ID, ReleaseRequest{Actor: "a", LeaseToken: c.LeaseToken})
	wantCode(t, err, CodeInvalidTransition)
	if _, err := s.Abandon(ctx, claimed.ID, AbandonRequest{Actor: "owner", Note: "not needed"}); err != nil {
		t.Fatal(err)
	}

	_, err = s.Get(ctx, 9999)
	wantCode(t, err, CodeNotFound)
	_, err = s.Claim(ctx, 9999, ClaimRequest{Actor: "a"})
	wantCode(t, err, CodeNotFound)
	_, err = s.Claim(ctx, open.ID, ClaimRequest{Actor: "a", LeaseSeconds: 5})
	wantCode(t, err, CodeValidationFailed)
	_, err = s.Claim(ctx, open.ID, ClaimRequest{})
	wantCode(t, err, CodeValidationFailed)
}

func TestFinishRequiresTrail(t *testing.T) {
	content := t.TempDir()
	s, _, _ := newTestService(t, WithContentDir(content))
	ctx := context.Background()
	j := mustCreate(t, s, "q")
	c, _ := s.Claim(ctx, j.ID, ClaimRequest{Actor: "a"})

	_, err := s.Finish(ctx, j.ID, FinishRequest{Actor: "a", LeaseToken: c.LeaseToken})
	wantCode(t, err, CodeValidationFailed)
	_, err = s.Finish(ctx, j.ID, FinishRequest{Actor: "a", LeaseToken: c.LeaseToken, TrailSlug: "Not A Slug"})
	wantCode(t, err, CodeValidationFailed)
	_, err = s.Finish(ctx, j.ID, FinishRequest{Actor: "a", LeaseToken: c.LeaseToken, TrailSlug: "memory-run"})
	wantCode(t, err, CodeValidationFailed) // trail page not written yet

	if err := os.MkdirAll(filepath.Join(content, "trails"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(content, "trails", "memory-run.md"), []byte("---\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = s.Finish(ctx, j.ID, FinishRequest{Actor: "a", LeaseToken: c.LeaseToken, TrailSlug: "memory-run", OutputPages: []string{"sources/x.md"}})
	wantCode(t, err, CodeValidationFailed)
	got, err := s.Finish(ctx, j.ID, FinishRequest{
		Actor: "a", LeaseToken: c.LeaseToken, TrailSlug: "memory-run",
		OutputPages: []string{"wiki/agent-memory.md", "wiki/agent-memory.md"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusDone || got.TrailSlug != "memory-run" || len(got.OutputPages) != 1 || got.FinishedAt == nil {
		t.Fatalf("unexpected finished job %+v", got)
	}
}

func TestBoundedListing(t *testing.T) {
	s, _, _ := newTestService(t)
	ctx := context.Background()
	for i := range 7 {
		p := 1 + i%5
		if _, err := s.Create(ctx, CreateRequest{Question: fmt.Sprintf("q%d", i), RequestedBy: "owner", Priority: p}); err != nil {
			t.Fatal(err)
		}
	}
	var seen []int64
	offset := 0
	pages := 0
	for {
		page, err := s.List(ctx, ListRequest{Limit: 3, Offset: offset})
		if err != nil {
			t.Fatal(err)
		}
		pages++
		if len(page.Jobs) > 3 {
			t.Fatalf("page has %d jobs, limit 3", len(page.Jobs))
		}
		for _, j := range page.Jobs {
			seen = append(seen, j.ID)
		}
		if page.NextOffset == nil {
			break
		}
		offset = *page.NextOffset
	}
	if pages != 3 || len(seen) != 7 {
		t.Fatalf("got %d pages and %d jobs, want 3 and 7", pages, len(seen))
	}

	page, err := s.List(ctx, ListRequest{MaxPriority: 1})
	if err != nil || len(page.Jobs) != 2 || page.NextOffset != nil {
		t.Fatalf("priority filter: %+v %v", page, err)
	}
	c, _ := s.Claim(ctx, seen[0], ClaimRequest{Actor: "alice"})
	_ = c
	page, _ = s.List(ctx, ListRequest{Statuses: []string{StatusClaimed}, Assignee: "alice"})
	if len(page.Jobs) != 1 || page.Jobs[0].ID != seen[0] {
		t.Fatalf("status/assignee filter: %+v", page)
	}

	for _, bad := range []ListRequest{{Limit: MaxListLimit + 1}, {Limit: -1}, {Offset: -1}, {Statuses: []string{"review"}}, {MaxPriority: 9}} {
		_, err := s.List(ctx, bad)
		wantCode(t, err, CodeValidationFailed)
	}
}

func TestParentLinks(t *testing.T) {
	s, _, _ := newTestService(t)
	ctx := context.Background()
	a := mustCreate(t, s, "a")
	b, err := s.Create(ctx, CreateRequest{Question: "b", RequestedBy: "owner", ParentID: &a.ID})
	if err != nil {
		t.Fatal(err)
	}
	page, _ := s.List(ctx, ListRequest{ParentID: &a.ID})
	if len(page.Jobs) != 1 || page.Jobs[0].ID != b.ID {
		t.Fatalf("children of a: %+v", page)
	}
	_, err = s.Edit(ctx, a.ID, EditRequest{Actor: "owner", ParentID: &b.ID})
	wantCode(t, err, CodeValidationFailed)
	_, err = s.Edit(ctx, a.ID, EditRequest{Actor: "owner", ParentID: &a.ID})
	wantCode(t, err, CodeValidationFailed)
	if got, err := s.Edit(ctx, b.ID, EditRequest{Actor: "owner", ClearParent: true}); err != nil || got.ParentID != nil {
		t.Fatalf("clear parent: %+v %v", got, err)
	}
	_, err = s.Edit(ctx, b.ID, EditRequest{Actor: "owner"})
	wantCode(t, err, CodeValidationFailed)
}

// replay folds a job's events into the fields they set. If the log is
// complete, the result matches the stored job.
func replay(t *testing.T, events []Event) map[string]any {
	t.Helper()
	state := map[string]any{}
	for _, ev := range events {
		for k, v := range ev.Data {
			state[k] = v
		}
	}
	return state
}

func TestEventLogReconstructsHistory(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	parent := mustCreate(t, s, "parent")
	j, err := s.Create(ctx, CreateRequest{Question: "How do agents keep memory?", ScopeNotes: "no fine-tuning", RequestedBy: "owner", Priority: 2})
	if err != nil {
		t.Fatal(err)
	}
	step := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		clock.Advance(time.Second)
	}
	c, err := s.Claim(ctx, j.ID, ClaimRequest{Actor: "alice", Harness: "codex", LeaseSeconds: 60})
	step(err)
	_, err = s.Renew(ctx, j.ID, RenewRequest{Actor: "alice", LeaseToken: c.LeaseToken, LeaseSeconds: 120})
	step(err)
	_, err = s.Release(ctx, j.ID, ReleaseRequest{Actor: "alice", LeaseToken: c.LeaseToken, Note: "out of time"})
	step(err)
	_, err = s.Claim(ctx, j.ID, ClaimRequest{Actor: "bob", Harness: "dsh", LeaseSeconds: 60})
	step(err)
	clock.Advance(2 * time.Minute)
	_, err = s.Get(ctx, j.ID) // expires bob's lease
	step(err)
	q := "How do LLM agents keep durable memory?"
	p := 1
	_, err = s.Edit(ctx, j.ID, EditRequest{Actor: "owner", Question: &q, Priority: &p, ParentID: &parent.ID})
	step(err)
	c, err = s.Claim(ctx, j.ID, ClaimRequest{Actor: "carol", Harness: "claude-code"})
	step(err)
	_, err = s.Finish(ctx, j.ID, FinishRequest{Actor: "carol", LeaseToken: c.LeaseToken, TrailSlug: "agent-memory-run", OutputPages: []string{"wiki/agent-memory.md"}})
	step(err)

	evs, err := s.Events(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for i, ev := range evs {
		kinds = append(kinds, ev.Kind)
		if i > 0 && (ev.ID <= evs[i-1].ID || ev.At.Before(evs[i-1].At)) {
			t.Fatalf("events out of order: %+v", evs)
		}
		if ev.Actor == "" {
			t.Fatalf("event without actor: %+v", ev)
		}
	}
	wantKinds := []string{EventCreated, EventClaimed, EventRenewed, EventReleased, EventClaimed, EventExpired, EventEdited, EventClaimed, EventFinished}
	if fmt.Sprint(kinds) != fmt.Sprint(wantKinds) {
		t.Fatalf("event kinds %v, want %v", kinds, wantKinds)
	}

	assertReplayMatches(t, s, j.ID)
}

// assertReplayMatches checks that replaying a job's event data in order
// rebuilds the whole public job, timestamps included.
func assertReplayMatches(t *testing.T, s *Service, id int64) {
	t.Helper()
	ctx := context.Background()
	evs, err := s.Events(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	final, err := s.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(replay(t, evs))
	var rebuilt Job
	if err := json.Unmarshal(raw, &rebuilt); err != nil {
		t.Fatalf("decode replayed job: %v\n%s", err, raw)
	}
	got, _ := json.Marshal(rebuilt)
	want, _ := json.Marshal(final)
	if string(got) != string(want) {
		t.Fatalf("replayed job\n %s\nstored job\n %s", got, want)
	}
}

func TestEventLogReconstructsAbandonedClaim(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	j := mustCreate(t, s, "Is this worth a run?")
	clock.Advance(time.Second)
	c, err := s.Claim(ctx, j.ID, ClaimRequest{Actor: "alice", LeaseSeconds: 60})
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Second)
	got, err := s.Abandon(ctx, j.ID, AbandonRequest{Actor: "alice", LeaseToken: c.LeaseToken})
	if err != nil {
		t.Fatal(err)
	}
	if got.LeaseExpiresAt != nil || got.FinishedAt == nil {
		t.Fatalf("abandoned job: %+v", got)
	}
	assertReplayMatches(t, s, j.ID)
}

// barrierStore holds every Update until n callers have arrived, so the
// callers' pre-transaction work all happens before any of them writes.
type barrierStore struct {
	Store
	arrive *sync.WaitGroup
}

func (b barrierStore) Update(ctx context.Context, id int64, now time.Time, fn mutation) (stored, error) {
	b.arrive.Done()
	b.arrive.Wait()
	return b.Store.Update(ctx, id, now, fn)
}

func TestConcurrentReciprocalParentsCannotMakeACycle(t *testing.T) {
	s, clock, path := newTestService(t)
	ctx := context.Background()
	a := mustCreate(t, s, "a")
	b := mustCreate(t, s, "b")
	// Two services on separate stores (separate SQLite connections) over one
	// database, as two den-wiki processes would be.
	var arrive sync.WaitGroup
	arrive.Add(2)
	s1 := NewService(barrierStore{openStore(t, path), &arrive}, clock.Now)
	s2 := NewService(barrierStore{openStore(t, path), &arrive}, clock.Now)
	errs := make([]error, 2)
	var done sync.WaitGroup
	done.Add(2)
	go func() {
		defer done.Done()
		_, errs[0] = s1.Edit(ctx, a.ID, EditRequest{Actor: "alpha", ParentID: &b.ID})
	}()
	go func() {
		defer done.Done()
		_, errs[1] = s2.Edit(ctx, b.ID, EditRequest{Actor: "beta", ParentID: &a.ID})
	}()
	done.Wait()
	if (errs[0] == nil) == (errs[1] == nil) {
		t.Fatalf("want exactly one edit rejected, got %v and %v", errs[0], errs[1])
	}
	for _, err := range errs {
		if err != nil {
			wantCode(t, err, CodeValidationFailed)
		}
	}
	ga, _ := s.Get(ctx, a.ID)
	gb, _ := s.Get(ctx, b.ID)
	if ga.ParentID != nil && gb.ParentID != nil {
		t.Fatalf("cycle committed: a.parent=%d b.parent=%d", *ga.ParentID, *gb.ParentID)
	}
}

func TestEventsAreAppendOnly(t *testing.T) {
	s, _, _ := newTestService(t)
	j := mustCreate(t, s, "q")
	st := s.store.(*SQLiteStore)
	if _, err := st.db.Exec(`UPDATE job_events SET kind = 'x' WHERE job_id = ?`, j.ID); err == nil {
		t.Fatal("event update allowed")
	}
	if _, err := st.db.Exec(`DELETE FROM job_events WHERE job_id = ?`, j.ID); err == nil {
		t.Fatal("event delete allowed")
	}
}

func TestReopenedStoreKeepsJobs(t *testing.T) {
	s, clock, path := newTestService(t)
	j := mustCreate(t, s, "q")
	s2 := NewService(openStore(t, path), clock.Now)
	got, err := s2.Get(context.Background(), j.ID)
	if err != nil || got.Question != "q" {
		t.Fatalf("reopened: %+v %v", got, err)
	}
}
