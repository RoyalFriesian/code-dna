package knowledge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Service Detection
// ---------------------------------------------------------------------------

// commonServiceRoots lists directory names that typically contain one
// sub-directory per deployable service.
var commonServiceRoots = []string{
	"cmd", "services", "apps", "deployments", "functions", "lambdas",
	"microservices", "workers", "jobs", "servers",
}

// DetectServices inspects the repository tree and returns a slice of
// ServiceInfo, one per detected deployable unit.
//
// Detection strategy (in priority order):
//  1. Any of the commonServiceRoots directories found at the repo root whose
//     immediate children each contain at least one source file.
//  2. A single service representing the whole repository when no multi-service
//     layout is found.
func DetectServices(repoPath string, tree []TreeNode) []ServiceInfo {
	abs, _ := filepath.Abs(repoPath)

	// Build a flat set of relative file paths for fast lookup.
	allFiles := flattenFiles(tree)

	// Check each common service-root directory.
	for _, rootName := range commonServiceRoots {
		rootAbs := filepath.Join(abs, rootName)
		entries, err := os.ReadDir(rootAbs)
		if err != nil {
			continue
		}

		var services []ServiceInfo
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			// Collect files under this service directory.
			prefix := rootName + "/" + e.Name() + "/"
			var svcFiles []string
			for _, f := range allFiles {
				if strings.HasPrefix(f, prefix) {
					svcFiles = append(svcFiles, f)
				}
			}
			if len(svcFiles) == 0 {
				continue
			}
			entry := ServiceInfo{
				Name:     sanitizeName(e.Name()),
				RootPath: filepath.Join(rootAbs, e.Name()),
				Files:    svcFiles,
			}
			// Detect entry point.
			for _, f := range svcFiles {
				if strings.HasSuffix(f, "main.go") ||
					strings.HasSuffix(f, "main.py") ||
					strings.HasSuffix(f, "index.ts") ||
					strings.HasSuffix(f, "index.js") ||
					strings.HasSuffix(f, "server.go") {
					entry.EntryPoint = f
					break
				}
			}
			services = append(services, entry)
		}

		if len(services) > 1 {
			// Found a proper multi-service layout.
			sort.Slice(services, func(i, j int) bool {
				return services[i].Name < services[j].Name
			})
			return services
		}
	}

	// Fall back to a single service spanning the entire repo.
	name := filepath.Base(abs)
	return []ServiceInfo{
		{
			Name:     sanitizeName(name),
			RootPath: abs,
			Files:    allFiles,
		},
	}
}

// ---------------------------------------------------------------------------
// Service Document Generation
// ---------------------------------------------------------------------------

const serviceDocSystemPrompt = `You are a senior software architect writing a comprehensive, human-readable product document for a software service.

You will receive:
- SERVICE NAME: the name of the service
- MASTER CONTEXT: the high-level architectural overview of the entire repository  
- SERVICE FILES: a list of source files belonging to this service
- RELEVANT SUMMARIES: detailed code-level summaries of the service's files

Write a well-structured Markdown document with the following sections.  
Use GitHub-Flavoured Markdown.  Use Mermaid for all diagrams.

---

# <Service Name>

## Overview
2–4 sentences describing what this service does, who uses it, and why it exists.
Include the primary responsibilities and the business value it provides.

## Architecture

Provide a high-level component diagram.

` + "```mermaid" + `
graph TD
    ...
` + "```" + `

Nodes should represent the major internal components (packages, handlers, stores, queues, etc.).
Edges should show data/control flow between them.
Label edges with the protocol or action (HTTP, SQL, gRPC, pub/sub, etc.).

## Data Flow

Show a typical end-to-end request or job flow as a sequence diagram.

` + "```mermaid" + `
sequenceDiagram
    participant Client
    ...
` + "```" + `

## Key Components

A table listing the major internal packages/modules/classes with a one-line description each.

| Component | Location | Responsibility |
|-----------|----------|----------------|
| ...       | ...      | ...            |

## API / Interfaces

List every public-facing interface: HTTP endpoints, gRPC methods, CLI flags, event topics, or exported library functions.
For HTTP endpoints include method, path, and brief description.
For CLI tools include the command signature and flags.

## Dependencies

External dependencies: databases, caches, message brokers, other services, third-party APIs.

| Dependency | Type | Purpose |
|-----------|------|---------|
| ...       | ...  | ...     |

## Configuration

Key environment variables or configuration options this service requires.

| Variable | Default | Description |
|----------|---------|-------------|
| ...      | ...     | ...         |

## Deployment Notes

Any important notes about how this service is deployed, scaled, or operated.

---

Be precise and technical. Prefer concrete facts from the summaries over generic statements.
Do NOT invent information that is not present in the provided context.`

// GenerateServiceDocs detects services in the indexed repository and creates
// one Markdown architecture document per service, stored under the knowledge
// base directory.
func GenerateServiceDocs(
	ctx context.Context,
	client CompletionClient,
	repoPath string,
	cfg Config,
	manifest Manifest,
	progress ProgressFunc,
) ([]ServiceDoc, error) {
	repoID := manifest.Repo.ID

	// Load tree.
	tree, err := ReadTree(cfg, repoID)
	if err != nil {
		return nil, fmt.Errorf("read tree: %w", err)
	}

	// Detect services.
	services := DetectServices(repoPath, tree)

	// Load master context.
	masterContext, err := ReadMasterContext(cfg, repoID, manifest)
	if err != nil {
		return nil, fmt.Errorf("read master context: %w", err)
	}

	// Load all L1 summaries.
	l1Summaries, err := ReadAgentSummaries(cfg, repoID, 1)
	if err != nil {
		// Non-fatal: proceed without L1 detail.
		l1Summaries = nil
	}

	var docs []ServiceDoc

	for i, svc := range services {
		if progress != nil {
			progress("service-doc:"+svc.Name, i+1, len(services))
		}

		// Collect relevant L1 summaries (those covering files in this service).
		svcFileSet := make(map[string]bool, len(svc.Files))
		for _, f := range svc.Files {
			svcFileSet[f] = true
		}
		var relevantSummaries []string
		for _, s := range l1Summaries {
			for _, fp := range s.FilePaths {
				rel := relPath(repoPath, fp)
				if svcFileSet[rel] || svcFileSet[fp] {
					relevantSummaries = append(relevantSummaries, s.Summary)
					break
				}
			}
		}

		userPrompt := buildServiceDocPrompt(svc, masterContext, relevantSummaries)

		content, err := client.Generate(ctx, cfg.GetIndexModel(), serviceDocSystemPrompt, userPrompt)
		if err != nil {
			// Log and skip this service rather than aborting the whole pipeline.
			fmt.Fprintf(os.Stderr, "service-doc %s: %v\n", svc.Name, err)
			continue
		}

		doc := ServiceDoc{
			ServiceName: svc.Name,
			Content:     content,
			GeneratedAt: time.Now().UTC(),
		}
		if err := WriteServiceDoc(cfg, repoID, doc); err != nil {
			return nil, fmt.Errorf("write service doc %s: %w", svc.Name, err)
		}
		docs = append(docs, doc)
	}

	return docs, nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

func buildServiceDocPrompt(svc ServiceInfo, masterCtx string, summaries []string) string {
	var sb strings.Builder
	sb.WriteString("SERVICE NAME: ")
	sb.WriteString(svc.Name)
	sb.WriteString("\nENTRY POINT: ")
	if svc.EntryPoint != "" {
		sb.WriteString(svc.EntryPoint)
	} else {
		sb.WriteString("(not detected)")
	}
	sb.WriteString("\n\nSERVICE FILES:\n")
	for _, f := range svc.Files {
		sb.WriteString("  - ")
		sb.WriteString(f)
		sb.WriteString("\n")
	}
	sb.WriteString("\nMASTER CONTEXT (full repository overview):\n")
	sb.WriteString(masterCtx)

	if len(summaries) > 0 {
		sb.WriteString("\n\nRELEVANT L1 SUMMARIES (code-level detail for this service):\n\n")
		for i, s := range summaries {
			sb.WriteString(fmt.Sprintf("--- Summary %d ---\n%s\n\n", i+1, s))
		}
	}
	return sb.String()
}

func flattenFiles(nodes []TreeNode) []string {
	var out []string
	for _, n := range nodes {
		if n.Type == "file" {
			out = append(out, n.Path)
		}
		out = append(out, flattenFiles(n.Children)...)
	}
	return out
}

func sanitizeName(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "_", "-")
	return s
}

func relPath(base, target string) string {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return target
	}
	return rel
}
