// codedna — unified CLI for the code-dna knowledge base tool.
//
// Subcommands:
//
//	codedna init                      — interactive setup wizard
//	codedna index [--reindex] [path]  — index or incrementally reindex a repo
//	codedna reindex [path]            — shorthand for index --reindex
//	codedna query [path] [question]   — query an indexed repo
//	codedna list                      — list all indexed repos
//	codedna status [path]             — show index status for a repo
//	codedna config                    — show active configuration
//	codedna export [path] [output]    — export master context to a file
//	codedna diff [path]               — show files changed since last index
//	codedna docs [path]               — list generated service docs
//	codedna delete [path]             — remove indexed data for a repo
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/RoyalFriesian/code-dna/llm"
	"github.com/RoyalFriesian/code-dna/pkg/knowledge"
)

func main() {
	loadEnvFile(".env")
	loadEnvFile(filepath.Join(filepath.Dir(os.Args[0]), ".env"))

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(0)
	}

	switch os.Args[1] {
	case "init":
		cmdInit()
	case "index":
		cmdIndex(os.Args[2:])
	case "reindex":
		cmdIndex(append([]string{"--reindex"}, os.Args[2:]...))
	case "query":
		cmdQuery(os.Args[2:])
	case "list":
		cmdList()
	case "status":
		cmdStatus(os.Args[2:])
	case "config":
		cmdConfig()
	case "export":
		cmdExport(os.Args[2:])
	case "diff":
		cmdDiff(os.Args[2:])
	case "docs":
		cmdDocs(os.Args[2:])
	case "delete", "remove":
		cmdDelete(os.Args[2:])
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown subcommand: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

// ── init ─────────────────────────────────────────────────────────────────────

func cmdInit() {
	sc := bufio.NewScanner(os.Stdin)

	fmt.Println("╔══════════════════════════════════════════╗")
	fmt.Println("║       code-dna  ·  setup wizard          ║")
	fmt.Println("╚══════════════════════════════════════════╝")
	fmt.Println()

	saved, _ := knowledge.LoadSavedConfig()

	// ── Storage directory ──────────────────────────────────────────────────
	defaultDir := saved.BaseDir
	if defaultDir == "" {
		defaultDir = filepath.Join(homeDir(), ".code-dna")
	}
	fmt.Printf("Where should code-dna store indexed knowledge bases?\n")
	fmt.Printf("  Press Enter to use the default: %s\n", defaultDir)
	fmt.Printf("  Or enter a custom path: ")
	os.Stdout.Sync()
	sc.Scan()
	baseDir := strings.TrimSpace(sc.Text())
	if baseDir == "" {
		baseDir = defaultDir
	}
	baseDir, _ = filepath.Abs(baseDir)
	fmt.Printf("  ✓ Storage: %s\n\n", baseDir)

	// ── LLM provider ───────────────────────────────────────────────────────
	defaultProvider := saved.LLMProvider
	if defaultProvider == "" {
		defaultProvider = "openai"
	}
	fmt.Println("Which LLM provider do you want to use?")
	fmt.Println("  1) openai  — cloud API (requires OPENAI_API_KEY)")
	fmt.Println("  2) ollama  — local / on-prem (requires Ollama running)")
	fmt.Printf("  [%s]: ", defaultProvider)
	os.Stdout.Sync()
	sc.Scan()
	providerInput := strings.TrimSpace(sc.Text())

	var provider string
	switch providerInput {
	case "1", "openai", "":
		provider = "openai"
	case "2", "ollama":
		provider = "ollama"
	default:
		if providerInput == defaultProvider {
			provider = defaultProvider
		} else {
			fmt.Fprintf(os.Stderr, "Unrecognised choice %q — defaulting to %s\n", providerInput, defaultProvider)
			provider = defaultProvider
		}
	}
	fmt.Printf("  ✓ Provider: %s\n\n", provider)

	newCfg := knowledge.SavedConfig{
		BaseDir:     baseDir,
		LLMProvider: provider,
	}

	// ── Provider-specific settings ─────────────────────────────────────────
	switch provider {
	case "openai":
		defaultKey := saved.APIKey
		if defaultKey == "" {
			defaultKey = os.Getenv("OPENAI_API_KEY")
		}
		masked := ""
		if defaultKey != "" {
			masked = defaultKey[:min(8, len(defaultKey))] + "…"
		}
		fmt.Print("OpenAI API key")
		if masked != "" {
			fmt.Printf(" [%s]", masked)
		}
		fmt.Print(": ")
		os.Stdout.Sync()
		sc.Scan()
		key := strings.TrimSpace(sc.Text())
		if key == "" {
			key = defaultKey
		}
		newCfg.APIKey = key

		defaultModel := saved.Model
		if defaultModel == "" {
			defaultModel = "gpt-4o-mini"
		}
		fmt.Printf("Default model [%s]: ", defaultModel)
		os.Stdout.Sync()
		sc.Scan()
		model := strings.TrimSpace(sc.Text())
		if model == "" {
			model = defaultModel
		}
		newCfg.Model = model
		fmt.Println()

	case "ollama":
		defaultURL := saved.OllamaURL
		if defaultURL == "" {
			defaultURL = os.Getenv("OLLAMA_URL")
		}
		if defaultURL == "" {
			defaultURL = "http://localhost:11434"
		}
		fmt.Printf("Ollama base URL [%s]: ", defaultURL)
		os.Stdout.Sync()
		sc.Scan()
		ollamaURL := strings.TrimSpace(sc.Text())
		if ollamaURL == "" {
			ollamaURL = defaultURL
		}
		newCfg.OllamaURL = ollamaURL

		defaultModel := saved.OllamaModel
		if defaultModel == "" {
			defaultModel = os.Getenv("OLLAMA_MODEL")
		}
		if defaultModel == "" {
			defaultModel = os.Getenv("OLLAMA_CHILD_MODEL")
		}
		if defaultModel == "" {
			defaultModel = "gemma4:e2b"
		}
		fmt.Printf("Ollama model [%s]: ", defaultModel)
		os.Stdout.Sync()
		sc.Scan()
		ollamaModel := strings.TrimSpace(sc.Text())
		if ollamaModel == "" {
			ollamaModel = defaultModel
		}
		newCfg.OllamaModel = ollamaModel
		fmt.Println()
	}

	if err := knowledge.SaveConfig(newCfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving config: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ Config saved to %s/config.json\n\n", newCfg.BaseDir)
	fmt.Println("You're all set! Next steps:")
	fmt.Println("  codedna index .          — index the current repo")
	fmt.Println("  codedna query . 'why?'   — ask a question")
	fmt.Println("  codedna list             — see all indexed repos")
}

// ── index ─────────────────────────────────────────────────────────────────────

func cmdIndex(args []string) {
	reindex := false
	var filteredArgs []string
	for _, a := range args {
		if a == "-reindex" || a == "--reindex" {
			reindex = true
		} else {
			filteredArgs = append(filteredArgs, a)
		}
	}

	target := "."
	if len(filteredArgs) > 0 {
		target = filteredArgs[0]
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		fatalf("resolve path: %v", err)
	}

	cfg := loadConfig()
	client := buildClient(cfg)

	t0 := time.Now()
	progress := func(stage string, current, total int) {
		elapsed := time.Since(t0).Round(time.Second)
		if total > 0 {
			fmt.Printf("  [%s] %-20s %d/%d\n", elapsed, stage, current, total)
		} else {
			fmt.Printf("  [%s] %s\n", elapsed, stage)
		}
	}

	if reindex {
		fmt.Printf("Incremental reindex: %s\n", abs)
		manifest, changed, err := knowledge.ReindexRepo(context.Background(), client, abs, cfg, progress)
		if err != nil {
			fatalf("reindex: %v", err)
		}
		printManifest(manifest, time.Since(t0))
		fmt.Printf("  changed files: %d\n", changed)
	} else {
		fmt.Printf("Indexing: %s\n", abs)
		manifest, err := knowledge.IndexRepo(context.Background(), client, abs, cfg, progress)
		if err != nil {
			fatalf("index: %v", err)
		}
		printManifest(manifest, time.Since(t0))
	}
}

// ── query ─────────────────────────────────────────────────────────────────────

func cmdQuery(args []string) {
	target := "."
	if len(args) > 0 {
		target = args[0]
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		fatalf("resolve path: %v", err)
	}

	// Look up the repo first (no credentials needed for the lookup).
	baseCfg := loadConfigQuiet()
	manifest, found, err := knowledge.FindRepoByPath(baseCfg, abs)
	if err != nil {
		fatalf("lookup: %v", err)
	}
	if !found {
		fatalf("repo %s is not indexed — run `codedna index %s` first", abs, target)
	}

	// Now validate credentials and build the LLM client.
	cfg := loadConfig()

	fmt.Printf("Knowledge base: %s  (%d files, %d levels, model: %s)\n\n",
		manifest.Repo.ID, manifest.Repo.FileCount, manifest.Repo.LevelsCount, manifest.Repo.Model)

	client := buildClient(cfg)
	queryFn := knowledge.ResolveQuery(context.Background(), client, cfg, manifest)

	// Inline question from args.
	if len(args) > 1 {
		runQuery(queryFn, strings.Join(args[1:], " "))
		return
	}

	// Interactive loop.
	sc := bufio.NewScanner(os.Stdin)
	fmt.Println("Enter your questions (empty line or Ctrl-D to quit):")
	for {
		fmt.Print("\nQ: ")
		os.Stdout.Sync()
		if !sc.Scan() {
			break
		}
		q := strings.TrimSpace(sc.Text())
		if q == "" {
			break
		}
		runQuery(queryFn, q)
	}
}

func runQuery(queryFn func(context.Context, string) (*knowledge.QueryResult, error), question string) {
	t0 := time.Now()
	result, err := queryFn(context.Background(), question)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	fmt.Printf("\nAnswer (%s):\n%s\n", time.Since(t0).Round(time.Millisecond), result.Answer)
	if len(result.Sources) > 0 {
		fmt.Println("\nSources:")
		for _, s := range result.Sources {
			fmt.Printf("  · %s\n", s.File)
		}
	}
}

// ── list ──────────────────────────────────────────────────────────────────────

func cmdList() {
	cfg := loadConfigQuiet()
	manifests, err := knowledge.ListRepos(cfg)
	if err != nil {
		fatalf("list: %v", err)
	}
	if len(manifests) == 0 {
		fmt.Println("No indexed repos found in", cfg.BaseDir)
		fmt.Println("Run `codedna index <path>` to index a repo.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATUS\tFILES\tTOKENS\tMODEL\tPATH")
	fmt.Fprintln(w, "──────────────\t──────\t─────\t──────\t──────────────\t────")
	for _, m := range manifests {
		fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%s\t%s\n",
			m.Repo.ID, m.Repo.Status,
			m.Repo.FileCount, m.Repo.TotalTokens,
			m.Repo.Model, m.Repo.Path)
	}
	w.Flush()
}

// ── config ───────────────────────────────────────────────────────────────────

func cmdConfig() {
	saved, err := knowledge.LoadSavedConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not read saved config: %v\n", err)
	}
	cfg := knowledge.ConfigFromEnv()
	cfg = knowledge.ApplySavedConfig(cfg, saved)

	fmt.Println("Active configuration:")
	fmt.Println()

	configPath := ""
	if p, err := knowledge.ActiveConfigPath(); err == nil {
		configPath = p
	}
	if configPath != "" {
		fmt.Printf("  Config file:  %s\n", configPath)
	} else {
		fmt.Printf("  Config file:  (none — using env vars or defaults)\n")
	}
	fmt.Printf("  Storage dir:  %s\n", cfg.BaseDir)
	fmt.Println()

	provider := cfg.LLMProvider
	if provider == "" {
		provider = "openai (default)"
	}
	fmt.Printf("  Provider:     %s\n", provider)

	switch llm.Provider(cfg.LLMProvider) {
	case llm.ProviderOllama:
		fmt.Printf("  Ollama URL:   %s\n", cfg.OllamaURL)
		fmt.Printf("  Ollama model: %s\n", cfg.OllamaModel)
	default:
		masked := ""
		if k := cfg.APIKey; k != "" {
			masked = k[:min(8, len(k))] + "…"
		}
		if masked == "" {
			masked = "(not set)"
		}
		fmt.Printf("  API key:      %s\n", masked)
		fmt.Printf("  Index model:  %s\n", cfg.GetIndexModel())
		fmt.Printf("  Query model:  %s\n", cfg.GetQueryModel())
	}
	fmt.Println()
	fmt.Println("Run `codedna init` to update settings.")
}

// ── export ───────────────────────────────────────────────────────────────────

func cmdExport(args []string) {
	target := "."
	outFile := ""
	switch len(args) {
	case 0:
		// defaults
	case 1:
		target = args[0]
	default:
		target = args[0]
		outFile = args[1]
	}

	abs, err := filepath.Abs(target)
	if err != nil {
		fatalf("resolve path: %v", err)
	}

	cfg := loadConfigQuiet()
	manifest, found, err := knowledge.FindRepoByPath(cfg, abs)
	if err != nil {
		fatalf("lookup: %v", err)
	}
	if !found {
		fatalf("repo %s is not indexed — run `codedna index %s` first", abs, target)
	}

	content, err := knowledge.ReadMasterContext(cfg, manifest.Repo.ID, manifest)
	if err != nil {
		fatalf("read master context: %v", err)
	}

	if outFile == "" {
		outFile = manifest.Repo.ID + "-knowledge.md"
	}
	if err := os.WriteFile(outFile, []byte(content), 0o644); err != nil {
		fatalf("write file: %v", err)
	}
	fmt.Printf("Exported master context to: %s\n", outFile)
	fmt.Printf("  Repo:   %s\n", manifest.Repo.Path)
	fmt.Printf("  Size:   %d bytes\n", len(content))
}

// ── diff ─────────────────────────────────────────────────────────────────────

func cmdDiff(args []string) {
	target := "."
	if len(args) > 0 {
		target = args[0]
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		fatalf("resolve path: %v", err)
	}

	cfg := loadConfigQuiet()
	manifest, found, err := knowledge.FindRepoByPath(cfg, abs)
	if err != nil {
		fatalf("lookup: %v", err)
	}
	if !found {
		fatalf("repo %s is not indexed — run `codedna index %s` first", abs, target)
	}

	// Load the stored tree (hashes from last index).
	storedTree, err := knowledge.ReadTree(cfg, manifest.Repo.ID)
	if err != nil {
		fatalf("read stored tree: %v", err)
	}
	storedHashes := make(map[string]string, len(storedTree))
	for _, n := range storedTree {
		storedHashes[n.Path] = n.Hash
	}

	// Scan the repo as it is now.
	scan, err := knowledge.ScanRepo(abs, knowledge.ScanModeSmart)
	if err != nil {
		fatalf("scan: %v", err)
	}
	currentHashes := make(map[string]string, len(scan.Tree))
	for _, n := range scan.Tree {
		currentHashes[n.Path] = n.Hash
	}

	var added, modified, deleted []string
	for path, hash := range currentHashes {
		if stored, ok := storedHashes[path]; !ok {
			added = append(added, path)
		} else if stored != hash {
			modified = append(modified, path)
		}
	}
	for path := range storedHashes {
		if _, ok := currentHashes[path]; !ok {
			deleted = append(deleted, path)
		}
	}

	total := len(added) + len(modified) + len(deleted)
	if total == 0 {
		fmt.Println("No changes since last index.")
		return
	}

	fmt.Printf("Changes since last index (%s):\n\n", manifest.Repo.UpdatedAt.Format(time.RFC3339))
	for _, f := range added {
		fmt.Printf("  + %s\n", f)
	}
	for _, f := range modified {
		fmt.Printf("  ~ %s\n", f)
	}
	for _, f := range deleted {
		fmt.Printf("  - %s\n", f)
	}
	fmt.Printf("\n%d added, %d modified, %d deleted\n", len(added), len(modified), len(deleted))
	if total > 0 {
		fmt.Println("\nRun `codedna reindex .` to update the index.")
	}
}

// ── docs ─────────────────────────────────────────────────────────────────────

func cmdDocs(args []string) {
	target := "."
	if len(args) > 0 {
		target = args[0]
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		fatalf("resolve path: %v", err)
	}

	cfg := loadConfigQuiet()
	manifest, found, err := knowledge.FindRepoByPath(cfg, abs)
	if err != nil {
		fatalf("lookup: %v", err)
	}
	if !found {
		fatalf("repo %s is not indexed — run `codedna index %s` first", abs, target)
	}

	names, err := knowledge.ListServiceDocs(cfg, manifest.Repo.ID)
	if err != nil {
		fatalf("list service docs: %v", err)
	}

	if len(names) == 0 {
		fmt.Println("No service docs generated for this repo.")
		return
	}

	fmt.Printf("Service docs for %s (%d):\n\n", manifest.Repo.ID, len(names))
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tPATH")
	fmt.Fprintln(w, "────────────────────────\t────────────────────────")
	for _, name := range names {
		doc, err := knowledge.ReadServiceDoc(cfg, manifest.Repo.ID, name)
		if err != nil {
			fmt.Fprintf(w, "%s\t(unreadable: %v)\n", name, err)
			continue
		}
		path := filepath.Join(cfg.BaseDir, manifest.Repo.ID, "service-docs", name+".json")
		_ = doc
		fmt.Fprintf(w, "%s\t%s\n", name, path)
	}
	w.Flush()
}

// ── delete ───────────────────────────────────────────────────────────────────

func cmdDelete(args []string) {
	target := "."
	if len(args) > 0 {
		target = args[0]
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		fatalf("resolve path: %v", err)
	}

	cfg := loadConfigQuiet()
	manifest, found, err := knowledge.FindRepoByPath(cfg, abs)
	if err != nil {
		fatalf("lookup: %v", err)
	}
	if !found {
		fmt.Printf("Not indexed: %s\nNothing to delete.\n", abs)
		return
	}

	repoDir := filepath.Join(cfg.BaseDir, manifest.Repo.ID)
	fmt.Printf("This will delete all indexed data for:\n")
	fmt.Printf("  Repo:    %s\n", manifest.Repo.Path)
	fmt.Printf("  ID:      %s\n", manifest.Repo.ID)
	fmt.Printf("  Storage: %s\n", repoDir)
	fmt.Printf("\nType the repo ID to confirm deletion: ")
	os.Stdout.Sync()

	sc := bufio.NewScanner(os.Stdin)
	sc.Scan()
	confirm := strings.TrimSpace(sc.Text())
	if confirm != manifest.Repo.ID {
		fmt.Println("Cancelled.")
		return
	}

	if err := os.RemoveAll(repoDir); err != nil {
		fatalf("delete: %v", err)
	}
	fmt.Printf("\n✓ Deleted: %s\n", repoDir)
}

// ── status ────────────────────────────────────────────────────────────────────

func cmdStatus(args []string) {
	target := "."
	if len(args) > 0 {
		target = args[0]
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		fatalf("resolve path: %v", err)
	}

	cfg := loadConfigQuiet()
	manifest, found, err := knowledge.FindRepoByPath(cfg, abs)
	if err != nil {
		fatalf("lookup: %v", err)
	}
	if !found {
		fmt.Printf("Not indexed: %s\n", abs)
		fmt.Println("Run `codedna index .` to index it.")
		return
	}

	r := manifest.Repo
	fmt.Printf("Repo:        %s\n", r.Path)
	fmt.Printf("ID:          %s\n", r.ID)
	fmt.Printf("Status:      %s\n", r.Status)
	fmt.Printf("Model:       %s\n", r.Model)
	fmt.Printf("Files:       %d\n", r.FileCount)
	fmt.Printf("Levels:      %d\n", r.LevelsCount)
	fmt.Printf("Tokens:      %d\n", r.TotalTokens)
	fmt.Printf("Created:     %s\n", r.CreatedAt.Format(time.RFC3339))
	fmt.Printf("Updated:     %s\n", r.UpdatedAt.Format(time.RFC3339))
	fmt.Printf("Storage:     %s\n", filepath.Join(cfg.BaseDir, r.ID))
	fmt.Println()
	fmt.Printf("Levels:\n")
	for _, l := range manifest.Levels {
		fmt.Printf("  L%d: %d agents, %d tokens\n", l.Number, l.AgentCount, l.TotalTokens)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

// loadConfigQuiet loads config without printing the provider/storage header.
func loadConfigQuiet() knowledge.Config {
	saved, _ := knowledge.LoadSavedConfig()
	cfg := knowledge.ConfigFromEnv()
	return knowledge.ApplySavedConfig(cfg, saved)
}

// loadConfig reads the persisted config, then overlays env vars.
// It prints a one-line summary of what it loaded.
func loadConfig() knowledge.Config {
	saved, err := knowledge.LoadSavedConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not read saved config: %v\n", err)
	}
	cfg := knowledge.ConfigFromEnv()
	cfg = knowledge.ApplySavedConfig(cfg, saved)

	// Resolve provider.
	provider := llm.Provider(cfg.LLMProvider)
	if provider == "" {
		provider = llm.ProviderOpenAI
	}
	switch provider {
	case llm.ProviderOllama:
		if cfg.OllamaURL == "" {
			fatalf("No Ollama URL configured. Run `codedna init` or set OLLAMA_URL.")
		}
		if cfg.OllamaModel == "" {
			fatalf("No Ollama model configured. Run `codedna init` or set OLLAMA_MODEL.")
		}
		fmt.Printf("Provider: ollama  url=%s  model=%s\n", cfg.OllamaURL, cfg.OllamaModel)
	default:
		if cfg.APIKey == "" {
			fatalf("No OpenAI API key. Run `codedna init` or set OPENAI_API_KEY.")
		}
		fmt.Printf("Provider: openai  model=%s\n", cfg.GetIndexModel())
	}
	fmt.Printf("Storage:  %s\n", cfg.BaseDir)
	return cfg
}

func buildClient(cfg knowledge.Config) knowledge.CompletionClient {
	return llm.NewFromConfig(cfg)
}

func printManifest(m *knowledge.Manifest, elapsed time.Duration) {
	fmt.Printf("\nDone in %s\n", elapsed.Round(time.Second))
	fmt.Printf("  ID:     %s\n", m.Repo.ID)
	fmt.Printf("  Status: %s\n", m.Repo.Status)
	fmt.Printf("  Files:  %d\n", m.Repo.FileCount)
	fmt.Printf("  Tokens: %d\n", m.Repo.TotalTokens)
	fmt.Printf("  Levels: %d\n", m.Repo.LevelsCount)
	for _, l := range m.Levels {
		fmt.Printf("    L%d: %d agents, %d tokens\n", l.Number, l.AgentCount, l.TotalTokens)
	}
}

func printUsage() {
	fmt.Print(`code-dna — index and query codebases with AI

Usage:
  codedna <command> [flags] [args]

Commands:
  init                        Interactive setup wizard (storage path, LLM provider, API key)
  config                      Show active configuration
  index [path]                Full index of a repo (default: current directory)
  index --reindex [path]      Incremental reindex — only changed files
  reindex [path]              Shorthand for index --reindex
  query [path] [question]     Query an indexed repo (interactive if no question given)
  list                        List all indexed repos
  status [path]               Show index status for a repo
  diff [path]                 Show files changed since last index
  docs [path]                 List generated service docs
  export [path] [output]      Export master context to a markdown file
  delete [path]               Remove indexed data for a repo (with confirmation)

Examples:
  codedna init
  codedna config
  codedna index .
  codedna reindex .
  codedna index --reindex /path/to/repo
  codedna query . "how does authentication work?"
  codedna query .              # interactive mode
  codedna list
  codedna status .
  codedna diff .
  codedna docs .
  codedna export . context.md
  codedna delete .

Configuration (in priority order):
  1. Environment variables  (OPENAI_API_KEY, LLM_PROVIDER, OLLAMA_URL, …)
  2. ~/.code-dna/config.json  (written by codedna init)
  3. Built-in defaults

`)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "Error: "+format+"\n", args...)
	os.Exit(1)
}

func loadEnvFile(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if os.Getenv(k) == "" {
			os.Setenv(k, v)
		}
	}
}

func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "."
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
