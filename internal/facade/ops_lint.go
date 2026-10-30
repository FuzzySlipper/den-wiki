package facade

import (
	"context"
	"fmt"

	"github.com/FuzzySlipper/den-wiki/internal/lint"
	"github.com/FuzzySlipper/den-wiki/internal/registry"
)

type lintArgs struct {
	Commits     string `json:"commits,omitempty" desc:"Also check the commits in this git range of the content repo, e.g. origin/main..HEAD."`
	MinSeverity string `json:"min_severity,omitempty" enum:"info,warning,error" desc:"Only return findings at or above this severity; default info."`
}

type lintRulesArgs struct{}

type taxonomyArgs struct{}

func lintOps(contentDir string) []*registry.Operation {
	return []*registry.Operation{
		registry.New(registry.Operation{
			Name: "wiki_lint", Risk: registry.RiskRead, Backend: "lint",
			Description: "Check the content repository against the den-wiki content contract. Findings are reports, not blocks; each has a stable code (see wiki_lint_rules).",
		}, func(ctx context.Context, _ *registry.Call, a lintArgs) (any, error) {
			if contentDir == "" {
				return nil, &registry.Error{Code: registry.CodeBackendUnavailable, Message: "the facade has no content_dir configured, so lint cannot run"}
			}
			min := lint.Info
			if a.MinSeverity != "" {
				s, err := lint.ParseSeverity(a.MinSeverity)
				if err != nil {
					return nil, &registry.Error{Code: registry.CodeInvalidArguments, Message: err.Error()}
				}
				min = s
			}
			fs, err := lint.Lint(contentDir, lint.Options{Commits: a.Commits})
			if err != nil {
				return nil, &registry.Error{Code: registry.CodeBackendError, Message: fmt.Sprintf("lint: %v", err)}
			}
			kept := []lint.Finding{}
			for _, f := range fs {
				if f.Severity >= min {
					kept = append(kept, f)
				}
			}
			return map[string]any{"counts": lint.Counts(fs), "findings": kept}, nil
		}),
		registry.New(registry.Operation{
			Name: "wiki_taxonomy", Risk: registry.RiskRead, Tier: registry.TierDefault, Backend: "lint",
			Description: "The page types and tags taxonomy.yml allows, each with its description, parent tag and the pages using it; plus types and tags pages use that it does not list, and the taxonomy lint findings. Use a listed tag, or propose a new one in your trail.",
		}, func(context.Context, *registry.Call, taxonomyArgs) (any, error) {
			if contentDir == "" {
				return nil, &registry.Error{Code: registry.CodeBackendUnavailable, Message: "the facade has no content_dir configured, so the taxonomy cannot be read"}
			}
			r, err := lint.ReadTaxonomy(contentDir)
			if err != nil {
				return nil, &registry.Error{Code: registry.CodeBackendError, Message: fmt.Sprintf("taxonomy: %v", err)}
			}
			return r, nil
		}),
		registry.New(registry.Operation{
			Name: "wiki_lint_rules", Risk: registry.RiskRead, Backend: "lint",
			Description: "Every lint finding code with its severity and meaning.",
		}, func(context.Context, *registry.Call, lintRulesArgs) (any, error) {
			return map[string]any{"rules": lint.Rules()}, nil
		}),
	}
}
