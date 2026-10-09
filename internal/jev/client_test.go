package jev

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDecideSendsPinnedModelAndParsesAnswers(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model     string              `json:"model"`
			State     string              `json:"state"`
			Questions map[string]Question `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("bad body: %v", err)
		}
		if body.Model != Model || body.State != "the state" || body.Questions["q"].Type != "noul" {
			t.Errorf("unexpected request %+v", body)
		}
		if r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("missing bearer token")
		}
		_, _ = w.Write([]byte(`{"answers":{"q":{"noul":0.91},"c":{"choice":"bug","confidence":0.8,"probabilities":{"bug":0.8}}}}`))
	}))
	t.Cleanup(srv.Close)

	c := NewClient("k", nil)
	c.endpoint = srv.URL
	answers, err := c.Decide(context.Background(), "the state", map[string]Question{"q": Noul("yes?", "Yes.", "No.")})
	if err != nil {
		t.Fatal(err)
	}
	if *answers["q"].Noul != 0.91 || answers["c"].Choice != "bug" || answers["c"].Probabilities["bug"] != 0.8 {
		t.Fatalf("unexpected answers %+v", answers)
	}
}

func TestDecideReportsHTTPErrors(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)
	c := NewClient("k", nil)
	c.endpoint = srv.URL
	if _, err := c.Decide(context.Background(), "s", nil); err == nil {
		t.Fatal("want an error on 429")
	}
}

func TestNoneNeverDecides(t *testing.T) {
	t.Parallel()
	if _, err := (None{}).Decide(context.Background(), "s", nil); err == nil {
		t.Fatal("None must return an error so callers take the default path")
	}
}
