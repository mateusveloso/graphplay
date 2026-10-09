package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/invopop/jsonschema"

	"github.com/mateusveloso/graphplay/internal/metrics"
)

// DeepSeekBaseURL is the OpenAI-compatible endpoint of api.deepseek.com.
const DeepSeekBaseURL = "https://api.deepseek.com"

// OpenAICompat asks any OpenAI-compatible chat/completions endpoint for a JSON object
// matching the Go type of out. These APIs guarantee valid JSON (response_format
// json_object) but not a schema, so the schema derived from the struct goes into the
// system prompt and the reply is validated by unmarshalling into the struct.
type OpenAICompat struct {
	http      *http.Client
	baseURL   string
	apiKey    string
	model     string
	meter     *metrics.Meter
	thinking  *bool // nil: provider default; false: off (bounded tasks); true: on
	maxTokens int
}

// defaultMaxTokens is enough for any typed answer in this program when the model does not
// reason first. A reasoning model spends the same budget on its thinking, so a role that
// keeps thinking on asks for more with WithMaxTokens.
const defaultMaxTokens = 4096

// WithoutThinking turns the provider's reasoning mode off for this model. A bounded,
// structured task does not need it, and a reasoning model that thinks inside the completion
// budget can return an empty object when the budget runs out.
func (c *OpenAICompat) WithoutThinking() *OpenAICompat {
	off := false
	c.thinking = &off
	return c
}

// NewOpenAICompat returns a Model bound to one model id on one endpoint. A nil meter is fine.
func NewOpenAICompat(baseURL, apiKey, model string, meter *metrics.Meter) *OpenAICompat {
	return &OpenAICompat{
		http:      &http.Client{Timeout: 180 * time.Second},
		baseURL:   strings.TrimRight(baseURL, "/"),
		apiKey:    apiKey,
		model:     model,
		meter:     meter,
		maxTokens: defaultMaxTokens,
	}
}

// WithMaxTokens sets the completion budget, reasoning included where the model reasons.
func (c *OpenAICompat) WithMaxTokens(n int) *OpenAICompat {
	c.maxTokens = n
	return c
}

// NewDeepSeek is NewOpenAICompat pointed at DeepSeek.
func NewDeepSeek(apiKey, model string, meter *metrics.Meter) *OpenAICompat {
	return NewOpenAICompat(DeepSeekBaseURL, apiKey, model, meter)
}

type chatRequest struct {
	Model          string        `json:"model"`
	Messages       []chatMessage `json:"messages"`
	ResponseFormat format        `json:"response_format"`
	MaxTokens      int           `json:"max_tokens"`
	Thinking       *thinking     `json:"thinking,omitempty"`
}

type thinking struct {
	Type string `json:"type"` // "enabled" | "disabled"
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type format struct {
	Type string `json:"type"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// errTransient marks failures worth one more try: an empty completion (documented as
// occasional), rate limiting, or a server error.
type errTransient struct{ err error }

func (e errTransient) Error() string { return e.err.Error() }
func (e errTransient) Unwrap() error { return e.err }

const attempts = 3

// Ask implements Model. Transient failures are retried with a short backoff.
func (c *OpenAICompat) Ask(ctx context.Context, system, user string, out any) error {
	schema, err := schemaOf(out)
	if err != nil {
		return err
	}
	for attempt := 1; ; attempt++ {
		err = c.ask(ctx, system, user, schema, out)
		var transient errTransient
		if err == nil || !errors.As(err, &transient) || attempt == attempts {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt) * time.Second):
		}
	}
}

func (c *OpenAICompat) ask(ctx context.Context, system, user, schema string, out any) (err error) {
	started := time.Now()
	var usage struct{ in, out int }
	defer func() { c.meter.Record(c.model, usage.in, usage.out, time.Since(started), err != nil) }()

	// The word "json" must appear in the prompt for json_object mode; the schema is the
	// example the provider asks for.
	system += "\n\nRespond with a single json object and nothing else. It must conform to this JSON Schema:\n" + schema

	req0 := chatRequest{
		Model:          c.model,
		Messages:       []chatMessage{{Role: "system", Content: system}, {Role: "user", Content: user}},
		ResponseFormat: format{Type: "json_object"},
		MaxTokens:      c.maxTokens,
	}
	if c.thinking != nil {
		req0.Thinking = &thinking{Type: "disabled"}
		if *c.thinking {
			req0.Thinking.Type = "enabled"
		}
	}
	body, err := json.Marshal(req0)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		err := fmt.Errorf("%s: %s: %s", c.baseURL, resp.Status, strings.TrimSpace(string(raw)))
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			return errTransient{err}
		}
		return err
	}
	var parsed chatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return fmt.Errorf("%s: decode response: %w", c.baseURL, err)
	}
	usage.in, usage.out = parsed.Usage.PromptTokens, parsed.Usage.CompletionTokens
	if parsed.Error != nil {
		return fmt.Errorf("%s: %s", c.baseURL, parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 || parsed.Choices[0].Message.Content == "" {
		return errTransient{fmt.Errorf("%s: empty completion", c.baseURL)}
	}
	content := stripFences(parsed.Choices[0].Message.Content)
	if err := json.Unmarshal([]byte(content), out); err != nil {
		return fmt.Errorf("%s: reply is not the expected json: %w\n%s", c.baseURL, err, content)
	}
	return nil
}

// schemaOf renders the JSON Schema of out's type, honoring the same jsonschema tags the
// Anthropic adapter relies on.
func schemaOf(out any) (string, error) {
	r := jsonschema.Reflector{ExpandedStruct: true, DoNotReference: true}
	s := r.Reflect(out)
	s.Version = "" // keep the prompt short
	raw, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// stripFences tolerates a model that wraps the object in a ```json block anyway.
func stripFences(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	return strings.TrimSpace(s)
}
