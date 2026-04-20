// Package llm provides a minimal OpenAI client that satisfies the
// knowledge.CompletionClient interface. It has no dependency on any
// other package in this module.
package llm

import (
	"context"
	"errors"
	"strings"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
)

// Client wraps the OpenAI Responses API and implements
// knowledge.CompletionClient.
type Client struct {
	inner openai.Client
}

// New creates a Client using the provided API key. Pass an empty string to
// rely on the OPENAI_API_KEY environment variable.
func New(apiKey string) *Client {
	opts := []option.RequestOption{}
	if apiKey != "" {
		opts = append(opts, option.WithAPIKey(apiKey))
	}
	return &Client{inner: openai.NewClient(opts...)}
}

// Generate sends a two-message (system + user) request to the OpenAI
// Responses API and returns the model's text output.
func (c *Client) Generate(ctx context.Context, model, systemPrompt, userPrompt string) (string, error) {
	input := responses.ResponseInputParam{
		responses.ResponseInputItemParamOfMessage(systemPrompt, responses.EasyInputMessageRoleSystem),
		responses.ResponseInputItemParamOfMessage(userPrompt, responses.EasyInputMessageRoleUser),
	}

	resp, err := c.inner.Responses.New(ctx, responses.ResponseNewParams{
		Model: model,
		Input: responses.ResponseNewParamsInputUnion{OfInputItemList: input},
	})
	if err != nil {
		return "", err
	}

	out := strings.TrimSpace(resp.OutputText())
	if out == "" {
		return "", errors.New("empty response from model")
	}
	return out, nil
}
