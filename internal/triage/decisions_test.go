package triage

import (
	"strings"
	"testing"

	"github.com/mateusveloso/graph-issue-triage/internal/jev"
)

// The decision model's answers steer the graph through code, never through a prompt.

func TestConfidentNoCodeSkipsPlannerAndCollect(t *testing.T) {
	t.Parallel()
	decider := decide(
		map[string]jev.Answer{"category": choice("question", 0.92), "needs_code": noul(0.05)},
		rubricAnswers(0.95, 0.95, 0.95),
	)
	noEvidence := goodDraft
	noEvidence.EvidenceUsed = nil
	model := script(noEvidence)

	_, cp := run(t, model, decider, testConfig(t))

	if model.asked(Plan{}) != 0 || model.asked(Review{}) != 0 {
		t.Fatalf("want only the writer to run, got %+v", model.calls)
	}
	prompt := model.prompts(Triage{})[0]
	for _, want := range []string{"(no code read)", "category: question (confidence 0.92)"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("writer prompt missing %q", want)
		}
	}
	if gatePayload(t, cp).Prior.Category != "question" {
		t.Fatal("prior not surfaced at the gate")
	}
}

func TestRubricPassSkipsTheGenerativeCritic(t *testing.T) {
	t.Parallel()
	decider := decide(map[string]jev.Answer{"needs_code": noul(0.9)}, rubricAnswers(0.90, 0.95, 0.88))
	model := script(testPlan, goodDraft)

	_, cp := run(t, model, decider, testConfig(t))

	if model.asked(Review{}) != 0 {
		t.Fatal("critic should not run on a clear pass")
	}
	payload := gatePayload(t, cp)
	if payload.LastReview.Source != "rubric" || !payload.LastReview.Approved {
		t.Fatalf("want rubric approval, got %+v", payload.LastReview)
	}
	if payload.Rubric["actionable"] != 0.88 {
		t.Fatalf("rubric probabilities not surfaced: %v", payload.Rubric)
	}
}

func TestRubricFailRejectsWithTemplatedFeedback(t *testing.T) {
	t.Parallel()
	decider := decide(
		map[string]jev.Answer{"needs_code": noul(0.9)},
		rubricAnswers(0.95, 0.95, 0.20),
		rubricAnswers(0.95, 0.95, 0.95),
	)
	model := script(testPlan, goodDraft, goodDraft)

	run(t, model, decider, testConfig(t))

	prompts := model.prompts(Triage{})
	if len(prompts) != 2 {
		t.Fatalf("want 2 writer calls, got %d", len(prompts))
	}
	if !strings.Contains(prompts[1], "[rubric] rules not met: actionable") {
		t.Fatalf("second draft did not see the rubric feedback:\n%s", prompts[1])
	}
	if model.asked(Review{}) != 0 {
		t.Fatal("no generative critic should have run")
	}
}

func TestUncertainRubricPaysForTheGenerativeCritic(t *testing.T) {
	t.Parallel()
	decider := decide(map[string]jev.Answer{"needs_code": noul(0.9)}, rubricAnswers(0.70, 0.95, 0.95))
	model := script(testPlan, goodDraft, approve)

	_, cp := run(t, model, decider, testConfig(t))

	prompts := model.prompts(Review{})
	if len(prompts) != 1 || !strings.Contains(prompts[0], "grounded=0.70") {
		t.Fatalf("critic should run once and see the rubric, got %v", prompts)
	}
	if gatePayload(t, cp).LastReview.Source != "critic" {
		t.Fatal("want the critic's review at the gate")
	}
}

func TestWithoutADeciderTheGraphTakesTheFullPath(t *testing.T) {
	t.Parallel()
	model := script(testPlan, goodDraft, approve)

	_, cp := run(t, model, decide(), testConfig(t))

	if model.asked(Plan{}) != 1 || model.asked(Triage{}) != 1 || model.asked(Review{}) != 1 {
		t.Fatalf("want planner, writer and critic once each, got %+v", model.calls)
	}
	if p := gatePayload(t, cp).Prior; p != (Prior{}) {
		t.Fatalf("want an empty prior, got %+v", p)
	}
}

func TestMissingRubricAnswerCountsAsUncertain(t *testing.T) {
	t.Parallel()
	partial := map[string]jev.Answer{"grounded": noul(0.95)} // two rules unanswered
	decider := decide(nil, partial)
	model := script(testPlan, goodDraft, approve)

	_, cp := run(t, model, decider, testConfig(t))

	rubric := gatePayload(t, cp).Rubric
	if rubric["category_matches"] != 0.5 || rubric["actionable"] != 0.5 {
		t.Fatalf("missing answers must read as 0.5, got %v", rubric)
	}
	if model.asked(Review{}) != 1 {
		t.Fatal("uncertain verdict must reach the critic")
	}
}
