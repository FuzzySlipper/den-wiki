package facade

import (
	"context"

	"github.com/FuzzySlipper/den-wiki/internal/ledger"
	"github.com/FuzzySlipper/den-wiki/internal/registry"
)

// The calling agent is the ledger actor, so no ledger tool takes an actor
// argument: callers never repeat what the transport already says.

type jobCreateArgs struct {
	Kind       string `json:"kind,omitempty" enum:"research,round-synthesis,calibration,curation" desc:"Kind of run; default research. Its trail's type repeats it (type trail for research)."`
	Question   string `json:"question" required:"true" desc:"What to research."`
	ScopeNotes string `json:"scope_notes,omitempty" desc:"What is in and out of scope."`
	ParentID   *int64 `json:"parent_id,omitempty" desc:"Parent job id."`
	Priority   int    `json:"priority,omitempty" min:"1" max:"5" desc:"1 (highest) to 5; default 3."`
}

type jobIDArgs struct {
	ID int64 `json:"id" required:"true" desc:"Job id."`
}

type jobListArgs struct {
	Statuses    []string `json:"statuses,omitempty" desc:"Any of open, claimed, done, abandoned."`
	Kinds       []string `json:"kinds,omitempty" desc:"Any of research, round-synthesis, calibration, curation."`
	Assignee    string   `json:"assignee,omitempty" desc:"Only jobs claimed by this agent."`
	RequestedBy string   `json:"requested_by,omitempty" desc:"Only jobs requested by this agent."`
	ParentID    *int64   `json:"parent_id,omitempty" desc:"Only children of this job."`
	MaxPriority int      `json:"max_priority,omitempty" min:"1" max:"5" desc:"Only jobs at this priority or higher (lower number)."`
	Limit       int      `json:"limit,omitempty" min:"1" max:"200" desc:"Page size; default 50."`
	Offset      int      `json:"offset,omitempty" min:"0" desc:"Start offset; pass the previous page's next_offset."`
}

type jobClaimArgs struct {
	ID           int64  `json:"id" required:"true" desc:"Job id."`
	Harness      string `json:"harness,omitempty" desc:"Harness you run in, e.g. claude-code, codex, dsh, hermes."`
	LeaseSeconds int    `json:"lease_seconds,omitempty" min:"60" max:"86400" desc:"Lease length; default 1800. Renew before it ends."`
	Note         string `json:"note,omitempty"`
}

type jobRenewArgs struct {
	ID           int64  `json:"id" required:"true" desc:"Job id."`
	LeaseToken   string `json:"lease_token" required:"true" desc:"Token returned by job_claim."`
	LeaseSeconds int    `json:"lease_seconds,omitempty" min:"60" max:"86400" desc:"New lease length from now; default 1800."`
	Note         string `json:"note,omitempty"`
}

type jobReleaseArgs struct {
	ID         int64  `json:"id" required:"true" desc:"Job id."`
	LeaseToken string `json:"lease_token" required:"true" desc:"Token returned by job_claim."`
	Note       string `json:"note,omitempty" desc:"Why you are giving the job back."`
}

type jobFinishArgs struct {
	ID          int64    `json:"id" required:"true" desc:"Job id."`
	LeaseToken  string   `json:"lease_token" required:"true" desc:"Token returned by job_claim."`
	TrailSlug   string   `json:"trail_slug" required:"true" desc:"Slug of the run's trail page, trails/<slug>.md; write it first."`
	OutputPages []string `json:"output_pages,omitempty" desc:"wiki/*.md paths created or edited."`
	Note        string   `json:"note,omitempty"`
}

type jobAbandonArgs struct {
	ID         int64  `json:"id" required:"true" desc:"Job id."`
	LeaseToken string `json:"lease_token,omitempty" desc:"Required while the job is claimed."`
	Note       string `json:"note,omitempty" desc:"Why the job is abandoned."`
}

type jobEditArgs struct {
	ID          int64   `json:"id" required:"true" desc:"Job id."`
	Kind        *string `json:"kind,omitempty" enum:"research,round-synthesis,calibration,curation"`
	Question    *string `json:"question,omitempty"`
	ScopeNotes  *string `json:"scope_notes,omitempty"`
	Priority    *int    `json:"priority,omitempty" min:"1" max:"5"`
	ParentID    *int64  `json:"parent_id,omitempty"`
	ClearParent bool    `json:"clear_parent,omitempty" desc:"Remove the parent link."`
	Note        string  `json:"note,omitempty"`
}

func ledgerOps(svc *ledger.Service) []*registry.Operation {
	const backend = "ledger"
	return []*registry.Operation{
		registry.New(registry.Operation{
			Name: "job_create", Risk: registry.RiskWrite, Tier: registry.TierDefault, Backend: backend,
			Description: "Queue a research job. You become its requester. Returns the job; claim it with job_claim to work on it.",
			Examples:    []registry.Example{{Description: "Queue a question", Arguments: map[string]any{"question": "How do LLM agents keep durable memory?", "priority": 2}}},
		}, func(ctx context.Context, c *registry.Call, a jobCreateArgs) (any, error) {
			return svc.Create(ctx, ledger.CreateRequest{Kind: a.Kind, Question: a.Question, ScopeNotes: a.ScopeNotes, RequestedBy: c.Agent, ParentID: a.ParentID, Priority: a.Priority})
		}),
		registry.New(registry.Operation{
			Name: "job_get", Risk: registry.RiskRead, Backend: backend,
			Description: "Get one research job.",
		}, func(ctx context.Context, _ *registry.Call, a jobIDArgs) (any, error) {
			return svc.Get(ctx, a.ID)
		}),
		registry.New(registry.Operation{
			Name: "job_list", Risk: registry.RiskRead, Tier: registry.TierDefault, Backend: backend,
			Description: "List research jobs, highest priority first, one bounded page at a time. next_offset is null on the last page.",
			Examples:    []registry.Example{{Description: "Open jobs to pick from", Arguments: map[string]any{"statuses": []string{"open"}, "limit": 10}}},
		}, func(ctx context.Context, _ *registry.Call, a jobListArgs) (any, error) {
			return svc.List(ctx, ledger.ListRequest{
				Statuses: a.Statuses, Kinds: a.Kinds, Assignee: a.Assignee, RequestedBy: a.RequestedBy, ParentID: a.ParentID,
				MaxPriority: a.MaxPriority, Limit: a.Limit, Offset: a.Offset,
			})
		}),
		registry.New(registry.Operation{
			Name: "job_claim", Risk: registry.RiskWrite, Tier: registry.TierDefault, Backend: backend,
			Description: "Claim an open research job. Exactly one concurrent claimer wins; others get claim_conflict. Keep the returned lease_token: renew, release and finish need it.",
		}, func(ctx context.Context, c *registry.Call, a jobClaimArgs) (any, error) {
			return svc.Claim(ctx, a.ID, ledger.ClaimRequest{Actor: c.Agent, Harness: a.Harness, LeaseSeconds: a.LeaseSeconds, Note: a.Note})
		}),
		registry.New(registry.Operation{
			Name: "job_renew", Risk: registry.RiskWrite, Backend: backend,
			Description: "Extend your lease on a claimed job. A lapsed lease gives lease_expired; claim the job again.",
		}, func(ctx context.Context, c *registry.Call, a jobRenewArgs) (any, error) {
			return svc.Renew(ctx, a.ID, ledger.RenewRequest{Actor: c.Agent, LeaseToken: a.LeaseToken, LeaseSeconds: a.LeaseSeconds, Note: a.Note})
		}),
		registry.New(registry.Operation{
			Name: "job_release", Risk: registry.RiskWrite, Backend: backend,
			Description: "Give a claimed job back to the queue unfinished.",
		}, func(ctx context.Context, c *registry.Call, a jobReleaseArgs) (any, error) {
			return svc.Release(ctx, a.ID, ledger.ReleaseRequest{Actor: c.Agent, LeaseToken: a.LeaseToken, Note: a.Note})
		}),
		registry.New(registry.Operation{
			Name: "job_finish", Risk: registry.RiskWrite, Tier: registry.TierDefault, Backend: backend,
			Description: "Finish your claimed job. trail_slug must name the run's trail page (trails/<slug>.md), which must already exist.",
		}, func(ctx context.Context, c *registry.Call, a jobFinishArgs) (any, error) {
			return svc.Finish(ctx, a.ID, ledger.FinishRequest{Actor: c.Agent, LeaseToken: a.LeaseToken, TrailSlug: a.TrailSlug, OutputPages: a.OutputPages, Note: a.Note})
		}),
		registry.New(registry.Operation{
			Name: "job_abandon", Risk: registry.RiskDestructive, Backend: backend,
			Description: "Close a job without a result. While claimed, only the lease holder can abandon it.",
		}, func(ctx context.Context, c *registry.Call, a jobAbandonArgs) (any, error) {
			return svc.Abandon(ctx, a.ID, ledger.AbandonRequest{Actor: c.Agent, LeaseToken: a.LeaseToken, Note: a.Note})
		}),
		registry.New(registry.Operation{
			Name: "job_edit", Risk: registry.RiskWrite, Backend: backend,
			Description: "Change a job's kind, question, scope notes, priority or parent while it is open or claimed.",
		}, func(ctx context.Context, c *registry.Call, a jobEditArgs) (any, error) {
			return svc.Edit(ctx, a.ID, ledger.EditRequest{Actor: c.Agent, Kind: a.Kind, Question: a.Question, ScopeNotes: a.ScopeNotes, Priority: a.Priority, ParentID: a.ParentID, ClearParent: a.ClearParent, Note: a.Note})
		}),
		registry.New(registry.Operation{
			Name: "job_events", Risk: registry.RiskRead, Backend: backend,
			Description: "A job's append-only event log, oldest first.",
		}, func(ctx context.Context, _ *registry.Call, a jobIDArgs) (any, error) {
			evs, err := svc.Events(ctx, a.ID)
			return map[string]any{"events": evs}, err
		}),
	}
}
