package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type answer struct {
	Word  string `json:"word" jsonschema_description:"one word"`
	Score int    `json:"score" jsonschema:"minimum=0"`
}

func TestAskSendsJSONModeWithSchemaAndParsesTheReply(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("bad request: %s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode: %v", err)
		}
		if req.Model != "deepseek-flash" || req.ResponseFormat.Type != "json_object" {
			t.Errorf("model/format: %+v", req)
		}
		system := req.Messages[0].Content
		if !strings.Contains(strings.ToLower(system), "json") || !strings.Contains(system, `"word"`) || !strings.Contains(system, "one word") {
			t.Errorf("system prompt lacks json mode cue or schema:\n%s", system)
		}
		// The model wraps the object in a fence anyway; the adapter must cope.
		fenced := "\x60\x60\x60json\n{\"word\":\"echo\",\"score\":3}\n\x60\x60\x60"
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": fenced}}}})
	}))
	t.Cleanup(srv.Close)

	var got answer
	if err := NewOpenAICompat(srv.URL, "k", "deepseek-flash").Ask(context.Background(), "You solve riddles.", "What repeats?", &got); err != nil {
		t.Fatal(err)
	}
	if got.Word != "echo" || got.Score != 3 {
		t.Fatalf("parsed %+v", got)
	}
}

func TestAskReportsProviderErrorsAndBadJSON(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		status int
		body   string
		want   string
	}{
		"http error":   {http.StatusUnauthorized, `{"error":{"message":"invalid key"}}`, "401"},
		"empty choice": {http.StatusOK, `{"choices":[]}`, "empty completion"},
		"not json":     {http.StatusOK, `{"choices":[{"message":{"content":"sure!"}}]}`, "not the expected json"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(srv.Close)
			var got answer
			err := NewOpenAICompat(srv.URL, "k", "m").Ask(context.Background(), "s", "u", &got)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}
