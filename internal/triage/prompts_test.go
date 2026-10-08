package triage

import (
	"strings"
	"testing"

	"github.com/mateusveloso/graph-issue-triage/internal/github"
)

// Every prompt file defines its own "user" block. Parsing them with a shared file must
// not let one file's block shadow another's (it did once).
func TestPromptsRenderDistinctUserBlocks(t *testing.T) {
	t.Parallel()
	s := &State{
		IssueRef:    "o/r#1",
		Issue:       &github.Issue{Owner: "o", Repo: "r", Number: 1, Title: "T", Body: "B"},
		RootListing: []string{"README.md"},
		Plan:        &Plan{Hypotheses: []string{"h1"}},
		Evidence:    []Evidence{{Kind: "read_file", Target: "a.go", Content: "x"}},
		Draft:       &Triage{Category: "bug", Summary: "S", NextSteps: []string{"n1"}},
		Rubric:      map[string]float64{"grounded": 0.7},
		Reviews:     []Review{{Source: "critic", Feedback: "fix it"}},
	}
	tests := []struct {
		name   string
		prompt prompt
		block  string
		want   string
	}{
		{"planner", plannerPrompt, "user", "Top-level listing:\nREADME.md"},
		{"writer", writerPrompt, "user", "## Feedback on previous drafts"},
		{"critic", criticPrompt, "user", "grounded=0.70"},
		{"check", checkPrompt, "state", "# Triage\ncategory: bug"},
		{"report", reportPrompt, "report", "[critic] rejected: fix it"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			out, err := tt.prompt.render(tt.block, s)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, tt.want) {
				t.Fatalf("%s/%s missing %q:\n%s", tt.name, tt.block, tt.want, out)
			}
		})
	}
	if !strings.Contains(plannerPrompt.system(), "You plan") || !strings.Contains(criticPrompt.system(), "You review") {
		t.Fatal("system blocks swapped between files")
	}
}
