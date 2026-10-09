// Package jev wraps TypeSafe AI's System One model as a decision node.
//
// Jev does not generate text. It answers typed questions about a state with calibrated
// probabilities: noul (yes/no -> P(yes)), choice (one of N -> choice, confidence,
// probabilities), score (ordered scale). That makes it the extreme case of "the model is
// a node": a node that can only decide.
package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/mateusveloso/graphplay/internal/metrics"
)

// Endpoint is the System One API; Model is pinned because thresholds are tuned against
// one version and jev-preview moves.
const (
	Endpoint = "https://api.typesafe.ai/v1/systemone"
	Model    = "jev-1.13.0"
)

// Question is one typed question. Build them with Noul and Choice.
type Question struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions"`
	Criteria     any    `json:"criteria,omitempty"`
}

// Answer is the union of what the three question types return.
type Answer struct {
	Noul          *float64           `json:"noul,omitempty"`
	Choice        string             `json:"choice,omitempty"`
	Confidence    float64            `json:"confidence,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
}

// Decider is the contract the graph depends on. A nil result with a nil error is not
// possible: callers treat any error as "no decision" and take the default path.
type Decider interface {
	Decide(ctx context.Context, state string, questions map[string]Question) (map[string]Answer, error)
}

// Noul is a yes/no statement. The answer is P(yes); 0.5 means the model could not tell.
func Noul(instructions, whenTrue, whenFalse string) Question {
	return Question{
		Type:         "noul",
		Instructions: instructions,
		Criteria:     map[string]string{"true": whenTrue, "false": whenFalse},
	}
}

// Choice picks one option (at most 255). Each value says when its key applies.
func Choice(instructions string, options map[string]string) Question {
	return Question{Type: "choice", Instructions: instructions, Criteria: options}
}

// Client posts to the System One endpoint.
type Client struct {
	http     *http.Client
	endpoint string
	apiKey   string
	meter    *metrics.Meter
}

// NewClient returns a Decider backed by the hosted API. A nil meter is fine.
func NewClient(apiKey string, meter *metrics.Meter) *Client {
	return &Client{
		http:     &http.Client{Timeout: 15 * time.Second},
		endpoint: Endpoint,
		apiKey:   apiKey,
		meter:    meter,
	}
}

// Decide implements Decider with one request; the API answers every question in parallel.
func (c *Client) Decide(ctx context.Context, state string, questions map[string]Question) (answers map[string]Answer, err error) {
	started := time.Now()
	var usage struct{ in, out int }
	defer func() { c.meter.Record(Model, usage.in, usage.out, time.Since(started), err != nil) }()

	body, err := json.Marshal(map[string]any{"model": Model, "state": state, "questions": questions})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("jev: %s", resp.Status)
	}
	var out struct {
		Answers map[string]Answer `json:"answers"`
		Usage   struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	usage.in, usage.out = out.Usage.InputTokens, out.Usage.OutputTokens
	return out.Answers, nil
}

// None is the decider used when no API key is configured: every decision falls through.
type None struct{}

// Decide implements Decider by always declining to decide.
func (None) Decide(context.Context, string, map[string]Question) (map[string]Answer, error) {
	return nil, fmt.Errorf("jev: not configured")
}
