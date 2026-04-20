package knowledge

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanRepo(t *testing.T) {
	dir := t.TempDir()

	createTestFile(t, dir, "main.go", "package main\nfunc main() {}\n")
	createTestFile(t, dir, "pkg/util.go", "package pkg\nfunc Helper() string { return \"hi\" }\n")
	createTestFile(t, dir, "README.md", "# Test\n")

	createTestFile(t, dir, ".git/config", "[core]\n")
	createTestFile(t, dir, "node_modules/pkg/index.js", "module.exports = {}\n")
	createTestFile(t, dir, "image.png", "\x89PNG\r\n\x1a\n")

	result, err := ScanRepo(dir, ScanModeSmart)
	if err != nil {
		t.Fatalf("ScanRepo error: %v", err)
	}

	if result.FileCount < 2 {
		t.Errorf("expected at least 2 files, got %d", result.FileCount)
	}

	for _, n := range result.Tree {
		if n.Path == ".git/config" {
			t.Error("should not include .git files")
		}
		if n.Path == "node_modules/pkg/index.js" {
			t.Error("should not include node_modules")
		}
	}

	foundGo := false
	for _, n := range result.Tree {
		if n.Name == "main.go" {
			foundGo = true
			if n.Language != "go" {
				t.Errorf("expected language 'go', got %q", n.Language)
			}
			if n.Hash == "" {
				t.Error("expected non-empty hash")
			}
			if n.Tokens <= 0 {
				t.Error("expected positive token estimate")
			}
		}
	}
	if !foundGo {
		t.Error("expected to find main.go")
	}
}

func TestScanRepoGitignore(t *testing.T) {
	dir := t.TempDir()

	createTestFile(t, dir, ".gitignore", "*.log\nbuild/\n")
	createTestFile(t, dir, "main.go", "package main\n")
	createTestFile(t, dir, "debug.log", "some log\n")
	createTestFile(t, dir, "build/output.js", "compiled\n")
	createTestFile(t, dir, "src/app.go", "package src\n")

	result, err := ScanRepo(dir, ScanModeSmart)
	if err != nil {
		t.Fatalf("ScanRepo error: %v", err)
	}

	for _, n := range result.Tree {
		if n.Name == "debug.log" {
			t.Error("should skip .log files per .gitignore")
		}
		if filepath.Dir(n.Path) == "build" {
			t.Error("should skip build/ per .gitignore")
		}
	}
}

func TestScanRepoEmpty(t *testing.T) {
	dir := t.TempDir()
	result, err := ScanRepo(dir, ScanModeSmart)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.FileCount != 0 {
		t.Errorf("expected 0 files, got %d", result.FileCount)
	}
}

func TestScanRepoNotDir(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file.txt")
	os.WriteFile(f, []byte("hi"), 0o644)
	_, err := ScanRepo(f, ScanModeSmart)
	if err == nil {
		t.Error("expected error for non-directory")
	}
}
