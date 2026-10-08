package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := NewClient("tok")
	c.base = srv.URL
	return c
}

func TestIssue(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/o/r/issues/7" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("token not sent")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"title": "Crash", "body": "boom", "html_url": "https://x/7",
			"labels": []map[string]string{{"name": "bug"}, {"name": "p1"}},
		})
	})
	issue, err := c.Issue(context.Background(), "o", "r", 7)
	if err != nil {
		t.Fatal(err)
	}
	if issue.Title != "Crash" || len(issue.Labels) != 2 || issue.Number != 7 {
		t.Fatalf("unexpected issue %+v", issue)
	}
}

func TestFileDecodesBase64AndListsDirectories(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/r/contents/main.go":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"encoding": "base64",
				"content":  base64.StdEncoding.EncodeToString([]byte("package main\n")),
			})
		case "/repos/o/r/contents/src":
			_ = json.NewEncoder(w).Encode([]map[string]string{{"path": "src/a.go"}, {"path": "src/b.go"}})
		default:
			http.NotFound(w, r)
		}
	})
	ctx := context.Background()
	if got, _ := c.File(ctx, "o", "r", "main.go"); got != "package main\n" {
		t.Fatalf("file: %q", got)
	}
	if got, _ := c.File(ctx, "o", "r", "src"); got != "src/a.go\nsrc/b.go" {
		t.Fatalf("dir listing: %q", got)
	}
	if _, err := c.File(ctx, "o", "r", "nope"); err == nil {
		t.Fatal("want an error for a 404")
	}
}

func TestParseRef(t *testing.T) {
	t.Parallel()
	tests := []struct {
		ref  string
		ok   bool
		want [3]any
	}{
		{"octo/demo#7", true, [3]any{"octo", "demo", 7}},
		{"octo/demo", false, [3]any{}},
		{"octo#7", false, [3]any{}},
		{"octo/demo#x", false, [3]any{}},
		{"octo/demo#0", false, [3]any{}},
	}
	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			t.Parallel()
			owner, repo, n, err := ParseRef(tt.ref)
			if (err == nil) != tt.ok {
				t.Fatalf("ok=%v err=%v", tt.ok, err)
			}
			if tt.ok && (owner != tt.want[0] || repo != tt.want[1] || n != tt.want[2]) {
				t.Fatalf("got %s/%s#%d", owner, repo, n)
			}
		})
	}
}
