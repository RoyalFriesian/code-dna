package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/RoyalFriesian/code-dna/pkg/knowledge"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func registerResources(server *mcp.Server, cfg knowledge.Config) {
	// Register resource templates for dynamic repo-based resources
	server.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: "knowledge://{repoId}/master-context",
		Name:        "Master Context",
		Description: "The final compressed architectural overview of an indexed repository",
		MIMEType:    "text/markdown",
	}, masterContextHandler(cfg))

	server.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: "knowledge://{repoId}/tree",
		Name:        "Repository Tree",
		Description: "The full directory/file tree of an indexed repository (L0 scan)",
		MIMEType:    "application/json",
	}, treeHandler(cfg))

	server.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: "knowledge://{repoId}/service-doc/{serviceName}",
		Name:        "Service Architecture Document",
		Description: "Human-readable architecture document with Mermaid diagrams for a detected service",
		MIMEType:    "text/markdown",
	}, serviceDocHandler(cfg))

	server.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: "knowledge://{repoId}/agent-guide",
		Name:        "Agent Query Guide",
		Description: "Comprehensive guide for AI agents explaining the knowledge base structure, navigation, best practices, and worked examples",
		MIMEType:    "text/markdown",
	}, agentGuideHandler(cfg))
}

func masterContextHandler(cfg knowledge.Config) mcp.ResourceHandler {
	return func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		repoID, err := extractRepoID(req.Params.URI)
		if err != nil {
			return nil, err
		}

		manifest, err := knowledge.ReadManifest(cfg, repoID)
		if err != nil {
			return nil, fmt.Errorf("repo %s not found: %w", repoID, err)
		}

		content, err := knowledge.ReadMasterContext(cfg, repoID, manifest)
		if err != nil {
			return nil, fmt.Errorf("read master context: %w", err)
		}

		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{
				{
					URI:      req.Params.URI,
					MIMEType: "text/markdown",
					Text:     content,
				},
			},
		}, nil
	}
}

func treeHandler(cfg knowledge.Config) mcp.ResourceHandler {
	return func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		repoID, err := extractRepoID(req.Params.URI)
		if err != nil {
			return nil, err
		}

		tree, err := knowledge.ReadTree(cfg, repoID)
		if err != nil {
			return nil, fmt.Errorf("read tree for %s: %w", repoID, err)
		}

		data, err := json.MarshalIndent(tree, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("marshal tree: %w", err)
		}

		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{
				{
					URI:      req.Params.URI,
					MIMEType: "application/json",
					Text:     string(data),
				},
			},
		}, nil
	}
}

func serviceDocHandler(cfg knowledge.Config) mcp.ResourceHandler {
	return func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		// URI: knowledge://{repoId}/service-doc/{serviceName}
		parts := splitURIParts(req.Params.URI)
		// parts[0]=repoId, parts[1]="service-doc", parts[2]=serviceName
		if len(parts) < 3 {
			return nil, fmt.Errorf("invalid URI: expected knowledge://{repoId}/service-doc/{serviceName}")
		}
		repoID := parts[0]
		serviceName := parts[2]

		doc, err := knowledge.ReadServiceDoc(cfg, repoID, serviceName)
		if err != nil {
			return nil, fmt.Errorf("service doc %q for repo %s not found: %w", serviceName, repoID, err)
		}

		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{
				{
					URI:      req.Params.URI,
					MIMEType: "text/markdown",
					Text:     doc.Content,
				},
			},
		}, nil
	}
}

func agentGuideHandler(cfg knowledge.Config) mcp.ResourceHandler {
	return func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		// URI: knowledge://{repoId}/agent-guide
		parts := splitURIParts(req.Params.URI)
		if len(parts) < 1 {
			return nil, fmt.Errorf("invalid URI: expected knowledge://{repoId}/agent-guide")
		}
		repoID := parts[0]

		guide, err := knowledge.ReadAgentGuide(cfg, repoID)
		if err != nil {
			return nil, fmt.Errorf("agent guide for repo %s not found: %w", repoID, err)
		}

		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{
				{
					URI:      req.Params.URI,
					MIMEType: "text/markdown",
					Text:     guide.Content,
				},
			},
		}, nil
	}
}

// extractRepoID parses the repo ID from a knowledge:// URI.
// Expected format: knowledge://{repoId}/...
func extractRepoID(uri string) (string, error) {
	const prefix = "knowledge://"
	if len(uri) <= len(prefix) {
		return "", fmt.Errorf("invalid knowledge URI: %s", uri)
	}
	path := uri[len(prefix):]
	// First path segment is the repo ID
	base := filepath.Base(filepath.Dir(path))
	if base == "." || base == "" {
		// URI is knowledge://{repoId}/something — get the first segment
		parts := splitPath(path)
		if len(parts) == 0 {
			return "", fmt.Errorf("invalid knowledge URI: %s", uri)
		}
		return parts[0], nil
	}
	return base, nil
}

func splitPath(p string) []string {
	var parts []string
	for _, s := range filepath.SplitList(p) {
		for _, part := range split(s, '/') {
			if part != "" {
				parts = append(parts, part)
			}
		}
	}
	return parts
}

func split(s string, sep byte) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			if i > start {
				parts = append(parts, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		parts = append(parts, s[start:])
	}
	return parts
}

// splitURIParts returns the path segments after "knowledge://" in order.
// e.g. "knowledge://abc123/service-doc/api" → ["abc123","service-doc","api"]
func splitURIParts(uri string) []string {
	const prefix = "knowledge://"
	if len(uri) <= len(prefix) {
		return nil
	}
	path := uri[len(prefix):]
	var parts []string
	for _, p := range split(path, '/') {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return parts
}
