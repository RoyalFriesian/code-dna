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
