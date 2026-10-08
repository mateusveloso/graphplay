package triage

import (
	"context"
	"testing"

	"github.com/mateusveloso/graph-issue-triage/internal/github"
)

func collectState(requests ...EvidenceRequest) *State {
	return &State{
		Issue: &github.Issue{Owner: "o", Repo: "r", Number: 1},
		Plan:  &Plan{Requests: requests},
	}
}

func TestCollectCapsRequestsInCode(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	repo := newRepo()
	var requests []EvidenceRequest
	for range 10 {
		requests = append(requests, EvidenceRequest{Kind: "read_file", Target: "README.md"})
	}
	s := collectState(requests...)

	if err := collect(repo, cfg)(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if len(s.Evidence) != cfg.MaxEvidenceRequests || repo.callCount() != cfg.MaxEvidenceRequests {
		t.Fatalf("want %d fetches, got %d evidence / %d calls", cfg.MaxEvidenceRequests, len(s.Evidence), repo.callCount())
	}
}

func TestCollectKeepsPlanOrderAndTurnsFailuresIntoEvidence(t *testing.T) {
	t.Parallel()
	s := collectState(
		EvidenceRequest{Kind: "read_file", Target: "missing.py"},
		EvidenceRequest{Kind: "search_code", Target: "def main"},
		EvidenceRequest{Kind: "read_file", Target: "README.md"},
	)
	if err := collect(newRepo(), testConfig(t))(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if s.Evidence[0].Error == "" || s.Evidence[0].Content != "" {
		t.Fatalf("a missing file must be an error item: %+v", s.Evidence[0])
	}
	if s.Evidence[1].Content != "[src/app.py]" {
		t.Fatalf("search hits: %+v", s.Evidence[1])
	}
	if s.Evidence[2].Content != "# demo\n" {
		t.Fatalf("file content: %+v", s.Evidence[2])
	}
}

func TestCollectTruncatesLargeFiles(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	cfg.MaxFileChars = 3
	s := collectState(EvidenceRequest{Kind: "read_file", Target: "README.md"})
	if err := collect(newRepo(), cfg)(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if s.Evidence[0].Content != "# d" {
		t.Fatalf("got %q", s.Evidence[0].Content)
	}
}
