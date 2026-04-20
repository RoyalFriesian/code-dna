package knowledge

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Agent Guide Generation
// ---------------------------------------------------------------------------

// agentGuideSystemPrompt instructs the LLM to write the guide for AI agents.
const agentGuideSystemPrompt = `You are a knowledge architect. 
Your task is to write a comprehensive, accurate guide for AI agents that will query this knowledge base.

The guide must be written in GitHub-Flavoured Markdown and must cover:

1. What the knowledge base is and why it exists
2. How information is physically stored (directory layout, file names)
3. How the hierarchical compression works (levels, agents, master context)
4. How to navigate from high-level to fine-grained detail (drill-down)
5. The right query strategy for different question types
6. Best practices for writing effective queries
7. Concrete worked examples (with realistic questions and the right strategy)
8. Common mistakes and how to avoid them
9. How the agent guide itself and service documents fit in

Be concrete. Use real paths and file names based on the actual knowledge base structure provided.
Use Mermaid diagrams to illustrate the hierarchy and the query flow.
Include at least 5 worked query examples.`

// GenerateAgentGuide creates a comprehensive guide for AI agents that
// explains how the knowledge base is structured and how to query it
// effectively.  The guide is stored as AGENT_GUIDE.md in the repo's KB dir.
func GenerateAgentGuide(
	ctx context.Context,
	client CompletionClient,
	cfg Config,
	manifest Manifest,
	services []ServiceInfo,
	progress ProgressFunc,
) (*AgentGuide, error) {
	if progress != nil {
		progress("agent-guide", 0, 0)
	}

	// Load master context so the guide can reference real content.
	masterContext, err := ReadMasterContext(cfg, manifest.Repo.ID, manifest)
	if err != nil {
		return nil, fmt.Errorf("read master context: %w", err)
	}

	// Load one sample L1 summary so the guide can show its format.
	l1Summaries, _ := ReadAgentSummaries(cfg, manifest.Repo.ID, 1)
	var sampleL1 string
	if len(l1Summaries) > 0 {
		s := l1Summaries[0].Summary
		if len(s) > 2000 {
			s = s[:2000] + "\n...(truncated for brevity)"
		}
		sampleL1 = s
	}

	userPrompt := buildAgentGuidePrompt(cfg, manifest, services, masterContext, sampleL1)

	content, err := client.Generate(ctx, cfg.GetIndexModel(), agentGuideSystemPrompt, userPrompt)
	if err != nil {
		return nil, fmt.Errorf("generate agent guide: %w", err)
	}

	guide := &AgentGuide{
		Content:     content,
		GeneratedAt: time.Now().UTC(),
	}
	if err := WriteAgentGuide(cfg, manifest.Repo.ID, *guide); err != nil {
		return nil, fmt.Errorf("write agent guide: %w", err)
	}
	return guide, nil
}

// buildAgentGuidePrompt assembles the context the LLM needs to write an
// accurate guide.
func buildAgentGuidePrompt(
	cfg Config,
	manifest Manifest,
	services []ServiceInfo,
	masterContext string,
	sampleL1 string,
) string {
	var sb strings.Builder

	// ── Knowledge base facts ──────────────────────────────────────────────
	sb.WriteString("## Knowledge Base Facts\n\n")
	sb.WriteString(fmt.Sprintf("- Repository path: %s\n", manifest.Repo.Path))
	sb.WriteString(fmt.Sprintf("- Repository ID: %s\n", manifest.Repo.ID))
	sb.WriteString(fmt.Sprintf("- Total source files indexed: %d\n", manifest.Repo.FileCount))
	sb.WriteString(fmt.Sprintf("- Total tokens across all summaries: %d\n", manifest.Repo.TotalTokens))
	sb.WriteString(fmt.Sprintf("- Compression levels produced: %d\n", len(manifest.Levels)))
	sb.WriteString(fmt.Sprintf("- LLM model used for indexing: %s\n", manifest.Repo.Model))
	sb.WriteString(fmt.Sprintf("- Knowledge base storage root: %s\n", cfg.BaseDir))
	sb.WriteString(fmt.Sprintf("- Repo knowledge base dir: %s\n", cfg.RepoDir(manifest.Repo.ID)))

	sb.WriteString("\n### Compression Levels\n\n")
	sb.WriteString("| Level | Description | Agent Count | Total Tokens |\n")
	sb.WriteString("|-------|-------------|-------------|-------------|\n")
	for _, l := range manifest.Levels {
		desc := fmt.Sprintf("L%d summaries", l.Number)
		if l.Number == len(manifest.Levels) {
			desc = "Master context (final compressed overview)"
		} else if l.Number == 1 {
			desc = "L1 per-file-group summaries"
		}
		sb.WriteString(fmt.Sprintf("| L%d | %s | %d | %d |\n",
			l.Number, desc, l.AgentCount, l.TotalTokens))
	}

	// ── Physical directory layout ─────────────────────────────────────────
	repoDir := cfg.RepoDir(manifest.Repo.ID)
	sb.WriteString("\n### Physical Directory Layout\n\n")
	sb.WriteString("```\n")
	sb.WriteString(fmt.Sprintf("%s/\n", repoDir))
	sb.WriteString("├── manifest.json          # top-level metadata (repo info, level list)\n")
	sb.WriteString("├── l0/\n")
	sb.WriteString("│   └── tree.json          # full directory/file tree (no content)\n")
	for _, l := range manifest.Levels {
		if l.Number == len(manifest.Levels) {
			sb.WriteString(fmt.Sprintf("├── l%d/\n", l.Number))
			sb.WriteString(fmt.Sprintf("│   └── master.md              # master context (%d tokens)\n", l.TotalTokens))
			sb.WriteString(fmt.Sprintf("│   └── compression-map.json   # routing map for drill-down\n"))
		} else {
			sb.WriteString(fmt.Sprintf("├── l%d/\n", l.Number))
			sb.WriteString(fmt.Sprintf("│   ├── agents/\n"))
			sb.WriteString(fmt.Sprintf("│   │   ├── agent-000.json     # summary for file group 0\n"))
			sb.WriteString(fmt.Sprintf("│   │   ├── agent-001.json     # summary for file group 1\n"))
			sb.WriteString(fmt.Sprintf("│   │   └── ...                # (%d agents total)\n", l.AgentCount))
			sb.WriteString(fmt.Sprintf("│   └── index.json             # filepath → agent index map\n"))
		}
	}
	sb.WriteString("├── service-docs/\n")
	if len(services) > 0 {
		for _, svc := range services {
			sb.WriteString(fmt.Sprintf("│   └── %s.md            # architecture doc for %s service\n",
				svc.Name, svc.Name))
		}
	} else {
		sb.WriteString("│   └── <service-name>.md   # per-service architecture docs\n")
	}
	sb.WriteString("└── AGENT_GUIDE.md             # this file\n")
	sb.WriteString("```\n")

	// ── Detected services ─────────────────────────────────────────────────
	if len(services) > 0 {
		sb.WriteString("\n### Detected Services\n\n")
		for _, svc := range services {
			sb.WriteString(fmt.Sprintf("- **%s** (`%s`)", svc.Name, svc.RootPath))
			if svc.EntryPoint != "" {
				sb.WriteString(fmt.Sprintf(" — entry point: `%s`", svc.EntryPoint))
			}
			sb.WriteString(fmt.Sprintf(" — %d files\n", len(svc.Files)))
		}
	}

	// ── Query interface ───────────────────────────────────────────────────
	sb.WriteString("\n## Query Interface\n\n")
	sb.WriteString("Queries are resolved through `knowledge.ResolveQuery()` which returns a\n")
	sb.WriteString("`func(ctx, question) (*QueryResult, error)`.\n\n")
	sb.WriteString("The resolver performs multi-level drill-down automatically:\n")
	sb.WriteString("1. Loads the master context\n")
	sb.WriteString("2. Asks the LLM whether it can answer or needs to drill down\n")
	sb.WriteString("3. If drill-down is requested, loads the indicated L(n-1) agent summaries\n")
	sb.WriteString("4. Repeats until it has enough detail to produce a precise answer\n\n")

	// ── Master context excerpt ────────────────────────────────────────────
	sb.WriteString("## Master Context Excerpt\n\n")
	sb.WriteString("The master context is the starting point for every query. Here is its beginning:\n\n")
	sb.WriteString("```\n")
	excerpt := masterContext
	if len(excerpt) > 3000 {
		excerpt = excerpt[:3000] + "\n...(truncated)"
	}
	sb.WriteString(excerpt)
	sb.WriteString("\n```\n")

	// ── Sample L1 summary ────────────────────────────────────────────────
	if sampleL1 != "" {
		sb.WriteString("\n## Sample L1 Summary (agent-000)\n\n")
		sb.WriteString("This is the format of a per-file-group summary that the resolver\n")
		sb.WriteString("fetches during drill-down:\n\n")
		sb.WriteString("```\n")
		sb.WriteString(sampleL1)
		sb.WriteString("\n```\n")
	}

	// ── Instruction to the LLM ────────────────────────────────────────────
	sb.WriteString("\n## Your Task\n\n")
	sb.WriteString("Using all of the above, write a comprehensive AGENT_GUIDE.md.\n")
	sb.WriteString("The guide is for AI agents (LLM-based) that will call `ResolveQuery`.\n")
	sb.WriteString("It should be the single document they need to query this KB effectively.\n")
	sb.WriteString("Include concrete Mermaid diagrams and at least 5 worked examples.\n")

	return sb.String()
}
