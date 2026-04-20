package llm

import "github.com/RoyalFriesian/code-dna/pkg/knowledge"

// Provider selects which LLM backend to use.
type Provider string

const (
	ProviderOpenAI Provider = "openai"
	ProviderOllama Provider = "ollama"
)

// NewFromConfig returns a knowledge.CompletionClient based on the provider
// setting in cfg.
//
//   - "ollama"  → OllamaClient pointed at cfg.OllamaURL with cfg.OllamaModel
//   - anything else (default) → OpenAI Client
func NewFromConfig(cfg knowledge.Config) knowledge.CompletionClient {
	if Provider(cfg.LLMProvider) == ProviderOllama {
		return NewOllama(cfg.OllamaURL, cfg.OllamaModel)
	}
	return New(cfg.APIKey)
}
