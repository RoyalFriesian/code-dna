package knowledge

import (
	"context"
	"testing"
)

func TestIndexRepo_FullPipeline(t *testing.T) {
	// Create a small repo to index
	repoDir := t.TempDir()
	createTestFile(t, repoDir, "main.go", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n")
	createTestFile(t, repoDir, "pkg/util.go", "package pkg\n\nfunc Helper() string {\n\treturn \"helper\"\n}\n")

	// Mock LLM that returns decreasing-size summaries
	client := &mockClient{
		responses: []string{
			"L1 summary for main.go: entry point, prints hello",
			"L1 summary for pkg/util.go: helper function",
			"# Master Context\nA simple Go project with main and pkg packages.",
		},
	}

	baseDir := t.TempDir()
	cfg := DefaultConfig()
	cfg.BaseDir = baseDir
	cfg.Model = "test-model"
	cfg.TargetTokens = 100000 // large enough that L1 summaries fit directly
	cfg.CompressionRatio = 0.10
	cfg.Concurrency = 1
	cfg.AgentFileLimit = 1 // 1 file per agent so we get 2 L1 agents

	var stages []string
	progress := func(stage string, current, total int) {
		stages = append(stages, stage)
	}

	manifest, err := IndexRepo(context.Background(), client, repoDir, cfg, progress)
	if err != nil {
		t.Fatalf("IndexRepo error: %v", err)
	}

	if manifest.Repo.Status != "ready" {
		t.Errorf("expected status 'ready', got %q", manifest.Repo.Status)
	}
	if manifest.Repo.FileCount != 2 {
		t.Errorf("expected 2 files, got %d", manifest.Repo.FileCount)
	}
	if len(manifest.Levels) < 2 {
		t.Errorf("expected at least 2 levels (L1 + master), got %d", len(manifest.Levels))
	}
	if manifest.Repo.LevelsCount < 2 {
		t.Errorf("expected LevelsCount >= 2, got %d", manifest.Repo.LevelsCount)
	}

	// Verify progress was reported
	if len(stages) == 0 {
		t.Error("expected progress callbacks")
	}

	// Verify manifest was persisted
	readBack, err := ReadManifest(cfg, manifest.Repo.ID)
	if err != nil {
		t.Fatalf("ReadManifest error: %v", err)
	}
	if readBack.Repo.Status != "ready" {
		t.Errorf("persisted manifest status: %q", readBack.Repo.Status)
	}

	// Verify tree was persisted
	tree, err := ReadTree(cfg, manifest.Repo.ID)
	if err != nil {
		t.Fatalf("ReadTree error: %v", err)
	}
	if len(tree) != 2 {
		t.Errorf("expected 2 tree nodes, got %d", len(tree))
	}
}

func TestIndexRepo_EmptyRepo(t *testing.T) {
	repoDir := t.TempDir()
	client := &mockClient{}

	cfg := DefaultConfig()
	cfg.BaseDir = t.TempDir()

	_, err := IndexRepo(context.Background(), client, repoDir, cfg, nil)
	if err == nil {
		t.Fatal("expected error for empty repo")
	}
}

func TestReindexRepo_NoExistingIndex(t *testing.T) {
	repoDir := t.TempDir()
	createTestFile(t, repoDir, "main.go", "package main\n")

	client := &mockClient{
		responses: []string{
			"L1 summary for main.go",
			"# Master Context\nSimple project.",
		},
	}

	cfg := DefaultConfig()
	cfg.BaseDir = t.TempDir()
	cfg.TargetTokens = 100000
	cfg.Concurrency = 1
	cfg.AgentFileLimit = 5

	manifest, changedFiles, err := ReindexRepo(context.Background(), client, repoDir, cfg, nil)
	if err != nil {
		t.Fatalf("ReindexRepo error: %v", err)
	}

	// First index: changedFiles == fileCount (full index)
	if manifest.Repo.Status != "ready" {
		t.Errorf("expected status 'ready', got %q", manifest.Repo.Status)
	}
	if changedFiles != manifest.Repo.FileCount {
		t.Errorf("expected changedFiles=%d (full index), got %d", manifest.Repo.FileCount, changedFiles)
	}
}

func TestReindexRepo_NoChanges(t *testing.T) {
	repoDir := t.TempDir()
	createTestFile(t, repoDir, "main.go", "package main\n")

	client := &mockClient{
		responses: []string{
			"L1 summary",
			"# Master Context",
			// Second index would need more, but we expect no-changes
		},
	}

	cfg := DefaultConfig()
	cfg.BaseDir = t.TempDir()
	cfg.TargetTokens = 100000
	cfg.Concurrency = 1
	cfg.AgentFileLimit = 5

	// First: full index
	_, _, err := ReindexRepo(context.Background(), client, repoDir, cfg, nil)
	if err != nil {
		t.Fatalf("first ReindexRepo error: %v", err)
	}

	// Second: no changes
	_, changedFiles, err := ReindexRepo(context.Background(), client, repoDir, cfg, nil)
	if err != nil {
		t.Fatalf("second ReindexRepo error: %v", err)
	}
	if changedFiles != 0 {
		t.Errorf("expected 0 changed files, got %d", changedFiles)
	}
}

func TestFilestore_RoundTrip(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BaseDir = t.TempDir()

	repoID := "test-repo-123"

	// Write and read manifest
	m := Manifest{
		Repo: Repo{
			ID:     repoID,
			Path:   "/tmp/test",
			Status: "ready",
		},
		Levels: []Level{{RepoID: repoID, Number: 1, AgentCount: 2, TotalTokens: 500}},
	}

	if err := WriteManifest(cfg, m); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	readM, err := ReadManifest(cfg, repoID)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if readM.Repo.Status != "ready" {
		t.Errorf("expected status 'ready', got %q", readM.Repo.Status)
	}

	// Write and read tree
	tree := []TreeNode{
		{Path: "main.go", Name: "main.go", Type: "file", Language: "go", Tokens: 50},
	}
	if err := WriteTree(cfg, repoID, tree); err != nil {
		t.Fatalf("WriteTree: %v", err)
	}
	readTree, err := ReadTree(cfg, repoID)
	if err != nil {
		t.Fatalf("ReadTree: %v", err)
	}
	if len(readTree) != 1 || readTree[0].Path != "main.go" {
		t.Errorf("unexpected tree: %+v", readTree)
	}

	// Write and read agent summaries
	agents := []AgentSummary{
		{RepoID: repoID, Level: 1, Index: 0, Summary: "agent 0", Tokens: 100, FilePaths: []string{"main.go"}},
	}
	if err := WriteAgentSummaries(cfg, repoID, 1, agents); err != nil {
		t.Fatalf("WriteAgentSummaries: %v", err)
	}
	readAgents, err := ReadAgentSummaries(cfg, repoID, 1)
	if err != nil {
		t.Fatalf("ReadAgentSummaries: %v", err)
	}
	if len(readAgents) != 1 || readAgents[0].Summary != "agent 0" {
		t.Errorf("unexpected agents: %+v", readAgents)
	}

	// Write and read master context
	if err := WriteMasterContext(cfg, repoID, 2, "# Master\nArchitecture overview"); err != nil {
		t.Fatalf("WriteMasterContext: %v", err)
	}
	readMaster, err := ReadMasterContext(cfg, repoID, Manifest{Repo: Repo{ID: repoID, LevelsCount: 2}})
	if err != nil {
		t.Fatalf("ReadMasterContext: %v", err)
	}
	if readMaster != "# Master\nArchitecture overview" {
		t.Errorf("unexpected master context: %q", readMaster)
	}

	// List repos
	repos, err := ListRepos(cfg)
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if len(repos) != 1 {
		t.Errorf("expected 1 repo, got %d", len(repos))
	}
}

func TestRepoID_Deterministic(t *testing.T) {
	id1 := RepoID("/tmp/test/repo")
	id2 := RepoID("/tmp/test/repo")
	if id1 != id2 {
		t.Errorf("expected deterministic IDs, got %q and %q", id1, id2)
	}

	id3 := RepoID("/tmp/other/repo")
	if id1 == id3 {
		t.Error("expected different IDs for different paths")
	}

	if len(id1) != 12 {
		t.Errorf("expected 12-char hex ID, got %d chars: %q", len(id1), id1)
	}
}
