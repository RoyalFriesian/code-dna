// cmd/index indexes a repository and writes the knowledge base to disk.
//
// Usage:
//
//	go run ./cmd/index [path/to/repo]
//
// OPENAI_API_KEY must be set (or present in a .env file in the repo root).
package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/RoyalFriesian/code-dna/llm"
	"github.com/RoyalFriesian/code-dna/pkg/knowledge"
)

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

func main() {
	// Load .env from the working directory or the binary's parent.
	loadEnvFile(".env")
	loadEnvFile(filepath.Join(filepath.Dir(os.Args[0]), ".env"))

	// Flags: [-reindex] [path]
	reindex := false
	args := os.Args[1:]
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
		log.Fatalf("resolve path: %v", err)
	}

	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		log.Fatal("OPENAI_API_KEY is required (set it in your environment or a .env file)")
	}

	cfg := knowledge.ConfigFromEnv()
	cfg.APIKey = key

	client := llm.New(key)

	t0 := time.Now()
	progress := func(stage string, current, total int) {
		elapsed := time.Since(t0).Round(time.Second)
		if total > 0 {
			fmt.Printf("[%s] %s: %d/%d\n", elapsed, stage, current, total)
		} else {
			fmt.Printf("[%s] %s\n", elapsed, stage)
		}
	}

	if reindex {
		fmt.Printf("Incremental reindex of %s ...\n", abs)
		manifest, changed, err := knowledge.ReindexRepo(context.Background(), client, abs, cfg, progress)
		if err != nil {
			log.Fatalf("ReindexRepo: %v", err)
		}
		fmt.Printf("\n=== Done in %s ===\n", time.Since(t0).Round(time.Second))
		fmt.Printf("ID=%s  files=%d  changed=%d  levels=%d  tokens=%d  status=%s\n",
			manifest.Repo.ID, manifest.Repo.FileCount, changed,
			manifest.Repo.LevelsCount, manifest.Repo.TotalTokens, manifest.Repo.Status)
		for _, l := range manifest.Levels {
			fmt.Printf("  L%d: %d agents, %d tokens\n", l.Number, l.AgentCount, l.TotalTokens)
		}
		if changed == 0 {
			fmt.Println("Nothing changed — existing index is up to date.")
			return
		}
	} else {
		fmt.Printf("Indexing %s ...\n", abs)
		manifest, err := knowledge.IndexRepo(context.Background(), client, abs, cfg, progress)
		if err != nil {
			log.Fatalf("IndexRepo: %v", err)
		}
		fmt.Printf("\n=== Done in %s ===\n", time.Since(t0).Round(time.Second))
		fmt.Printf("ID=%s  files=%d  levels=%d  tokens=%d  status=%s\n",
			manifest.Repo.ID, manifest.Repo.FileCount, manifest.Repo.LevelsCount,
			manifest.Repo.TotalTokens, manifest.Repo.Status)
		for _, l := range manifest.Levels {
			fmt.Printf("  L%d: %d agents, %d tokens\n", l.Number, l.AgentCount, l.TotalTokens)
		}

		mc, err := knowledge.ReadMasterContext(cfg, manifest.Repo.ID, *manifest)
		if err == nil {
			fmt.Printf("\nMaster context: %d bytes\n", len(mc))
			preview := mc
			if len(preview) > 2000 {
				preview = preview[:2000] + "\n...(truncated)"
			}
			fmt.Println(preview)
		}
	}
}
