// cmd/query queries an already-indexed repository interactively.
//
// Usage:
//
//	go run ./cmd/query [path/to/repo] [question...]
//
// OPENAI_API_KEY must be set (or present in a .env file).
package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

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
	loadEnvFile(".env")
	loadEnvFile(filepath.Join(filepath.Dir(os.Args[0]), ".env"))

	target := "."
	if len(os.Args) > 1 {
		target = os.Args[1]
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		log.Fatalf("resolve path: %v", err)
	}

	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		log.Fatal("OPENAI_API_KEY is required")
	}

	cfg := knowledge.ConfigFromEnv()
	cfg.APIKey = key

	manifest, found, err := knowledge.FindRepoByPath(cfg, abs)
	if err != nil {
		log.Fatalf("FindRepoByPath: %v", err)
	}
	if !found {
		log.Fatalf("repository %s is not indexed — run `go run ./cmd/index %s` first", abs, abs)
	}
	fmt.Printf("Found index: %s (%d files, %d levels)\n\n",
		manifest.Repo.ID, manifest.Repo.FileCount, manifest.Repo.LevelsCount)

	client := llm.New(key)
	queryFn := knowledge.ResolveQuery(context.Background(), client, cfg, manifest)

	// Inline question from args, or enter interactive mode.
	if len(os.Args) > 2 {
		question := strings.Join(os.Args[2:], " ")
		runQuery(queryFn, question)
		return
	}

	// Interactive loop.
	sc := bufio.NewScanner(os.Stdin)
	fmt.Println("Enter your questions (Ctrl-D or empty line to quit):")
	for {
		fmt.Print("\nQ: ")
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
	result, err := queryFn(context.Background(), question)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	fmt.Printf("\nAnswer:\n%s\n", result.Answer)
	if len(result.Sources) > 0 {
		fmt.Println("\nSources:")
		for _, s := range result.Sources {
			fmt.Printf("  - %s\n", s.File)
		}
	}
}
