package main

import (
	"github.com/RoyalFriesian/code-dna/llm"
	"github.com/RoyalFriesian/code-dna/pkg/knowledge"
)

func newLLMClient(cfg knowledge.Config) knowledge.CompletionClient {
	return llm.New(cfg.APIKey)
}
