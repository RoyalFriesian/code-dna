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

// ServiceInfo describes a detected service (deployable unit) within a repository.
type ServiceInfo struct {
	// Name is the human-readable service name derived from the directory or
	// package name (e.g. "api-server", "worker", "knowledge-mcp").
	Name string `json:"name"`

	// RootPath is the absolute path to the top-level directory of this service
	// (e.g. /project/cmd/api or /project/services/worker).
	RootPath string `json:"rootPath"`

	// EntryPoint is the relative path of the main entry point file, when one
	// can be determined (e.g. "cmd/api/main.go").
	EntryPoint string `json:"entryPoint,omitempty"`

	// Files is the set of relative file paths that belong to this service.
	Files []string `json:"files"`
}

// ServiceDoc is the LLM-generated human-readable architecture document for
// one detected service.  Content is GitHub-flavoured Markdown with embedded
// Mermaid diagrams.
type ServiceDoc struct {
	ServiceName string    `json:"serviceName"`
	Content     string    `json:"content"`
	GeneratedAt time.Time `json:"generatedAt"`
}

// AgentGuide is the LLM-generated guide written for AI agents that will
// query this knowledge base.
type AgentGuide struct {
	Content     string    `json:"content"`
	GeneratedAt time.Time `json:"generatedAt"`
}
