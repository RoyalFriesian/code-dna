package knowledge

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestSummarizeAgent_Basic(t *testing.T) {
	dir := t.TempDir()
	createTestFile(t, dir, "main.go", "package main\nfunc main() {}\n")

	client := &mockClient{responses: []string{"# Summary\nPackage main: entry point"}}

	assignment := AgentAssignment{
		Index:     0,
		FilePaths: []string{"main.go"},
	}

	result, err := SummarizeAgent(context.Background(), client, "test-model", dir, assignment, 0.10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Level != 1 {
		t.Errorf("expected level 1, got %d", result.Level)
	}
	if result.Index != 0 {
		t.Errorf("expected index 0, got %d", result.Index)
	}
	if result.Summary != "# Summary\nPackage main: entry point" {
		t.Errorf("unexpected summary: %q", result.Summary)
	}
	if len(client.calls) != 1 {
		t.Errorf("expected 1 LLM call, got %d", len(client.calls))
	}
	if client.calls[0].Model != "test-model" {
		t.Errorf("expected model test-model, got %s", client.calls[0].Model)
	}
}

func TestSummarizeAgent_UnreadableFile(t *testing.T) {
	dir := t.TempDir()
	// No files created, so path won't exist

	client := &mockClient{responses: []string{}}

	assignment := AgentAssignment{
		Index:     0,
		FilePaths: []string{"nonexistent.go"},
	}

	result, err := SummarizeAgent(context.Background(), client, "test-model", dir, assignment, 0.10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Summary != "No readable content." {
		t.Errorf("expected 'No readable content.', got %q", result.Summary)
	}
}

func TestSummarizeAgent_LLMError(t *testing.T) {
	dir := t.TempDir()
	createTestFile(t, dir, "main.go", "package main\n")

	client := &errorClient{err: fmt.Errorf("some internal error")}

	assignment := AgentAssignment{
		Index:     0,
		FilePaths: []string{"main.go"},
	}

	// Graceful degradation: should succeed with a placeholder summary, not return error
	result, err := SummarizeAgent(context.Background(), client, "test-model", dir, assignment, 0.10)
	if err != nil {
		t.Fatalf("expected graceful degradation, got error: %v", err)
	}
	if !strings.Contains(result.Summary, "Summary unavailable") {
		t.Errorf("expected placeholder summary, got %q", result.Summary)
	}
	if !strings.Contains(result.Summary, "some internal error") {
		t.Errorf("expected error message in placeholder, got %q", result.Summary)
	}
}

func TestSummarizeAllAgents_Concurrent(t *testing.T) {
	dir := t.TempDir()
	createTestFile(t, dir, "a.go", "package a\n")
	createTestFile(t, dir, "b.go", "package b\n")
	createTestFile(t, dir, "c.go", "package c\n")

	client := &mockClient{responses: []string{"summary-a", "summary-b", "summary-c"}}

	assignments := []AgentAssignment{
		{Index: 0, FilePaths: []string{"a.go"}},
		{Index: 1, FilePaths: []string{"b.go"}},
		{Index: 2, FilePaths: []string{"c.go"}},
	}

	cfg := DefaultConfig()
	cfg.Concurrency = 2
	cfg.CompressionRatio = 0.10

	results, err := SummarizeAllAgents(context.Background(), client, "test-model", dir, assignments, cfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}
}

func TestSummarizeAllAgents_CancelledContext(t *testing.T) {
	dir := t.TempDir()
	createTestFile(t, dir, "a.go", "package a\n")

	client := &mockClient{responses: []string{"summary"}}

	assignments := []AgentAssignment{
		{Index: 0, FilePaths: []string{"a.go"}},
	}

	cfg := DefaultConfig()
	cfg.Concurrency = 1

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // immediately cancel

	// No longer returns error — gracefully degrades with placeholder summaries
	results, err := SummarizeAllAgents(ctx, client, "test-model", dir, assignments, cfg, nil)
	if err != nil {
		t.Fatalf("expected graceful degradation, got error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !strings.Contains(results[0].Summary, "Summary unavailable") {
		t.Errorf("expected placeholder summary for cancelled context, got %q", results[0].Summary)
	}
}
