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
)

// OllamaClient calls the Ollama /api/chat endpoint and implements
// knowledge.CompletionClient. It has no dependency on the OpenAI SDK.
type OllamaClient struct {
	baseURL string
	model   string
	http    *http.Client
}

// NewOllama creates an OllamaClient.
//
//   - baseURL is the root Ollama URL, e.g. "http://192.168.68.109:11434".
//     A trailing slash is fine; it will be normalised.
//   - model is the Ollama model tag, e.g. "gemma4:e2b".
func NewOllama(baseURL, model string) *OllamaClient {
	return &OllamaClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		http: &http.Client{
			Timeout: 10 * time.Minute, // large models can be slow
		},
	}
}

// ollamaChatRequest is the JSON body sent to /api/chat.
type ollamaChatRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
}

type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ollamaChatResponse is the JSON body returned by /api/chat (stream:false).
type ollamaChatResponse struct {
	Message ollamaMessage `json:"message"`
	Error   string        `json:"error,omitempty"`
}

// Generate sends a system+user prompt to Ollama and returns the assistant reply.
// The model argument is ignored for Ollama — the model is fixed at construction
// time (NewOllama) and pulled/running on the Ollama server. This keeps the
// knowledge.CompletionClient interface compatible while letting OpenAI callers
// pass per-call model names without affecting the Ollama backend.
func (c *OllamaClient) Generate(ctx context.Context, _ /*model*/ string, systemPrompt, userPrompt string) (string, error) {

	payload := ollamaChatRequest{
		Model:  c.model,
		Stream: false,
		Messages: []ollamaMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		// No NumCtx override — let Ollama use the model's native context window.
		// gemma4:e2b natively supports 131,072 tokens; forcing a lower value here
		// would silently cap it.
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal ollama request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build ollama request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read ollama response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var out ollamaChatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("parse ollama response: %w", err)
	}
	if out.Error != "" {
		return "", fmt.Errorf("ollama error: %s", out.Error)
	}

	result := strings.TrimSpace(out.Message.Content)
	if result == "" {
		return "", errors.New("empty response from ollama model")
	}
	return result, nil
}
