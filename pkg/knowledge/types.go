package knowledge

import (
	"context"
	"time"
)

// Repo represents a single indexed repository.
type Repo struct {
	ID          string    `json:"id"`
	Path        string    `json:"path"`
	Hash        string    `json:"hash"`
	Status      string    `json:"status"` // pending, indexing, ready, failed
	LevelsCount int       `json:"levelsCount"`
	FileCount   int       `json:"fileCount"`
	TotalTokens int       `json:"totalTokens"`
	Model       string    `json:"model"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// Level represents one compression level in the hierarchy.
type Level struct {
	RepoID      string `json:"repoId"`
	Number      int    `json:"number"`
	AgentCount  int    `json:"agentCount"`
	TotalTokens int    `json:"totalTokens"`
}

// AgentSummary is a single summarizer agent's output at a given level.
type AgentSummary struct {
	RepoID    string   `json:"repoId"`
	Level     int      `json:"level"`
	Index     int      `json:"index"`
	FilePaths []string `json:"filePaths,omitempty"`
	GroupIDs  []int    `json:"groupIds,omitempty"`
	Summary   string   `json:"summary"`
	Tokens    int      `json:"tokens"`
	FileHash  string   `json:"fileHash"`
}

// Manifest is the top-level metadata file for an indexed repository.
type Manifest struct {
	Repo   Repo    `json:"repo"`
	Levels []Level `json:"levels"`
}

// TreeNode represents a file or directory in the scanned repo.
type TreeNode struct {
	Path     string     `json:"path"`
	Name     string     `json:"name"`
	Type     string     `json:"type"`
	Size     int64      `json:"size,omitempty"`
	Hash     string     `json:"hash,omitempty"`
	Language string     `json:"language,omitempty"`
	Tokens   int        `json:"tokens,omitempty"`
	Children []TreeNode `json:"children,omitempty"`
}

// LevelIndex maps identifiers to agent indices at a given level.
type LevelIndex struct {
	Level   int              `json:"level"`
	Entries map[string][]int `json:"entries"`
}

// CompressionMap tracks routing from master level down to L1.
type CompressionMap struct {
	Levels []LevelIndex `json:"levels"`
}

// AgentAssignment is the distributor output: which files an agent should summarize.
type AgentAssignment struct {
	Index      int      `json:"index"`
	FilePaths  []string `json:"filePaths"`
	TotalBytes int64    `json:"totalBytes"`
}

// CompletionClient is the LLM interface used by summarizer and resolver.
// Defined locally so pkg/knowledge has no dependency on ai-clients or agents/ceo.
type CompletionClient interface {
	Generate(ctx context.Context, model string, systemPrompt string, userPrompt string) (string, error)
}

// ProgressFunc is a callback for reporting pipeline progress.
type ProgressFunc func(stage string, current, total int)

// QueryResult is the output of a knowledge query.
type QueryResult struct {
	Answer  string   `json:"answer"`
	Sources []Source `json:"sources"`
}

// Source is a reference to a specific file/location in the evidence trail.
type Source struct {
	File    string `json:"file"`
	Lines   string `json:"lines,omitempty"`
	Symbols string `json:"symbols,omitempty"`
	Note    string `json:"note,omitempty"`
}
