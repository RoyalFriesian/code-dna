package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const resolverSystemPrompt = `You are a knowledge query resolver. You have access to a hierarchical compressed knowledge base of a repository.

Current context level: %s

Your task: Given the user's question and the knowledge context provided, either:
1. ANSWER the question directly if you have enough detail with exact counts, timestamps, and evidence, OR
2. REQUEST deeper detail by specifying which groups/agents to drill into

IMPORTANT RULES for deciding between answering and drilling down:
- If the question asks for EXACT counts, specific timestamps, or cross-file correlations, and you only have high-level summaries without that data, you MUST request drill-down. Do NOT guess or say "check the files yourself."
- ALWAYS prefer drilling down to get more data over giving a vague or incomplete answer.
- Only answer directly when you have the SPECIFIC data needed to give a precise, evidence-backed answer.
- If you're at the master context level and the question requires per-file details, ALWAYS drill down.

When answering:
- Be precise and reference specific files, types, functions, and line numbers when available
- Cite evidence from the summaries
- Show your reasoning step by step
- If the answer requires cross-file timestamp matching, list each relevant timestamp from each file and explain the matches

When requesting drill-down, respond with EXACTLY this JSON format and nothing else:
{"drillDown": [0, 3, 7]}
where the numbers are the agent/group indices you need more detail from.

CRITICAL: Do NOT mix an answer with a drill-down request. Either respond with ONLY the JSON drill-down request, or respond with ONLY a complete answer.
When in doubt, drill down. A drill-down request is always better than a vague answer.`

const finalAnswerSystemPrompt = `You are a knowledge expert. Answer the user's question using the repository knowledge provided.

Rules:
- Be precise: reference specific files, types, functions, and line numbers
- Structure your answer clearly
- If the question asks about implementation: describe the actual code flow
- If the question asks about architecture: describe packages, interfaces, and data flow
- If the question asks about data/events: provide exact counts, timestamps, and cross-file correlations
- Show your work: list each piece of evidence that contributes to the answer
- If you're uncertain about something, say so

CRITICAL for data/event questions:
- If the answer requires counting events across multiple files, DO THE COUNTING. List each relevant event with its file, line number, and timestamp, then compute the total.
- If timestamps need to be matched across files, DO THE MATCHING. List the timestamps from each file side by side and explain each match.
- You have been given detailed per-line summaries from the indexed knowledge — use them to compute the answer, do not defer to "check the raw files."
- If the knowledge context includes per-entry details with timestamps, use those timestamps to perform cross-file correlation.
- Example: If Player A bounced at 12:06 and Player B caught at 12:07 (1 minute later per rules), that is a match.

Always include a "Sources" section at the end listing the files referenced.
If any ambiguity remains after using all available indexed data, include a "File References" section with specific files, line ranges, and search keywords for final verification.`

// ResolveQuery performs multi-level knowledge resolution to answer a question.
func ResolveQuery(ctx context.Context, client CompletionClient,
	cfg Config, manifest Manifest) func(ctx context.Context, question string) (*QueryResult, error) {

	return func(ctx context.Context, question string) (*QueryResult, error) {
		repoID := manifest.Repo.ID
		queryModel := cfg.GetQueryModel()

		// Load master context
		masterContent, err := ReadMasterContext(cfg, repoID, manifest)
		if err != nil {
			return nil, fmt.Errorf("read master context: %w", err)
		}

		// Build agent routing index so the LLM knows which agents cover which files
		agentIndex := buildAgentRoutingIndex(cfg, repoID)

		// Try answering from master context first
		systemPrompt := fmt.Sprintf(resolverSystemPrompt, "master")
		userPrompt := fmt.Sprintf("Repository: %s\n\nMaster Context:\n%s\n\n%s\n\nQuestion: %s",
			manifest.Repo.Path, masterContent, agentIndex, question)

		response, err := client.Generate(ctx, queryModel, systemPrompt, userPrompt)
		if err != nil {
			return nil, fmt.Errorf("master query: %w", err)
		}

		// Check if the response is a drill-down request
		drillDown := parseDrillDown(response)
		if drillDown == nil {
			// Master context was sufficient
			return parseAnswer(response, manifest), nil
		}

		// Multi-level drill-down
		return drillDownResolve(ctx, client, cfg, manifest, question, masterContent, drillDown)
	}
}

func drillDownResolve(ctx context.Context, client CompletionClient,
	cfg Config, manifest Manifest, question string, masterContent string, targetIndices []int) (*QueryResult, error) {

	repoID := manifest.Repo.ID
	maxLevel := manifest.Repo.LevelsCount
	queryModel := cfg.GetQueryModel()
	reasoningModel := cfg.GetReasoningModel()

	// Collect context from each level, drilling down
	var accumulated strings.Builder
	accumulated.WriteString("Master Context:\n")
	accumulated.WriteString(masterContent)
	accumulated.WriteString("\n\n")

	// Walk down from the highest numbered non-master level to L1
	for level := maxLevel - 1; level >= 1; level-- {
		summaries, err := ReadAgentSummaries(cfg, repoID, level)
		if err != nil {
			break // level doesn't exist
		}

		// Pick the targeted summaries
		var picked []AgentSummary
		for _, idx := range targetIndices {
			if idx >= 0 && idx < len(summaries) {
				picked = append(picked, summaries[idx])
			}
		}
		if len(picked) == 0 {
			break
		}

		// Add picked summaries to accumulated context
		accumulated.WriteString(fmt.Sprintf("\n--- Level %d Detail ---\n", level))
		for _, s := range picked {
			accumulated.WriteString(fmt.Sprintf("\nAgent %d", s.Index))
			if len(s.FilePaths) > 0 {
				accumulated.WriteString(fmt.Sprintf(" (files: %s)", strings.Join(s.FilePaths, ", ")))
			}
			accumulated.WriteString(":\n")
			accumulated.WriteString(s.Summary)
			accumulated.WriteString("\n")
		}

		// Ask if we need to drill deeper
		if level > 1 {
			systemPrompt := fmt.Sprintf(resolverSystemPrompt, fmt.Sprintf("level %d", level))
			drillPrompt := fmt.Sprintf("Repository: %s\n\nAccumulated Context:\n%s\n\nQuestion: %s",
				manifest.Repo.Path, accumulated.String(), question)

			response, err := client.Generate(ctx, queryModel, systemPrompt, drillPrompt)
			if err != nil {
				break
			}

			nextDrill := parseDrillDown(response)
			if nextDrill == nil {
				// Got an answer at this level
				return parseAnswer(response, manifest), nil
			}
			targetIndices = nextDrill
		}
	}

	// Final answer with all accumulated context
	userPrompt := fmt.Sprintf("Repository: %s\n\nFull Knowledge Context:\n%s\n\nQuestion: %s",
		manifest.Repo.Path, accumulated.String(), question)

	response, err := client.Generate(ctx, reasoningModel, finalAnswerSystemPrompt, userPrompt)
	if err != nil {
		return nil, fmt.Errorf("final answer: %w", err)
	}

	return parseAnswer(response, manifest), nil
}

// buildAgentRoutingIndex produces a text directory mapping L1 agent indices to
// the files they cover. This is included in the resolver prompt so the LLM
// can make informed drill-down requests.
func buildAgentRoutingIndex(cfg Config, repoID string) string {
	summaries, err := ReadAgentSummaries(cfg, repoID, 1)
	if err != nil || len(summaries) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Agent Index (use these indices for drill-down requests):\n")
	for _, s := range summaries {
		b.WriteString(fmt.Sprintf("- Agent %d: %s\n", s.Index, strings.Join(s.FilePaths, ", ")))
	}
	return b.String()
}

type drillDownResponse struct {
	DrillDown []int `json:"drillDown"`
}

func parseDrillDown(response string) []int {
	trimmed := strings.TrimSpace(response)

	// Try to parse as drill-down JSON
	var dd drillDownResponse
	if err := json.Unmarshal([]byte(trimmed), &dd); err == nil && len(dd.DrillDown) > 0 {
		return dd.DrillDown
	}

	// Try to find JSON embedded in the response
	start := strings.Index(trimmed, `{"drillDown"`)
	if start >= 0 {
		end := strings.Index(trimmed[start:], "}")
		if end >= 0 {
			candidate := trimmed[start : start+end+1]
			if err := json.Unmarshal([]byte(candidate), &dd); err == nil && len(dd.DrillDown) > 0 {
				return dd.DrillDown
			}
		}
	}

	return nil
}

func parseAnswer(response string, manifest Manifest) *QueryResult {
	result := &QueryResult{
		Answer: response,
	}

	// Extract sources section if present
	lower := strings.ToLower(response)
	idx := strings.LastIndex(lower, "sources")
	if idx > 0 {
		sourcesSection := response[idx:]
		lines := strings.Split(sourcesSection, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			line = strings.TrimPrefix(line, "- ")
			line = strings.TrimPrefix(line, "* ")
			if line == "" || strings.HasPrefix(strings.ToLower(line), "source") {
				continue
			}
			if strings.Contains(line, "/") || strings.Contains(line, ".") {
				result.Sources = append(result.Sources, Source{
					File: line,
				})
			}
		}
	}

	// Extract file references section if present
	refIdx := strings.Index(lower, "file references")
	if refIdx > 0 {
		refSection := response[refIdx:]
		lines := strings.Split(refSection, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			line = strings.TrimPrefix(line, "- ")
			line = strings.TrimPrefix(line, "* ")
			if line == "" || strings.HasPrefix(strings.ToLower(line), "file reference") {
				continue
			}
			// Parse "**File**: X — **Read lines**: Y — **Search for**: Z" patterns
			if strings.Contains(line, "**File**") || (strings.Contains(line, ".") && strings.Contains(strings.ToLower(line), "line")) {
				src := Source{Note: line}
				// Try to extract the file name
				parts := strings.SplitN(line, "—", 4)
				if len(parts) >= 1 {
					filePart := strings.TrimSpace(parts[0])
					filePart = strings.ReplaceAll(filePart, "**File**:", "")
					filePart = strings.ReplaceAll(filePart, "**File**", "")
					filePart = strings.ReplaceAll(filePart, "**", "")
					filePart = strings.TrimSpace(filePart)
					if filePart != "" {
						src.File = filePart
					}
				}
				if len(parts) >= 2 {
					linesPart := strings.TrimSpace(parts[1])
					linesPart = strings.ReplaceAll(linesPart, "**Read lines**:", "")
					linesPart = strings.ReplaceAll(linesPart, "**Read lines**", "")
					linesPart = strings.ReplaceAll(linesPart, "**", "")
					linesPart = strings.TrimSpace(linesPart)
					if linesPart != "" {
						src.Lines = linesPart
					}
				}
				result.Sources = append(result.Sources, src)
			}
		}
	}

	return result
}
