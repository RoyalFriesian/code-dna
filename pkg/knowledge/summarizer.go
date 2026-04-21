package knowledge

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const l1SystemPrompt = `You are a knowledge extraction agent. Your job is to create a precise, structured summary of the files provided.

IMPORTANT: First detect the type of each file. Files can be source code, data/log files, configuration, documentation, or rule/specification files. Adapt your summary format to the file type.

## For SOURCE CODE files (.go, .py, .js, .ts, .java, .rs, .c, .cpp, .rb, etc.):
- Package/module name and purpose (1 line)
- All imports/dependencies (list)
- Every exported/public symbol: type, name, line number, signature
- Every function/method: name, receiver (if any), parameters, return types, line range, one-line purpose
- Interface definitions: name, method signatures, line numbers
- Struct/class definitions: name, key fields summary, line numbers
- Constants/enums if present
- Key logic flows and algorithmic patterns (brief)
- File-level purpose summary (2-3 sentences)

## For DATA / LOG / EVENT files (.txt, .log, .csv, .jsonl, or files containing timestamped entries):
- File purpose: what kind of data this file contains (1 line)
- Record format: the exact pattern/schema of each line (with a real example)
- Total record count: exact number of lines/entries
- Key entities mentioned (names, IDs, identifiers) with frequency counts
- Time range: first timestamp to last timestamp
- Per-entry details: for EVERY entry, record the line number, timestamp, and action/event
  Example: "Line 1: [2020-01-01 12:00:00] caught ball (white circle)"
- Statistical summary: count of each action type (e.g., 5 bounces, 3 catches, 2 color changes)
- Relationships and patterns: any cross-references, sequences, or correlations visible in the data
- Unique values: list all distinct values for each field (colors, shapes, statuses, etc.)

## For RULES / SPECIFICATION / DOCUMENTATION files (.md, RULES.txt, README, etc.):
- Document purpose (1 line)
- Complete summary of ALL rules, constraints, and mechanics defined
- Key entities and relationships defined in the document
- Any formulas, timing rules, sequences, or state machines described
- How this document relates to other files (e.g., "defines the game that player logs record")
- Critical details that would be needed to answer questions about the data (capture ALL of them)

## For CONFIGURATION files (.yaml, .json, .toml, .env, .ini, etc.):
- Purpose of the configuration
- Every key-value pair or setting with its meaning
- Default values and valid ranges
- Dependencies on other config or environment variables

## DRILL-DOWN HINTS (required for ALL file types):
At the end of each file's summary, add a section:
### Drill-Down Hints
- List the specific questions this file can answer
- List what keywords or patterns to search for in the raw file
- List which OTHER files would need to be consulted alongside this file to answer cross-file questions
- Specify the exact line ranges for key sections of data

Rules:
- Keep total output at most %d tokens (approximately %d%% of input size)
- Use a structured format with clear headings per file
- Include line numbers for all important items
- Do NOT include full raw data — summarize with line references
- Do NOT add commentary or opinions — just facts
- If a file is a test file, summarize what is tested, not how
- PRESERVE ALL TIMESTAMPS — they are critical for cross-file correlation
- PRESERVE EXACT COUNTS — approximate counts are not acceptable for data files`

// l1CompactPrompt is a shorter system prompt for local/smaller models (e.g. Ollama).
// It produces faster results by reducing instruction overhead.
const l1CompactPrompt = `You are a code summarization agent. Create a structured summary of the files provided.

For each file include:
- Package/module name and purpose (1 line)
- All exported/public symbols: type, name, signature
- Key functions: name, params, return types, purpose
- Struct/class definitions with key fields
- Architecture patterns and data flow

Rules:
- Max output: %d tokens (~%d%% of input)
- Use clear headings per file
- Include line numbers for important items
- Facts only — no opinions`

// isContextLengthError returns true when the LLM rejected the request
// because the combined prompt exceeded its context window.
func isContextLengthError(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "context_length_exceeded") ||
		strings.Contains(msg, "maximum context length") ||
		strings.Contains(msg, "token limit") ||
		strings.Contains(msg, "reduce the length")
}

// isRateLimitError returns true when the LLM returned a rate-limit (429).
func isRateLimitError(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "rate_limit") ||
		strings.Contains(msg, "429") ||
		strings.Contains(msg, "too many requests") ||
		strings.Contains(msg, "tokens per min")
}

// retryBackoffs defines the wait durations for rate-limit retries.
var retryBackoffs = []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second}

// buildAgentPrompt assembles the file contents for an agent assignment,
// optionally truncating each file to a fraction of its original length.
func buildAgentPrompt(repoRoot string, assignment AgentAssignment, truncateFrac float64) (userPrompt string, totalTokens int) {
	var buf strings.Builder
	for _, fp := range assignment.FilePaths {
		absPath := filepath.Join(repoRoot, fp)
		data, err := os.ReadFile(absPath)
		if err != nil {
			buf.WriteString(fmt.Sprintf("\n--- File: %s ---\n[unreadable: %v]\n", fp, err))
			continue
		}
		content := string(data)
		if truncateFrac < 1.0 {
			limit := int(float64(len(content)) * truncateFrac)
			if limit < len(content) {
				content = content[:limit] + "\n... [truncated]\n"
			}
		}
		tokens := estimateTokens(int64(len(content)))
		totalTokens += tokens
		lineCount := countLines(content)
		fileType := detectFileType(fp)
		buf.WriteString(fmt.Sprintf("\n--- File: %s (type: %s, lines: %d, tokens: ~%d) ---\n",
			fp, fileType, lineCount, tokens))
		buf.WriteString(content)
		buf.WriteString("\n")
	}
	userPrompt = fmt.Sprintf("Summarize the following %d files:\n%s",
		len(assignment.FilePaths), buf.String())
	return
}

// detectFileType classifies a file for the summarizer prompt.
func detectFileType(path string) string {
	lower := strings.ToLower(path)
	name := strings.ToLower(filepath.Base(path))

	// Rules/specification files
	if strings.Contains(name, "rule") || strings.Contains(name, "spec") ||
		name == "readme.md" || name == "readme.txt" || name == "readme" {
		return "rules/specification"
	}

	// Documentation
	ext := filepath.Ext(lower)
	switch ext {
	case ".md", ".rst", ".adoc":
		return "documentation"
	case ".go", ".py", ".js", ".ts", ".tsx", ".jsx", ".java", ".rs", ".c",
		".cpp", ".cc", ".h", ".hpp", ".rb", ".cs", ".swift", ".kt",
		".scala", ".php", ".pl", ".sh", ".bash", ".zsh", ".lua",
		".r", ".m", ".mm", ".zig", ".nim", ".ex", ".exs", ".erl",
		".hs", ".ml", ".fs", ".v", ".sv", ".vhd":
		return "source-code"
	case ".json", ".yaml", ".yml", ".toml", ".ini", ".env", ".cfg",
		".conf", ".properties", ".xml":
		return "configuration"
	case ".csv":
		return "data/csv"
	case ".log":
		return "data/log"
	case ".sql":
		return "source-code/sql"
	case ".txt":
		// .txt could be data or docs — check content heuristics from name
		if strings.HasPrefix(name, "player") || strings.Contains(name, "log") ||
			strings.Contains(name, "event") || strings.Contains(name, "record") {
			return "data/log"
		}
		return "data/text"
	}

	return "unknown"
}

// agentFileHash computes a combined hash of all files in an assignment.
// Files are sorted by path for determinism.
func agentFileHash(repoRoot string, filePaths []string) string {
	sorted := make([]string, len(filePaths))
	copy(sorted, filePaths)
	sort.Strings(sorted)

	h := sha256.New()
	for _, fp := range sorted {
		data, err := os.ReadFile(filepath.Join(repoRoot, fp))
		if err != nil {
			continue
		}
		h.Write([]byte(fp))
		h.Write(data)
	}
	return fmt.Sprintf("%x", h.Sum(nil)[:16])
}

// SummarizeAgent produces an L1 summary for a single agent assignment.
// It handles context-length errors by truncating files to 50% and retrying,
// and handles rate-limit errors with exponential backoff.
func SummarizeAgent(ctx context.Context, client CompletionClient, model string, repoRoot string,
	assignment AgentAssignment, cfg Config) (AgentSummary, error) {

	compressionRatio := cfg.CompressionRatio
	userPrompt, totalInputTokens := buildAgentPrompt(repoRoot, assignment, 1.0)
	hash := agentFileHash(repoRoot, assignment.FilePaths)

	if totalInputTokens == 0 {
		return AgentSummary{
			Level:     1,
			Index:     assignment.Index,
			FilePaths: assignment.FilePaths,
			Summary:   "No readable content.",
			Tokens:    0,
			FileHash:  hash,
		}, nil
	}

	// Proactive truncation: Ollama silently clips input beyond num_ctx
	// instead of returning an error, so the context-length retry never fires.
	// gemma4:e2b supports 128K context natively. Use ~65% for input to leave
	// room for the system prompt and generated output (best perf at 60-70%).
	const maxInputTokens = 83000
	truncFrac := 1.0
	if totalInputTokens > maxInputTokens {
		truncFrac = float64(maxInputTokens) / float64(totalInputTokens)
		slog.Warn("input exceeds context window, pre-truncating",
			"agent", assignment.Index, "inputTokens", totalInputTokens,
			"truncFrac", fmt.Sprintf("%.2f", truncFrac))
		userPrompt, totalInputTokens = buildAgentPrompt(repoRoot, assignment, truncFrac)
	}

	targetTokens := int(float64(totalInputTokens) * compressionRatio)
	if targetTokens < 100 {
		targetTokens = 100
	}
	pct := int(compressionRatio * 100)
	var systemPrompt string
	if cfg.CompactPrompts {
		systemPrompt = fmt.Sprintf(l1CompactPrompt, targetTokens, pct)
	} else {
		systemPrompt = fmt.Sprintf(l1SystemPrompt, targetTokens, pct)
	}

	// callLLM wraps the Generate call with rate-limit retry.
	callLLM := func(sys, user string) (string, error) {
		var lastErr error
		for attempt := 0; attempt <= len(retryBackoffs); attempt++ {
			resp, err := client.Generate(ctx, model, sys, user)
			if err == nil {
				return resp, nil
			}
			if isRateLimitError(err) && attempt < len(retryBackoffs) {
				wait := retryBackoffs[attempt]
				slog.Warn("rate-limited by LLM, backing off",
					"agent", assignment.Index, "attempt", attempt+1, "wait", wait)
				select {
				case <-time.After(wait):
				case <-ctx.Done():
					return "", ctx.Err()
				}
				lastErr = err
				continue
			}
			return "", err
		}
		return "", lastErr
	}

	response, err := callLLM(systemPrompt, userPrompt)
	if err != nil && isContextLengthError(err) {
		// Retry with files truncated to 50%
		slog.Warn("context length exceeded, retrying with truncated input",
			"agent", assignment.Index)
		truncatedPrompt, truncTokens := buildAgentPrompt(repoRoot, assignment, 0.5)
		truncTarget := int(float64(truncTokens) * compressionRatio)
		if truncTarget < 100 {
			truncTarget = 100
		}
		truncSystem := fmt.Sprintf(l1SystemPrompt, truncTarget, pct)
		if cfg.CompactPrompts {
			truncSystem = fmt.Sprintf(l1CompactPrompt, truncTarget, pct)
		}
		response, err = callLLM(truncSystem, truncatedPrompt)
	}

	if err != nil {
		// Graceful degradation: produce a placeholder summary instead of failing
		slog.Error("agent summarization failed, using placeholder",
			"agent", assignment.Index, "err", err)
		var fileListing strings.Builder
		for _, fp := range assignment.FilePaths {
			fileListing.WriteString("  - " + fp + "\n")
		}
		placeholder := fmt.Sprintf("[Summary unavailable — LLM error: %v]\n\nFiles in this agent:\n%s",
			err, fileListing.String())
		return AgentSummary{
			Level:     1,
			Index:     assignment.Index,
			FilePaths: assignment.FilePaths,
			Summary:   placeholder,
			Tokens:    estimateTokens(int64(len(placeholder))),
			FileHash:  hash,
		}, nil
	}

	return AgentSummary{
		Level:     1,
		Index:     assignment.Index,
		FilePaths: assignment.FilePaths,
		Summary:   response,
		Tokens:    estimateTokens(int64(len(response))),
		FileHash:  hash,
	}, nil
}

// SummarizeAllAgents runs L1 summarization across all assignments concurrently.
// When repoID is non-empty, each agent summary is written to disk as soon as it
// completes (incremental persistence). The index file is written at the end.
func SummarizeAllAgents(ctx context.Context, client CompletionClient, model string, repoRoot string,
	assignments []AgentAssignment, cfg Config, progress ProgressFunc, repoID ...string) ([]AgentSummary, error) {

	// Determine whether to write incrementally.
	incrementalID := ""
	if len(repoID) > 0 && repoID[0] != "" {
		incrementalID = repoID[0]
	}

	results := make([]AgentSummary, len(assignments))
	errs := make([]error, len(assignments))

	sem := make(chan struct{}, cfg.Concurrency)
	var wg sync.WaitGroup
	var completedCount int32
	var mu sync.Mutex

	for i, a := range assignments {
		wg.Add(1)
		go func(idx int, assign AgentAssignment) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if ctx.Err() != nil {
				errs[idx] = ctx.Err()
				return
			}

			result, err := SummarizeAgent(ctx, client, model, repoRoot, assign, cfg)
			if err != nil {
				errs[idx] = err
				return
			}
			result.RepoID = "" // will be set by caller
			results[idx] = result

			// Write to disk immediately so progress is visible.
			if incrementalID != "" {
				if wErr := WriteOneAgentSummary(cfg, incrementalID, 1, idx, result); wErr != nil {
					slog.Warn("incremental write failed", "agent", idx, "err", wErr)
				}
			}

			if progress != nil {
				mu.Lock()
				completedCount++
				done := int(completedCount)
				mu.Unlock()
				remaining := len(assignments) - done
				progress("l1-summarize", remaining, len(assignments))
			}
		}(i, a)
	}

	wg.Wait()

	// SummarizeAgent now gracefully degrades (never returns error), but handle
	// unexpected errors (e.g. context cancellation) by collecting partial results.
	var failed int
	for i, err := range errs {
		if err != nil {
			failed++
			slog.Error("agent summarization error (unexpected)",
				"agent", i, "err", err)
			results[i] = AgentSummary{
				Level:     1,
				Index:     assignments[i].Index,
				FilePaths: assignments[i].FilePaths,
				Summary:   fmt.Sprintf("[Summary unavailable — %v]", err),
				Tokens:    0,
			}
		}
	}
	if failed > 0 {
		slog.Warn("summarization completed with partial failures",
			"total", len(assignments), "failed", failed)
	}

	return results, nil
}

func countLines(s string) int {
	n := 1
	for _, c := range s {
		if c == '\n' {
			n++
		}
	}
	return n
}
