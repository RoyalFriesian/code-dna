package knowledge

import (
	"context"
	"testing"
)

func TestCompressLevel_MasterContext(t *testing.T) {
	client := &mockClient{responses: []string{"# Master Context\nFull architecture overview"}}

	// Summaries that fit within target tokens
	summaries := []AgentSummary{
		{Level: 1, Index: 0, Summary: "Package A summary", Tokens: 100, FilePaths: []string{"a.go"}},
		{Level: 1, Index: 1, Summary: "Package B summary", Tokens: 100, FilePaths: []string{"b.go"}},
	}

	cfg := DefaultConfig()
	cfg.TargetTokens = 1000 // well above the 200 total

	compressed, isMaster, err := CompressLevel(context.Background(), client, "test-model", summaries, 1, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isMaster {
		t.Error("expected isMaster=true when summaries fit in target")
	}
	if len(compressed) != 1 {
		t.Errorf("expected 1 master summary, got %d", len(compressed))
	}
	if compressed[0].Summary != "# Master Context\nFull architecture overview" {
		t.Errorf("unexpected summary: %q", compressed[0].Summary)
	}
}

func TestCompressLevel_NeedsCompression(t *testing.T) {
	// Return different compressed texts per group
	client := &mockClient{responses: []string{"compressed-group-0", "compressed-group-1"}}

	// 12 summaries → should produce 2 groups of 6
	summaries := make([]AgentSummary, 12)
	for i := range summaries {
		summaries[i] = AgentSummary{
			Level:   1,
			Index:   i,
			Summary: "Some summary text that is long enough to have tokens",
			Tokens:  5000, // Each 5K → total 60K → exceeds target
		}
	}

	cfg := DefaultConfig()
	cfg.TargetTokens = 10000 // 60K total > 10K target
	cfg.CompressionRatio = 0.10

	compressed, isMaster, err := CompressLevel(context.Background(), client, "test-model", summaries, 1, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isMaster {
		t.Error("expected isMaster=false when summaries exceed target")
	}
	if len(compressed) != 2 {
		t.Errorf("expected 2 compressed groups, got %d", len(compressed))
	}
	for _, c := range compressed {
		if c.Level != 2 {
			t.Errorf("expected level 2, got %d", c.Level)
		}
	}
}

func TestCompressLevel_CancelledContext(t *testing.T) {
	client := &mockClient{}

	summaries := make([]AgentSummary, 12)
	for i := range summaries {
		summaries[i] = AgentSummary{Level: 1, Index: i, Summary: "text", Tokens: 5000}
	}

	cfg := DefaultConfig()
	cfg.TargetTokens = 10000

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := CompressLevel(ctx, client, "test-model", summaries, 1, cfg)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestGroupSummaries(t *testing.T) {
	summaries := make([]AgentSummary, 15)
	for i := range summaries {
		summaries[i] = AgentSummary{Index: i}
	}

	groups := groupSummaries(summaries, 6)
	if len(groups) != 3 {
		t.Errorf("expected 3 groups, got %d", len(groups))
	}
	if len(groups[0]) != 6 {
		t.Errorf("expected 6 in first group, got %d", len(groups[0]))
	}
	if len(groups[2]) != 3 {
		t.Errorf("expected 3 in last group, got %d", len(groups[2]))
	}
}

func TestGroupSummariesSingle(t *testing.T) {
	summaries := []AgentSummary{{Index: 0}}
	groups := groupSummaries(summaries, 6)
	if len(groups) != 1 {
		t.Errorf("expected 1 group, got %d", len(groups))
	}
}
