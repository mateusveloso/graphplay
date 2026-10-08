package llm

import (
	"context"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// Anthropic asks Claude for a JSON object matching the Go type of out, via structured
// outputs. The SDK derives the JSON schema from the struct and parses the reply into it.
type Anthropic struct {
	client anthropic.Client
	model  string
}

// NewAnthropic returns a Model bound to one Claude model id. An empty apiKey lets the SDK
// resolve credentials itself (ANTHROPIC_API_KEY or a logged-in profile).
func NewAnthropic(apiKey, model string) *Anthropic {
	var opts []option.RequestOption
	if apiKey != "" {
		opts = append(opts, option.WithAPIKey(apiKey))
	}
	return &Anthropic{client: anthropic.NewClient(opts...), model: model}
}

// Ask implements Model with one non-streaming request. 16k output tokens is the SDK's
// safe ceiling before HTTP timeouts become a concern.
func (a *Anthropic) Ask(ctx context.Context, system, user string, out any) error {
	_, err := a.client.Beta.Messages.New(ctx, anthropic.BetaMessageNewParams{
		Model:     a.model,
		MaxTokens: 16000,
		System:    []anthropic.BetaTextBlockParam{{Text: system}},
		Messages: []anthropic.BetaMessageParam{
			anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(user)),
		},
		OutputConfig: anthropic.BetaOutputConfigParam{
			Format: anthropic.BetaJSONOutputFormatParam{Schema: out},
		},
	})
	return err
}
