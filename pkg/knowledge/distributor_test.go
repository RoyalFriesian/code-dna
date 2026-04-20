package knowledge

import (
	"testing"
)

func TestDistributeFilesEmpty(t *testing.T) {
	cfg := DefaultConfig()
	result := DistributeFiles(nil, cfg)
	if result != nil {
		t.Errorf("expected nil for empty input, got %d assignments", len(result))
	}
}

func TestDistributeFilesGroupsByLimit(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AgentFileLimit = 2
	cfg.AgentTokenBudget = 100000

	files := []TreeNode{
		{Path: "a/1.go", Tokens: 100, Size: 400},
		{Path: "a/2.go", Tokens: 100, Size: 400},
		{Path: "a/3.go", Tokens: 100, Size: 400},
		{Path: "b/1.go", Tokens: 100, Size: 400},
		{Path: "b/2.go", Tokens: 100, Size: 400},
	}

	assignments := DistributeFiles(files, cfg)
	if len(assignments) < 3 {
		t.Errorf("expected at least 3 assignments (5 files / 2 per agent), got %d", len(assignments))
	}

	for _, a := range assignments {
		if len(a.FilePaths) > 2 {
			t.Errorf("agent %d has %d files, expected <= 2", a.Index, len(a.FilePaths))
		}
	}
}

func TestDistributeFilesTokenBudget(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AgentFileLimit = 100
	cfg.AgentTokenBudget = 500

	files := []TreeNode{
		{Path: "a/1.go", Tokens: 300, Size: 1200},
		{Path: "a/2.go", Tokens: 300, Size: 1200},
		{Path: "a/3.go", Tokens: 300, Size: 1200},
	}

	assignments := DistributeFiles(files, cfg)
	if len(assignments) < 2 {
		t.Errorf("expected at least 2 assignments due to token budget, got %d", len(assignments))
	}
}

func TestDistributeFilesLargeFileOwnAgent(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AgentFileLimit = 5
	cfg.AgentTokenBudget = 1000

	files := []TreeNode{
		{Path: "small.go", Tokens: 100, Size: 400},
		{Path: "huge.go", Tokens: 5000, Size: 20000}, // exceeds budget
		{Path: "other.go", Tokens: 100, Size: 400},
	}

	assignments := DistributeFiles(files, cfg)

	// The huge file should get its own agent
	foundHugeAlone := false
	for _, a := range assignments {
		if len(a.FilePaths) == 1 && a.FilePaths[0] == "huge.go" {
			foundHugeAlone = true
		}
	}
	if !foundHugeAlone {
		t.Error("expected huge.go to have its own agent assignment")
	}
}

func TestDistributeFilesIndexing(t *testing.T) {
	cfg := DefaultConfig()
	cfg.AgentFileLimit = 1

	files := []TreeNode{
		{Path: "a.go", Tokens: 100, Size: 400},
		{Path: "b.go", Tokens: 100, Size: 400},
		{Path: "c.go", Tokens: 100, Size: 400},
	}

	assignments := DistributeFiles(files, cfg)
	for i, a := range assignments {
		if a.Index != i {
			t.Errorf("assignment %d has Index=%d, expected %d", i, a.Index, i)
		}
	}
}
