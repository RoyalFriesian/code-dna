package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/RoyalFriesian/code-dna/pkg/knowledge"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	httpAddr := flag.String("http", "", "HTTP listen address (e.g. :8081). Default: stdio.")
	flag.Parse()

	cfg := knowledge.ConfigFromEnv()
	llm := newLLMClient(cfg)

	server := mcp.NewServer(
		&mcp.Implementation{
			Name:    "knowledge-mcp",
			Version: "0.1.0",
		},
		&mcp.ServerOptions{
			Instructions: "Hierarchical code knowledge compression server. " +
				"Index repos with index_repo, query with query_repo, " +
				"list with list_repos, update with reindex_repo.",
		},
	)

	registerTools(server, cfg, llm)
	registerResources(server, cfg)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if *httpAddr != "" {
		handler := mcp.NewStreamableHTTPHandler(
			func(_ *http.Request) *mcp.Server { return server },
			nil,
		)
		fmt.Fprintf(os.Stderr, "knowledge-mcp listening on %s\n", *httpAddr)
		srv := &http.Server{Addr: *httpAddr, Handler: handler}
		go func() {
			<-ctx.Done()
			srv.Close()
		}()
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	} else {
		fmt.Fprintf(os.Stderr, "knowledge-mcp running on stdio\n")
		if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil {
			log.Fatalf("stdio error: %v", err)
		}
	}
}
