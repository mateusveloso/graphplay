package triage

import (
	"fmt"
	"slices"
	"strings"

	"github.com/mateusveloso/graph-issue-triage/internal/jev"
)

// rule is one yes/no the decision model answers about a draft. A rubric that weighs
// several things in one statement is a prompt, not a rubric.
type rule struct {
	name      string
	statement string
}

// rubric is ordered: feedback and diagrams list rules in a stable order.
var rubric = []rule{
	{"grounded", "Every claim in the summary is supported by the evidence listed."},
	{"category_matches", "The category is consistent with what the summary describes."},
	{"actionable", "A maintainer could act on the next steps today without asking anything."},
}

func rubricQuestions() map[string]jev.Question {
	qs := make(map[string]jev.Question, len(rubric))
	for _, r := range rubric {
		qs[r.name] = jev.Noul(r.statement, "Yes.", "No.")
	}
	return qs
}

var categories = map[string]string{
	"bug":        "Something that used to work or is documented to work does not.",
	"feature":    "A request for behaviour the project does not have.",
	"question":   "The author asks how to do something; nothing is broken.",
	"docs":       "The documentation is wrong, missing or unclear.",
	"needs_info": "Not enough detail to tell what the author wants or what happens.",
}

var classifyQuestions = map[string]jev.Question{
	"category": jev.Choice("Which kind of issue is this?", categories),
	"needs_code": jev.Noul(
		"Does answering this issue require reading the project's source code?",
		"A maintainer would have to open source files to answer.",
		"The issue can be answered from its text and the documentation alone.",
	),
}

func describe(failed []rule, probs map[string]float64) string {
	parts := make([]string, len(failed))
	for i, r := range failed {
		parts[i] = fmt.Sprintf("%s: %s (p=%.2f)", r.name, r.statement, probs[r.name])
	}
	return strings.Join(parts, "; ")
}

// renderRubric prints probabilities in rule order so output is stable. Keys outside the
// known rubric go last, sorted, so nothing is silently dropped.
func renderRubric(probs map[string]float64) string {
	known := make(map[string]bool, len(rubric))
	parts := make([]string, 0, len(probs))
	for _, r := range rubric {
		known[r.name] = true
		if p, ok := probs[r.name]; ok {
			parts = append(parts, fmt.Sprintf("%s=%.2f", r.name, p))
		}
	}
	var extra []string
	for k := range probs {
		if !known[k] {
			extra = append(extra, k)
		}
	}
	slices.Sort(extra)
	for _, k := range extra {
		parts = append(parts, fmt.Sprintf("%s=%.2f", k, probs[k]))
	}
	return strings.Join(parts, ", ")
}
