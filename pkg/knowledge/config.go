package knowledge

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
)

// Config holds all configuration for the knowledge indexing pipeline.
type Config struct {
	BaseDir          string   // Root directory for knowledge base storage
	Model            string   // Default LLM model (fallback when specific models are empty)
	IndexModel       string   // LLM model for indexing (L1 summarization, compression, master context)
	QueryModel       string   // LLM model for query drill-down decisions (can be cheap)
	ReasoningModel   string   // LLM model for final answer reasoning (should be strong)
	APIKey           string   // OpenAI API key
	TargetTokens     int      // Max tokens for master context (~80K)
	CompressionRatio float64  // Target compression per level (0.10 = 10%)
	Concurrency      int      // Worker pool size for L1 summarization
	AgentFileLimit   int      // Max files per agent assignment
	AgentTokenBudget int      // Target raw token budget per L1 agent
	ScanMode         ScanMode // "smart" (default) skips deps/generated; "deep" indexes everything
}

// GetIndexModel returns the model to use for indexing. Falls back to Model.
func (c Config) GetIndexModel() string {
	if c.IndexModel != "" {
		return c.IndexModel
	}
	return c.Model
}

// GetQueryModel returns the model for query drill-down. Falls back to Model.
func (c Config) GetQueryModel() string {
	if c.QueryModel != "" {
		return c.QueryModel
	}
	return c.Model
}

// GetReasoningModel returns the strong model for final answer. Falls back to Model.
func (c Config) GetReasoningModel() string {
	if c.ReasoningModel != "" {
		return c.ReasoningModel
	}
	return c.Model
}

// ScanMode controls how aggressively the scanner filters out non-user code.
type ScanMode string

const (
	// ScanModeSmart skips dependency directories, auto-generated files, lock
	// files, and vendor packages. Only manually written code is indexed.
	ScanModeSmart ScanMode = "smart"

	// ScanModeDeep indexes every text file including packages, generated code,
	// and vendored dependencies. Useful for auditing or when you need full
	// coverage of all code running in production.
	ScanModeDeep ScanMode = "deep"
)

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		BaseDir:          filepath.Join(homeDir(), ".code-dna"),
		Model:            "gpt-4o-mini",
		TargetTokens:     80000,
		CompressionRatio: 0.10,
		Concurrency:      5,
		AgentFileLimit:   5,
		AgentTokenBudget: 50000,
		ScanMode:         ScanModeSmart,
	}
}

// ConfigFromEnv builds a Config from environment variables, falling back to defaults.
func ConfigFromEnv() Config {
	cfg := DefaultConfig()

	if v := os.Getenv("KNOWLEDGE_BASE_DIR"); v != "" {
		cfg.BaseDir = v
	}
	if v := os.Getenv("KNOWLEDGE_MODEL"); v != "" {
		cfg.Model = v
	}
	if v := os.Getenv("KNOWLEDGE_INDEX_MODEL"); v != "" {
		cfg.IndexModel = v
	}
	if v := os.Getenv("KNOWLEDGE_QUERY_MODEL"); v != "" {
		cfg.QueryModel = v
	}
	if v := os.Getenv("KNOWLEDGE_REASONING_MODEL"); v != "" {
		cfg.ReasoningModel = v
	}
	if v := os.Getenv("OPENAI_API_KEY"); v != "" {
		cfg.APIKey = v
	}
	if v := os.Getenv("KNOWLEDGE_TARGET_TOKENS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.TargetTokens = n
		}
	}
	if v := os.Getenv("KNOWLEDGE_COMPRESSION_RATIO"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 && f < 1 {
			cfg.CompressionRatio = f
		}
	}
	if v := os.Getenv("KNOWLEDGE_CONCURRENCY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Concurrency = n
		}
	}
	if v := os.Getenv("KNOWLEDGE_SCAN_MODE"); v != "" {
		switch ScanMode(v) {
		case ScanModeSmart, ScanModeDeep:
			cfg.ScanMode = ScanMode(v)
		}
	}

	return cfg
}

// RepoDir returns the knowledge base directory for a specific repo.
func (c Config) RepoDir(repoID string) string {
	return filepath.Join(c.BaseDir, repoID)
}

// Validate checks that required configuration fields are present.
func (c Config) Validate() error {
	if c.APIKey == "" {
		return errMissingAPIKey
	}
	return nil
}

var errMissingAPIKey = &configError{"OPENAI_API_KEY is required"}

type configError struct{ msg string }

func (e *configError) Error() string { return e.msg }

func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	if runtime.GOOS == "windows" {
		return os.Getenv("USERPROFILE")
	}
	return os.Getenv("HOME")
}
