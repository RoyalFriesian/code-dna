package knowledge

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// SavedConfig is persisted to ~/.code-dna/config.json by `codedna init`.
// It is loaded by all CLI commands before env-var overrides are applied.
type SavedConfig struct {
	BaseDir     string `json:"baseDir"`
	LLMProvider string `json:"llmProvider"` // "openai" | "ollama"
	// OpenAI
	APIKey     string `json:"apiKey,omitempty"`
	Model      string `json:"model,omitempty"`
	IndexModel string `json:"indexModel,omitempty"`
	QueryModel string `json:"queryModel,omitempty"`
	// Ollama
	OllamaURL   string `json:"ollamaURL,omitempty"`
	OllamaModel string `json:"ollamaModel,omitempty"`
}

// savedConfigPath returns the canonical path for the saved config file.
// It lives inside BaseDir (or ~/.code-dna if BaseDir is empty).
func savedConfigPath(baseDir string) string {
	if baseDir == "" {
		baseDir = filepath.Join(homeDir(), ".code-dna")
	}
	return filepath.Join(baseDir, "config.json")
}

// LoadSavedConfig reads the persisted config from disk.
// It checks (in order):
//  1. $KNOWLEDGE_BASE_DIR/config.json  (if KNOWLEDGE_BASE_DIR is set)
//  2. ~/.code-dna/config.json          (default)
//
// Returns an empty SavedConfig (not an error) if no file is found.
func LoadSavedConfig() (SavedConfig, error) {
	candidates := []string{}
	if dir := os.Getenv("KNOWLEDGE_BASE_DIR"); dir != "" {
		candidates = append(candidates, savedConfigPath(dir))
	}
	candidates = append(candidates, savedConfigPath(""))

	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return SavedConfig{}, err
		}
		var sc SavedConfig
		if err := json.Unmarshal(data, &sc); err != nil {
			return SavedConfig{}, err
		}
		return sc, nil
	}
	return SavedConfig{}, nil
}

// SaveConfig writes sc to disk at the canonical path.
func SaveConfig(sc SavedConfig) error {
	dir := sc.BaseDir
	if dir == "" {
		dir = filepath.Join(homeDir(), ".code-dna")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(sc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(savedConfigPath(dir), data, 0o600)
}

// ApplySavedConfig merges a SavedConfig into a Config.
// Values from sc fill any zero-value fields; env-var values (already loaded
// by ConfigFromEnv) take precedence when they are non-empty.
func ApplySavedConfig(cfg Config, sc SavedConfig) Config {
	if cfg.BaseDir == filepath.Join(homeDir(), ".code-dna") && sc.BaseDir != "" {
		cfg.BaseDir = sc.BaseDir
	}
	if cfg.LLMProvider == "" && sc.LLMProvider != "" {
		cfg.LLMProvider = sc.LLMProvider
	}
	if cfg.APIKey == "" && sc.APIKey != "" {
		cfg.APIKey = sc.APIKey
	}
	if cfg.Model == "gpt-4o-mini" && sc.Model != "" {
		cfg.Model = sc.Model
	}
	if cfg.IndexModel == "" && sc.IndexModel != "" {
		cfg.IndexModel = sc.IndexModel
	}
	if cfg.QueryModel == "" && sc.QueryModel != "" {
		cfg.QueryModel = sc.QueryModel
	}
	if cfg.OllamaURL == "" && sc.OllamaURL != "" {
		cfg.OllamaURL = sc.OllamaURL
	}
	if cfg.OllamaModel == "" && sc.OllamaModel != "" {
		cfg.OllamaModel = sc.OllamaModel
	}
	return cfg
}

// ActiveConfigPath returns the path of the config file that would be loaded
// by LoadSavedConfig, or an error if no file exists.
func ActiveConfigPath() (string, error) {
	candidates := []string{}
	if dir := os.Getenv("KNOWLEDGE_BASE_DIR"); dir != "" {
		candidates = append(candidates, savedConfigPath(dir))
	}
	candidates = append(candidates, savedConfigPath(""))
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", os.ErrNotExist
}
