package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/RoyalFriesian/code-dna/pkg/knowledge"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// --- index_repo ---

type IndexRepoInput struct {
	Path  string `json:"path" jsonschema:"Absolute or relative path to the repository to index"`
	Force bool   `json:"force,omitempty" jsonschema:"Force full re-index even if already indexed"`
	Deep  bool   `json:"deep,omitempty" jsonschema:"Deep index mode: include all files including dependencies, generated code, and vendored packages. Default (false) indexes only manually written source code."`
}

type IndexRepoOutput struct {
	RepoID    string `json:"repoId"`
	Status    string `json:"status"`
	FileCount int    `json:"fileCount"`
	Levels    int    `json:"levels"`
	Tokens    int    `json:"totalTokens"`
}

// --- query_repo ---

type QueryRepoInput struct {
	Path     string `json:"path" jsonschema:"Path to the repository to query (must be already indexed)"`
	Question string `json:"question" jsonschema:"Natural language question about the codebase"`
}

type QueryRepoOutput struct {
	Answer  string             `json:"answer"`
	Sources []knowledge.Source `json:"sources,omitempty"`
}

// --- list_repos ---

type ListReposInput struct{}

type ListReposOutput struct {
	Repos []RepoInfo `json:"repos"`
}

type RepoInfo struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	Status    string `json:"status"`
	FileCount int    `json:"fileCount"`
	Levels    int    `json:"levels"`
	Tokens    int    `json:"totalTokens"`
	Model     string `json:"model"`
}

// --- reindex_repo ---

type ReindexRepoInput struct {
	Path string `json:"path" jsonschema:"Path to the repository to re-index incrementally"`
}

type ReindexRepoOutput struct {
	RepoID       string `json:"repoId"`
	Status       string `json:"status"`
	ChangedFiles int    `json:"changedFiles"`
	FileCount    int    `json:"fileCount"`
	Levels       int    `json:"levels"`
}

func registerTools(server *mcp.Server, cfg knowledge.Config, llm knowledge.CompletionClient) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "index_repo",
		Description: "Index a code repository into a compressed hierarchical knowledge base. This may take several minutes for large repos.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:   false,
			IdempotentHint: true,
			OpenWorldHint:  boolPtr(false),
		},
	}, indexRepoHandler(cfg, llm))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "query_repo",
		Description: "Answer a question about an indexed repository using hierarchical knowledge resolution. The repo must be indexed first with index_repo.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, queryRepoHandler(cfg, llm))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_repos",
		Description: "List all indexed repositories and their status.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, listReposHandler(cfg))

	mcp.AddTool(server, &mcp.Tool{
		Name:        "reindex_repo",
		Description: "Incrementally re-index a repository, detecting changed files and updating the knowledge base.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:   false,
			IdempotentHint: true,
			OpenWorldHint:  boolPtr(false),
		},
	}, reindexRepoHandler(cfg, llm))
}

func indexRepoHandler(cfg knowledge.Config, llm knowledge.CompletionClient) mcp.ToolHandlerFor[IndexRepoInput, IndexRepoOutput] {
	return func(ctx context.Context, req *mcp.CallToolRequest, input IndexRepoInput) (*mcp.CallToolResult, IndexRepoOutput, error) {
		if input.Path == "" {
			return nil, IndexRepoOutput{}, fmt.Errorf("path is required")
		}

		// Apply deep mode if requested
		indexCfg := cfg
		if input.Deep {
			indexCfg.ScanMode = knowledge.ScanModeDeep
		}

		// Check if already indexed and not forcing
		if !input.Force {
			if m, found, _ := knowledge.FindRepoByPath(indexCfg, input.Path); found && m.Repo.Status == "ready" {
				out := IndexRepoOutput{
					RepoID:    m.Repo.ID,
					Status:    "already_indexed",
					FileCount: m.Repo.FileCount,
					Levels:    m.Repo.LevelsCount,
					Tokens:    m.Repo.TotalTokens,
				}
				text := fmt.Sprintf("Repository already indexed (ID: %s, %d files, %d levels). Use force=true to re-index.",
					m.Repo.ID, m.Repo.FileCount, m.Repo.LevelsCount)
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, out, nil
			}
		}

		var stages []string
		progress := func(stage string, current, total int) {
			msg := stage
			if total > 0 {
				msg = fmt.Sprintf("%s (%d/%d)", stage, current, total)
			}
			stages = append(stages, msg)
		}

		manifest, err := knowledge.IndexRepo(ctx, llm, input.Path, indexCfg, progress)
		if err != nil {
			return nil, IndexRepoOutput{}, fmt.Errorf("indexing failed: %w", err)
		}

		out := IndexRepoOutput{
			RepoID:    manifest.Repo.ID,
			Status:    manifest.Repo.Status,
			FileCount: manifest.Repo.FileCount,
			Levels:    manifest.Repo.LevelsCount,
			Tokens:    manifest.Repo.TotalTokens,
		}

		text := fmt.Sprintf("Successfully indexed repository.\nRepo ID: %s\nFiles: %d\nLevels: %d\nTotal tokens: %d\nStages: %s",
			manifest.Repo.ID, manifest.Repo.FileCount, manifest.Repo.LevelsCount,
			manifest.Repo.TotalTokens, strings.Join(stages, " → "))

		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, out, nil
	}
}

func queryRepoHandler(cfg knowledge.Config, llm knowledge.CompletionClient) mcp.ToolHandlerFor[QueryRepoInput, QueryRepoOutput] {
	return func(ctx context.Context, req *mcp.CallToolRequest, input QueryRepoInput) (*mcp.CallToolResult, QueryRepoOutput, error) {
		if input.Path == "" {
			return nil, QueryRepoOutput{}, fmt.Errorf("path is required")
		}
		if input.Question == "" {
			return nil, QueryRepoOutput{}, fmt.Errorf("question is required")
		}

		manifest, found, err := knowledge.FindRepoByPath(cfg, input.Path)
		if err != nil {
			return nil, QueryRepoOutput{}, fmt.Errorf("lookup repo: %w", err)
		}
		if !found {
			return nil, QueryRepoOutput{}, fmt.Errorf("repository not indexed. Run index_repo first with path: %s", input.Path)
		}
		if manifest.Repo.Status != "ready" {
			return nil, QueryRepoOutput{}, fmt.Errorf("repository index status is '%s', not ready", manifest.Repo.Status)
		}

		resolverFn := knowledge.ResolveQuery(ctx, llm, cfg, manifest)
		result, err := resolverFn(ctx, input.Question)
		if err != nil {
			return nil, QueryRepoOutput{}, fmt.Errorf("query failed: %w", err)
		}

		out := QueryRepoOutput{
			Answer:  result.Answer,
			Sources: result.Sources,
		}

		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: result.Answer}}}, out, nil
	}
}

func listReposHandler(cfg knowledge.Config) mcp.ToolHandlerFor[ListReposInput, ListReposOutput] {
	return func(ctx context.Context, req *mcp.CallToolRequest, input ListReposInput) (*mcp.CallToolResult, ListReposOutput, error) {
		manifests, err := knowledge.ListRepos(cfg)
		if err != nil {
			return nil, ListReposOutput{}, fmt.Errorf("list repos: %w", err)
		}

		if len(manifests) == 0 {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "No repositories indexed yet."}}},
				ListReposOutput{}, nil
		}

		var repos []RepoInfo
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Indexed repositories (%d):\n\n", len(manifests)))

		for _, m := range manifests {
			repos = append(repos, RepoInfo{
				ID:        m.Repo.ID,
				Path:      m.Repo.Path,
				Status:    m.Repo.Status,
				FileCount: m.Repo.FileCount,
				Levels:    m.Repo.LevelsCount,
				Tokens:    m.Repo.TotalTokens,
				Model:     m.Repo.Model,
			})
			sb.WriteString(fmt.Sprintf("- %s (ID: %s)\n  Status: %s | Files: %d | Levels: %d | Tokens: %d\n",
				m.Repo.Path, m.Repo.ID, m.Repo.Status, m.Repo.FileCount, m.Repo.LevelsCount, m.Repo.TotalTokens))
		}

		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: sb.String()}}},
			ListReposOutput{Repos: repos}, nil
	}
}

func reindexRepoHandler(cfg knowledge.Config, llm knowledge.CompletionClient) mcp.ToolHandlerFor[ReindexRepoInput, ReindexRepoOutput] {
	return func(ctx context.Context, req *mcp.CallToolRequest, input ReindexRepoInput) (*mcp.CallToolResult, ReindexRepoOutput, error) {
		if input.Path == "" {
			return nil, ReindexRepoOutput{}, fmt.Errorf("path is required")
		}

		progress := func(stage string, current, total int) {
			// Progress tracking for reindex
		}

		manifest, changedFiles, err := knowledge.ReindexRepo(ctx, llm, input.Path, cfg, progress)
		if err != nil {
			return nil, ReindexRepoOutput{}, fmt.Errorf("reindex failed: %w", err)
		}

		out := ReindexRepoOutput{
			RepoID:       manifest.Repo.ID,
			Status:       manifest.Repo.Status,
			ChangedFiles: changedFiles,
			FileCount:    manifest.Repo.FileCount,
			Levels:       manifest.Repo.LevelsCount,
		}

		var text string
		if changedFiles == 0 {
			text = fmt.Sprintf("No changes detected in %s. Knowledge base is up to date.", input.Path)
		} else {
			text = fmt.Sprintf("Re-indexed %s. Changed files: %d, Total files: %d, Levels: %d",
				input.Path, changedFiles, manifest.Repo.FileCount, manifest.Repo.LevelsCount)
		}

		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, out, nil
	}
}

func boolPtr(b bool) *bool { return &b }
