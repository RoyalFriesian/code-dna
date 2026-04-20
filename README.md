# code-dna

Hierarchical knowledge-base builder for any source-code repository.

`code-dna` scans a repo, groups files into agent-sized batches, produces LLM summaries at increasing levels of compression, and stores a queryable knowledge base on disk. The same index can be surfaced via an MCP server so any AI client can query your codebase without ever sending all files in one shot.

---

## Architecture

```
repo files
    │
    ▼
Scanner          — walks the directory tree, respects .gitignore,
                   filters dependency/generated files (smart mode)
    │
    ▼
Distributor      — groups files into agent assignments by directory
                   proximity and token budget
    │
    ▼
Summarizer (L1)  — concurrent worker pool; each assignment → one
                   LLM call → structured summary
    │
    ▼
Compressor       — multi-level hierarchical compression until the
                   whole repo fits in a single "master context"
    │
    ▼
FileStore        — writes manifest, summaries, and master context
                   to ~/.code-dna/<repoId>/
    │
    ▼
Resolver         — multi-level drill-down query engine; answers
                   questions by loading only the relevant summaries
```

The `pkg/knowledge` package has **zero** internal dependencies — it only needs a `CompletionClient` interface implementation (any OpenAI-compatible model).

---

## Quick start

```bash
# 1. Set your API key
export OPENAI_API_KEY=sk-...

# 2. Index a repository
go run ./cmd/index /path/to/your/repo

# 3. Query it
go run ./cmd/query /path/to/your/repo "How does authentication work?"

# 4. Or start the MCP server (stdio, compatible with Claude Desktop etc.)
go run ./cmd/knowledge-mcp

# HTTP mode (for testing)
go run ./cmd/knowledge-mcp -http :8081
```

---

## MCP tools

| Tool | Description |
|------|-------------|
| `index_repo` | Index a repository (creates / updates the knowledge base) |
| `query_repo` | Answer a natural-language question about an indexed repo |
| `list_repos` | List all indexed repositories |
| `reindex_repo` | Incrementally re-index changed files |

## MCP resources

| URI template | Description |
|---|---|
| `knowledge://{repoId}/master-context` | Final compressed architectural overview (markdown) |
| `knowledge://{repoId}/tree` | Full directory/file tree of the indexed repo (JSON) |

---

## Configuration

All options can be set via environment variables. A `.env` file in the working directory is loaded automatically.

| Variable | Default | Description |
|---|---|---|
| `OPENAI_API_KEY` | _(required)_ | OpenAI API key |
| `KNOWLEDGE_BASE_DIR` | `~/.code-dna` | Directory where the knowledge base is stored |
| `KNOWLEDGE_MODEL` | `gpt-4o-mini` | Default model for all stages |
| `KNOWLEDGE_INDEX_MODEL` | — | Model for L1 summarisation and compression |
| `KNOWLEDGE_QUERY_MODEL` | — | Model for query drill-down decisions |
| `KNOWLEDGE_REASONING_MODEL` | — | Model for final answer generation |
| `KNOWLEDGE_SCAN_MODE` | `smart` | `smart` (skip deps) or `deep` (index everything) |
| `KNOWLEDGE_TARGET_TOKENS` | `80000` | Max tokens for master context |
| `KNOWLEDGE_CONCURRENCY` | `5` | Parallel summarisation workers |

Copy `.env.example` to `.env` and fill in your key:

```bash
cp .env.example .env
```

---

## Project layout

```
pkg/knowledge/      — core library (scanner, distributor, summariser,
                      compressor, indexer, resolver, filestore)
llm/                — minimal OpenAI client (implements CompletionClient)
cmd/
  index/            — CLI: index a repository
  query/            — CLI: query an indexed repository
  knowledge-mcp/    — MCP server exposing tools and resources
```

---

## License

MIT
