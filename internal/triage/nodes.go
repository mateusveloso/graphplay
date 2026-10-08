package triage

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/mateusveloso/graph-issue-triage/graph"
	"github.com/mateusveloso/graph-issue-triage/internal/github"
	"github.com/mateusveloso/graph-issue-triage/internal/jev"
	"github.com/mateusveloso/graph-issue-triage/internal/llm"
)

// Nodes are constructors: they take their dependencies once and return the function the
// graph calls. Deterministic nodes never see a model; model nodes never see GitHub.

// loadIssue is the deterministic entry: the issue and the repo's top-level listing.
func loadIssue(repo github.Repository) graph.Node[State] {
	return func(ctx context.Context, s *State) error {
		owner, name, number, err := github.ParseRef(s.IssueRef)
		if err != nil {
			return err
		}
		issue, err := repo.Issue(ctx, owner, name, number)
		if err != nil {
			return err
		}
		listing, err := repo.Root(ctx, owner, name)
		if err != nil {
			return err
		}
		s.Issue = &issue
		s.RootListing = listing
		return nil
	}
}

// classify asks the decision model, not a generative one: a category with confidence and
// one yes/no. Without a decision the prior stays empty and the graph takes the full path.
func classify(decider jev.Decider) graph.Node[State] {
	return func(ctx context.Context, s *State) error {
		text := fmt.Sprintf("# %s\nlabels: %s\n\n%s", s.Issue.Title, labels(s.Issue.Labels), s.Issue.Body)
		answers, err := decider.Decide(ctx, text, classifyQuestions)
		if err != nil {
			s.Prior = Prior{}
			return nil //nolint:nilerr // no decision is a valid outcome; the route handles it
		}
		s.Prior = Prior{
			Category:   answers["category"].Choice,
			Confidence: answers["category"].Confidence,
			NeedsCode:  answers["needs_code"].Noul,
		}
		return nil
	}
}

// plan is bounded output from a small model: hypotheses and what to read to test them.
func plan(model llm.Model) graph.Node[State] {
	return func(ctx context.Context, s *State) error {
		user, err := plannerPrompt.render("user", s)
		if err != nil {
			return err
		}
		var p Plan
		if err := model.Ask(ctx, plannerPrompt.system(), user, &p); err != nil {
			return err
		}
		s.Plan = &p
		return nil
	}
}

// collect executes the plan. The request cap is enforced here, not in a prompt. Fetches
// run concurrently with a bound; results keep the plan's order. A failed fetch becomes
// evidence instead of an error: the writer should know what is missing.
func collect(repo github.Repository, cfg Config) graph.Node[State] {
	return func(ctx context.Context, s *State) error {
		requests := s.Plan.Requests[:min(len(s.Plan.Requests), cfg.MaxEvidenceRequests)]
		s.Evidence = make([]Evidence, len(requests))

		g, ctx := errgroup.WithContext(ctx)
		g.SetLimit(cfg.CollectConcurrency)
		for i, r := range requests {
			g.Go(func() error {
				s.Evidence[i] = fetch(ctx, repo, s.Issue, r, cfg.MaxFileChars)
				return nil
			})
		}
		return g.Wait()
	}
}

func fetch(ctx context.Context, repo github.Repository, issue *github.Issue, r EvidenceRequest, maxChars int) Evidence {
	ev := Evidence{Kind: r.Kind, Target: r.Target}
	switch r.Kind {
	case "read_file":
		content, err := repo.File(ctx, issue.Owner, issue.Repo, r.Target)
		if err != nil {
			ev.Error = err.Error()
			return ev
		}
		ev.Content = content[:min(len(content), maxChars)]
	case "search_code":
		hits, err := repo.Search(ctx, issue.Owner, issue.Repo, r.Target)
		switch {
		case err != nil:
			ev.Error = err.Error()
		case len(hits) == 0:
			ev.Content = "(no matches)"
		default:
			ev.Content = strings.Join(hits, "\n")
		}
	default:
		ev.Error = "unknown request kind " + r.Kind
	}
	return ev
}

// write produces the draft from issue + prior + evidence + every rejection so far.
func write(model llm.Model) graph.Node[State] {
	return func(ctx context.Context, s *State) error {
		user, err := writerPrompt.render("user", s)
		if err != nil {
			return err
		}
		var draft Triage
		if err := model.Ask(ctx, writerPrompt.system(), user, &draft); err != nil {
			return err
		}
		s.Draft = &draft
		return nil
	}
}

// check is everything cheaper than a generative critic, in cost order:
//  1. code: did the draft cite evidence that was never collected?
//  2. decision model: one probability per rubric rule.
//
// The verdict is computed here, in code, from the thresholds in Config.
func check(decider jev.Decider, cfg Config) graph.Node[State] {
	return func(ctx context.Context, s *State) error {
		s.CriticRounds++
		s.Rubric = nil

		if unknown := s.uncollected(); len(unknown) > 0 {
			s.reject("validator", fmt.Sprintf("evidence_used references targets that were never collected: %v", unknown))
			return nil
		}

		state, err := checkPrompt.render("state", s)
		if err != nil {
			return err
		}
		answers, err := decider.Decide(ctx, state, rubricQuestions())
		if err != nil {
			s.Verdict = VerdictUncertain
			return nil //nolint:nilerr // no decision: the generative critic decides
		}

		s.Rubric = make(map[string]float64, len(rubric))
		var failed []rule
		allPass := true
		for _, r := range rubric {
			p := 0.5 // a missing answer is maximum uncertainty, not a pass
			if a, ok := answers[r.name]; ok && a.Noul != nil {
				p = *a.Noul
			}
			s.Rubric[r.name] = p
			if p < cfg.RubricFail {
				failed = append(failed, r)
			}
			if p < cfg.RubricPass {
				allPass = false
			}
		}
		switch {
		case len(failed) > 0:
			s.reject("rubric", "rules not met: "+describe(failed, s.Rubric))
		case allPass:
			s.Reviews = append(s.Reviews, Review{Source: "rubric", Approved: true, Feedback: "all rules met: " + renderRubric(s.Rubric)})
			s.Verdict = VerdictPass
		default:
			s.Verdict = VerdictUncertain
		}
		return nil
	}
}

// critic is generative judgment, paid only for drafts the cheaper checks could not settle.
func critic(model llm.Model) graph.Node[State] {
	return func(ctx context.Context, s *State) error {
		user, err := criticPrompt.render("user", s)
		if err != nil {
			return err
		}
		var review Review
		if err := model.Ask(ctx, criticPrompt.system(), user, &review); err != nil {
			return err
		}
		review.Source = "critic"
		s.Reviews = append(s.Reviews, review)
		return nil
	}
}

// Decision is what a human answers at the gate.
type Decision struct {
	Approve  bool
	Feedback string
}

// GatePayload is what the paused run shows to the human.
type GatePayload struct {
	IssueRef     string             `json:"issue_ref"`
	Draft        Triage             `json:"draft"`
	CriticRounds int                `json:"critic_rounds"`
	Prior        Prior              `json:"prior"`
	Rubric       map[string]float64 `json:"rubric,omitzero"`
	LastReview   *Review            `json:"last_review,omitzero"`
}

// gate is the human checkpoint. The run stops here and is resumed with a Decision.
func gate(ctx context.Context, s *State) error {
	payload := GatePayload{IssueRef: s.IssueRef, Draft: *s.Draft, CriticRounds: s.CriticRounds, Prior: s.Prior, Rubric: s.Rubric}
	if n := len(s.Reviews); n > 0 {
		payload.LastReview = &s.Reviews[n-1]
	}
	decision, err := graph.Interrupt[Decision](ctx, payload)
	if err != nil {
		return err
	}
	if decision.Approve {
		s.Approved = true
		return nil
	}
	s.Approved = false
	s.reject("human", cmp.Or(decision.Feedback, "rejected without feedback"))
	s.CriticRounds = 0 // a human rejection restarts the budget for the new draft
	return nil
}

// finalize renders the approved triage with its review trail next to the checkpoint.
func finalize(cfg Config) graph.Node[State] {
	return func(ctx context.Context, s *State) error {
		report, err := reportPrompt.render("report", s)
		if err != nil {
			return err
		}
		dir := filepath.Join(cfg.StateDir, "runs", graph.Thread(ctx))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		path := filepath.Join(dir, "triage.md")
		if err := os.WriteFile(path, []byte(report), 0o644); err != nil {
			return err
		}
		s.OutputPath = path
		return nil
	}
}
