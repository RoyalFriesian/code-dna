package knowledge

import (
	"context"
	"fmt"
	"strings"
)

const compressSystemPrompt = `You are a knowledge compression agent. You receive multiple summaries from different parts of a repository and must compress them into a single cohesive summary.

Your compressed output MUST:
- Preserve all package/module names and their purposes
- Preserve all key exported symbols and their roles
- Preserve architectural patterns and data flow
- Preserve cross-package dependencies and interfaces
- Group related functionality together
- Remove redundancy across summaries
- Keep the most important technical details

CRITICAL for data/log/event repositories:
- Preserve ALL entity names, IDs, and identifiers with their action counts
- Preserve timestamp ranges per file and globally
- Preserve cross-file relationships and correlation hints
- Preserve exact event counts — do NOT approximate
- Preserve drill-down hints: which files to consult for specific questions
- Preserve record formats and schemas
- Create a CROSS-REFERENCE SECTION showing which files relate to which entities

Rules:
- Keep total output at most %d tokens (approximately %d%% of input)
- Use clear section headings
- Maintain a hierarchical structure
- Do NOT invent information — only compress what is provided
- Highlight key interfaces, data contracts, and cross-file relationships`

const masterContextSystemPrompt = `You are a master knowledge compiler. You receive compressed summaries of an entire repository and must produce the definitive overview.

IMPORTANT: Detect whether this repository is primarily source code, data/logs, documentation, or a mix. Adapt your output accordingly.

## For CODE repositories, include:
1. **Repository Overview**: What this codebase does, its primary purpose, and target users
2. **Architecture**: Major packages/modules, their responsibilities, and how they connect
3. **Key Interfaces & Contracts**: The most important interfaces, data types, and API surfaces
4. **Data Flow**: How data moves through the system (e.g., request lifecycle, event pipeline)
5. **Entry Points**: Main executables, server startup, CLI commands
6. **Configuration**: How the system is configured (env vars, config files, flags)
7. **Dependencies**: External services, databases, APIs, third-party libraries of note
8. **Package Directory**: Brief 1-line description of every package/module

## For DATA / LOG repositories, include:
1. **Repository Overview**: What data this repository contains, its purpose, and domain
2. **Data Schema**: The format and structure of the data files (with examples)
3. **Entity Directory**: Every entity (player, user, device, etc.) with:
   - Which files contain data about that entity
   - Total event/record count per entity
   - Types of actions/events recorded
4. **Rules & Specifications**: Complete summary of any rules, game mechanics, protocols, or specifications files
5. **Cross-File Correlation Guide**: How to find relationships across files:
   - What field to match on (e.g., timestamp, ID, sequence number)
   - Which files need to be read together to answer cross-entity questions
   - Known correlation patterns (e.g., "a bounce in one file corresponds to a catch 1 minute later in another file")
6. **Time Range**: Global time span of the data, per-file if varying
7. **Statistical Summary**: Global counts of each event type across all files
8. **Drill-Down Guide**: A lookup table: "To answer questions about X, consult files Y and Z, search for keyword W"

## For MIXED repositories, include both sections as relevant.

Rules:
- Keep total output under %d tokens
- This will be used as context for answering questions about the repository
- Optimize for an AI agent that needs to answer precise questions with file and line references
- Structure with markdown headings for easy navigation
- Be precise about counts, timestamps, file paths, and entity names
- NEVER approximate counts — preserve exact numbers from the summaries
- Include a Drill-Down Guide section that maps question types to specific files`

// CompressLevel takes agent summaries from level K and produces compressed summaries for level K+1.
// If the total tokens of input summaries fit within targetTokens, it produces the master context instead.
func CompressLevel(ctx context.Context, client CompletionClient, model string,
	summaries []AgentSummary, level int, cfg Config) ([]AgentSummary, bool, error) {

	totalTokens := 0
	for _, s := range summaries {
		totalTokens += s.Tokens
	}

	// If already fits in target window, produce master context
	if totalTokens <= cfg.TargetTokens {
		master, err := produceMasterContext(ctx, client, model, summaries, cfg)
		if err != nil {
			return nil, false, err
		}
		return []AgentSummary{master}, true, nil
	}

	// Group summaries into clusters of ~groupSize
	groupSize := 6
	groups := groupSummaries(summaries, groupSize)

	compressed := make([]AgentSummary, 0, len(groups))
	for i, group := range groups {
		if ctx.Err() != nil {
			return nil, false, ctx.Err()
		}

		result, err := compressGroup(ctx, client, model, group, level+1, i, cfg.CompressionRatio)
		if err != nil {
			return nil, false, fmt.Errorf("compress group %d at level %d: %w", i, level+1, err)
		}
		compressed = append(compressed, result)
	}

	return compressed, false, nil
}

func compressGroup(ctx context.Context, client CompletionClient, model string,
	group []AgentSummary, newLevel int, groupIndex int, ratio float64) (AgentSummary, error) {

	var input strings.Builder
	totalInputTokens := 0
	var childIDs []int

	for _, s := range group {
		input.WriteString(fmt.Sprintf("\n--- Agent %d (Level %d, %d tokens) ---\n", s.Index, s.Level, s.Tokens))
		if len(s.FilePaths) > 0 {
			input.WriteString(fmt.Sprintf("Files: %s\n", strings.Join(s.FilePaths, ", ")))
		}
		input.WriteString(s.Summary)
		input.WriteString("\n")
		totalInputTokens += s.Tokens
		childIDs = append(childIDs, s.Index)
	}

	targetTokens := int(float64(totalInputTokens) * ratio)
	if targetTokens < 200 {
		targetTokens = 200
	}
	pct := int(ratio * 100)

	systemPrompt := fmt.Sprintf(compressSystemPrompt, targetTokens, pct)
	userPrompt := fmt.Sprintf("Compress these %d summaries into a single cohesive summary:\n%s",
		len(group), input.String())

	response, err := client.Generate(ctx, model, systemPrompt, userPrompt)
	if err != nil {
		return AgentSummary{}, err
	}

	return AgentSummary{
		Level:    newLevel,
		Index:    groupIndex,
		GroupIDs: childIDs,
		Summary:  response,
		Tokens:   estimateTokens(int64(len(response))),
	}, nil
}

func produceMasterContext(ctx context.Context, client CompletionClient, model string,
	summaries []AgentSummary, cfg Config) (AgentSummary, error) {

	var input strings.Builder
	for _, s := range summaries {
		input.WriteString(fmt.Sprintf("\n--- Summary %d ---\n", s.Index))
		if len(s.FilePaths) > 0 {
			input.WriteString(fmt.Sprintf("Files: %s\n", strings.Join(s.FilePaths, ", ")))
		}
		input.WriteString(s.Summary)
		input.WriteString("\n")
	}

	systemPrompt := fmt.Sprintf(masterContextSystemPrompt, cfg.TargetTokens)
	userPrompt := fmt.Sprintf("Produce the master architectural context from these %d summaries:\n%s",
		len(summaries), input.String())

	response, err := client.Generate(ctx, model, systemPrompt, userPrompt)
	if err != nil {
		return AgentSummary{}, fmt.Errorf("produce master context: %w", err)
	}

	var allIDs []int
	for _, s := range summaries {
		allIDs = append(allIDs, s.Index)
	}

	return AgentSummary{
		Level:    0, // will be set by caller to actual master level
		Index:    0,
		GroupIDs: allIDs,
		Summary:  response,
		Tokens:   estimateTokens(int64(len(response))),
	}, nil
}

func groupSummaries(summaries []AgentSummary, groupSize int) [][]AgentSummary {
	if groupSize <= 0 {
		groupSize = 6
	}
	var groups [][]AgentSummary
	for i := 0; i < len(summaries); i += groupSize {
		end := i + groupSize
		if end > len(summaries) {
			end = len(summaries)
		}
		groups = append(groups, summaries[i:end])
	}
	return groups
}
