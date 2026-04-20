package knowledge

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// RepoID generates a deterministic short ID from an absolute repo path.
func RepoID(absPath string) string {
	h := sha256.Sum256([]byte(absPath))
	return fmt.Sprintf("%x", h[:6]) // 12 hex chars
}

// WriteManifest writes a manifest to the repo's knowledge base directory.
func WriteManifest(cfg Config, m Manifest) error {
	dir := cfg.RepoDir(m.Repo.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create repo dir: %w", err)
	}
	return writeJSON(filepath.Join(dir, "manifest.json"), m)
}

// ReadManifest reads a manifest for the given repo ID.
func ReadManifest(cfg Config, repoID string) (Manifest, error) {
	var m Manifest
	err := readJSON(filepath.Join(cfg.RepoDir(repoID), "manifest.json"), &m)
	return m, err
}

// ListRepos returns manifests for all indexed repos in the knowledge base.
func ListRepos(cfg Config) ([]Manifest, error) {
	entries, err := os.ReadDir(cfg.BaseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list knowledge base: %w", err)
	}
	var repos []Manifest
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m, err := ReadManifest(cfg, e.Name())
		if err != nil {
			continue // skip corrupt entries
		}
		repos = append(repos, m)
	}
	return repos, nil
}

// FindRepoByPath looks up an indexed repo by its original filesystem path.
func FindRepoByPath(cfg Config, repoPath string) (Manifest, bool, error) {
	abs, err := filepath.Abs(repoPath)
	if err != nil {
		return Manifest{}, false, fmt.Errorf("resolve path: %w", err)
	}
	id := RepoID(abs)
	m, err := ReadManifest(cfg, id)
	if err != nil {
		if os.IsNotExist(err) {
			return Manifest{}, false, nil
		}
		return Manifest{}, false, err
	}
	return m, true, nil
}

// NewRepo creates a fresh Repo with pending status.
func NewRepo(absPath, model string) Repo {
	now := time.Now().UTC()
	return Repo{
		ID:        RepoID(absPath),
		Path:      absPath,
		Hash:      "",
		Status:    "pending",
		Model:     model,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// WriteTree writes the L0 tree to disk.
func WriteTree(cfg Config, repoID string, tree []TreeNode) error {
	dir := filepath.Join(cfg.RepoDir(repoID), "l0")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return writeJSON(filepath.Join(dir, "tree.json"), tree)
}

// ReadTree reads the L0 tree from disk.
func ReadTree(cfg Config, repoID string) ([]TreeNode, error) {
	var tree []TreeNode
	err := readJSON(filepath.Join(cfg.RepoDir(repoID), "l0", "tree.json"), &tree)
	return tree, err
}

// WriteAgentSummaries writes L(level) agent summaries and index to disk.
func WriteAgentSummaries(cfg Config, repoID string, level int, agents []AgentSummary) error {
	dir := filepath.Join(cfg.RepoDir(repoID), fmt.Sprintf("l%d", level), "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// Write individual agent files
	for i, a := range agents {
		path := filepath.Join(dir, fmt.Sprintf("agent-%03d.json", i))
		if err := writeJSON(path, a); err != nil {
			return err
		}
	}
	// Build and write index: filepath → agent indices
	index := make(map[string][]int)
	for i, a := range agents {
		for _, fp := range a.FilePaths {
			index[fp] = append(index[fp], i)
		}
		for _, gid := range a.GroupIDs {
			key := fmt.Sprintf("group:%d", gid)
			index[key] = append(index[key], i)
		}
	}
	indexPath := filepath.Join(cfg.RepoDir(repoID), fmt.Sprintf("l%d", level), "index.json")
	return writeJSON(indexPath, index)
}

// ReadAgentSummaries reads all agent summaries for a given level.
func ReadAgentSummaries(cfg Config, repoID string, level int) ([]AgentSummary, error) {
	dir := filepath.Join(cfg.RepoDir(repoID), fmt.Sprintf("l%d", level), "agents")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var agents []AgentSummary
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		var a AgentSummary
		if err := readJSON(filepath.Join(dir, e.Name()), &a); err != nil {
			continue
		}
		agents = append(agents, a)
	}
	return agents, nil
}

// WriteMasterContext writes the final compressed master context.
func WriteMasterContext(cfg Config, repoID string, level int, content string) error {
	dir := filepath.Join(cfg.RepoDir(repoID), fmt.Sprintf("l%d", level))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "master-context.md"), []byte(content), 0o644)
}

// ReadMasterContext reads the master context for a repo.
func ReadMasterContext(cfg Config, repoID string, manifest Manifest) (string, error) {
	level := manifest.Repo.LevelsCount
	path := filepath.Join(cfg.RepoDir(repoID), fmt.Sprintf("l%d", level), "master-context.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// WriteCompressionMap writes the compression routing map.
func WriteCompressionMap(cfg Config, repoID string, level int, cm CompressionMap) error {
	dir := filepath.Join(cfg.RepoDir(repoID), fmt.Sprintf("l%d", level))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return writeJSON(filepath.Join(dir, "compression-map.json"), cm)
}

// ReadCompressionMap reads the compression routing map.
func ReadCompressionMap(cfg Config, repoID string, manifest Manifest) (CompressionMap, error) {
	level := manifest.Repo.LevelsCount
	var cm CompressionMap
	err := readJSON(filepath.Join(cfg.RepoDir(repoID), fmt.Sprintf("l%d", level), "compression-map.json"), &cm)
	return cm, err
}

func writeJSON(path string, v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

func readJSON(path string, v interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
