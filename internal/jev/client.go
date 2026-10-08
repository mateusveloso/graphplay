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
)

const (
	Endpoint = "https://api.typesafe.ai/v1/systemone"
	Model    = "jev-1.13.0" // pinned: thresholds are tuned against one version
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

func Noul(instructions, whenTrue, whenFalse string) Question {
	return Question{
		Type:         "noul",
		Instructions: instructions,
		Criteria:     map[string]string{"true": whenTrue, "false": whenFalse},
	}
}

func Choice(instructions string, options map[string]string) Question {
	return Question{Type: "choice", Instructions: instructions, Criteria: options}
}

// Client posts to the System One endpoint.
type Client struct {
	http     *http.Client
	endpoint string
	apiKey   string
}

func NewClient(apiKey string) *Client {
	return &Client{
		http:     &http.Client{Timeout: 15 * time.Second},
		endpoint: Endpoint,
		apiKey:   apiKey,
	}
}

func (c *Client) Decide(ctx context.Context, state string, questions map[string]Question) (map[string]Answer, error) {
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
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Answers, nil
}

// None is the decider used when no API key is configured: every decision falls through.
type None struct{}

func (None) Decide(context.Context, string, map[string]Question) (map[string]Answer, error) {
	return nil, fmt.Errorf("jev: not configured")
}
