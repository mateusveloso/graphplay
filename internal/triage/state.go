// Package triage is the domain: the state, the nodes and the graph that triages an issue.
package triage

import "github.com/mateusveloso/graph-issue-triage/internal/github"

// Prior is what the decision model thinks before any evidence is read.
type Prior struct {
	Category   string   `json:"category,omitempty"`
	Confidence float64  `json:"confidence,omitempty"`
	NeedsCode  *float64 `json:"needs_code,omitempty"` // P(answering needs the code)
}

// EvidenceRequest is one thing the planner wants to look at.
type EvidenceRequest struct {
	Kind   string `json:"kind" jsonschema:"enum=read_file,enum=search_code"`
	Target string `json:"target" jsonschema_description:"A file path for read_file, a search query for search_code."`
	Reason string `json:"reason"`
}

// Plan is the planner's typed output.
type Plan struct {
	Hypotheses []string          `json:"hypotheses" jsonschema:"maxItems=3"`
	Requests   []EvidenceRequest `json:"requests"`
}

// Evidence is what collect produced for one request. A failure is evidence too.
type Evidence struct {
	Kind    string `json:"kind"`
	Target  string `json:"target"`
	Content string `json:"content,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Triage is the writer's typed output.
type Triage struct {
	Category     string   `json:"category" jsonschema:"enum=bug,enum=feature,enum=question,enum=docs,enum=needs_info"`
	Summary      string   `json:"summary"`
	EvidenceUsed []string `json:"evidence_used" jsonschema_description:"Targets from the evidence list that support this, verbatim."`
	NextSteps    []string `json:"next_steps"`
	Confidence   string   `json:"confidence" jsonschema:"enum=low,enum=medium,enum=high"`
}

// Review is one verdict on a draft, from whoever gave it.
type Review struct {
	Source   string `json:"source" jsonschema:"-"` // validator | rubric | critic | human
	Approved bool   `json:"approved"`
	Feedback string `json:"feedback"`
}

// Verdict is what check decided, computed in code from probabilities.
type Verdict string

const (
	VerdictReject    Verdict = "reject"
	VerdictUncertain Verdict = "uncertain"
	VerdictPass      Verdict = "pass"
)

// State is the whole run. Nodes mutate it; the runner checkpoints it after every node.
type State struct {
	IssueRef     string             `json:"issue_ref"`
	Issue        *github.Issue      `json:"issue,omitempty"`
	RootListing  []string           `json:"root_listing,omitempty"`
	Prior        Prior              `json:"prior"`
	Plan         *Plan              `json:"plan,omitempty"`
	Evidence     []Evidence         `json:"evidence"`
	Draft        *Triage            `json:"draft,omitempty"`
	Reviews      []Review           `json:"reviews"`
	CriticRounds int                `json:"critic_rounds"`
	Verdict      Verdict            `json:"verdict,omitempty"`
	Rubric       map[string]float64 `json:"rubric,omitempty"`
	Approved     bool               `json:"approved"`
	OutputPath   string             `json:"output_path,omitempty"`
}

// reject records a rejection and sets the verdict; the next route sends the draft back
// to the writer or, when the budget is spent, to the human.
func (s *State) reject(source, feedback string) {
	s.Reviews = append(s.Reviews, Review{Source: source, Feedback: feedback})
	s.Verdict = VerdictReject
}

// uncollected lists evidence the draft cites but collect never produced.
func (s *State) uncollected() []string {
	known := make(map[string]bool, len(s.Evidence))
	for _, e := range s.Evidence {
		known[e.Target] = true
	}
	var unknown []string
	for _, t := range s.Draft.EvidenceUsed {
		if !known[t] {
			unknown = append(unknown, t)
		}
	}
	return unknown
}
