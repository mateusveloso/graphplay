package triage

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/mateusveloso/graph-issue-triage/graph"
	"github.com/mateusveloso/graph-issue-triage/internal/llm"
)

func TestHappyPathPausesAtGateThenWritesReport(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	r, cp := run(t, script(testPlan, goodDraft, approve), decide(), cfg)

	payload := gatePayload(t, cp)
	if payload.Draft.Category != "bug" || !payload.LastReview.Approved {
		t.Fatalf("unexpected payload %+v", payload)
	}

	cp, err := r.Resume(context.Background(), t.Name(), Decision{Approve: true})
	if err != nil {
		t.Fatal(err)
	}
	if !cp.Done {
		t.Fatal("want the run to finish")
	}
	report, err := os.ReadFile(cp.State.OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"octo/demo#7", "[critic] approved", "accept argv in main()"} {
		if !strings.Contains(string(report), want) {
			t.Errorf("report missing %q:\n%s", want, report)
		}
	}
}

func TestCriticRejectionFeedsTheNextDraft(t *testing.T) {
	t.Parallel()
	weak := goodDraft
	weak.Summary, weak.Confidence = "something is broken", "low"
	model := script(testPlan, weak, rejectWith("summary does not say what breaks"), goodDraft, approve)

	_, cp := run(t, model, decide(), testConfig(t))

	prompts := model.prompts(Triage{})
	if len(prompts) != 2 {
		t.Fatalf("want 2 writer calls, got %d", len(prompts))
	}
	if !strings.Contains(prompts[1], "[critic] summary does not say what breaks") {
		t.Fatalf("second draft did not see the rejection:\n%s", prompts[1])
	}
	if got := gatePayload(t, cp).CriticRounds; got != 2 {
		t.Fatalf("want 2 rounds, got %d", got)
	}
}

func TestCriticBudgetIsEnforcedInCode(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	model := script(testPlan, goodDraft, rejectWith("no"), goodDraft, rejectWith("still no"))

	_, cp := run(t, model, decide(), cfg)

	payload := gatePayload(t, cp)
	if payload.CriticRounds != cfg.MaxCriticRounds || payload.LastReview.Approved {
		t.Fatalf("want gate after %d rejected rounds, got %+v", cfg.MaxCriticRounds, payload)
	}
	if got := model.asked(Triage{}); got != cfg.MaxCriticRounds {
		t.Fatalf("writer ran %d times, want %d", got, cfg.MaxCriticRounds)
	}
}

func TestValidatorRejectsInventedEvidenceWithoutAnyModel(t *testing.T) {
	t.Parallel()
	invented := goodDraft
	invented.EvidenceUsed = []string{"src/does_not_exist.py"}
	decider := decide()
	model := script(testPlan, invented, goodDraft, approve)

	_, cp := run(t, model, decider, testConfig(t))

	sources := make([]string, 0, len(cp.State.Reviews))
	for _, r := range cp.State.Reviews {
		sources = append(sources, r.Source)
	}
	if strings.Join(sources, ",") != "validator,critic" {
		t.Fatalf("want validator then critic, got %v", sources)
	}
	// classify + one check for the valid draft; the invented draft never reached Jev.
	if len(decider.calls) != 2 {
		t.Fatalf("decider called %d times, want 2", len(decider.calls))
	}
}

func TestHumanRejectionLoopsBackAndResetsBudget(t *testing.T) {
	t.Parallel()
	model := script(testPlan, goodDraft, approve, goodDraft, approve)
	r, _ := run(t, model, decide(), testConfig(t))

	cp, err := r.Resume(context.Background(), t.Name(), Decision{Feedback: "mention the version"})
	if err != nil {
		t.Fatal(err)
	}
	payload := gatePayload(t, cp)
	if payload.CriticRounds != 1 {
		t.Fatalf("budget not reset: rounds=%d", payload.CriticRounds)
	}
	if !strings.Contains(model.prompts(Triage{})[1], "[human] mention the version") {
		t.Fatal("second draft did not see the human feedback")
	}
}

func TestResumeFromAnotherRunnerOverTheSameStore(t *testing.T) {
	t.Parallel()
	// What the CLI does: one process pauses, another one resumes from disk.
	cfg := testConfig(t)
	store := graph.FileStore[State]{Dir: cfg.StateDir}
	decider := decide()
	first, err := graph.NewRunner(Build(llm.Single(script(testPlan, goodDraft, approve)), newRepo(), decider, cfg), store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Start(context.Background(), "disk", State{IssueRef: "octo/demo#7"}); err != nil {
		t.Fatal(err)
	}

	second, _ := graph.NewRunner(Build(llm.Single(script()), newRepo(), decider, cfg), store)
	cp, err := second.Resume(context.Background(), "disk", Decision{Approve: true})
	if err != nil {
		t.Fatal(err)
	}
	if !cp.Done || cp.State.OutputPath == "" {
		t.Fatalf("want a finished run with a report, got %+v", cp)
	}
}
