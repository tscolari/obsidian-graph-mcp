// Command obsidian-graph-mcp indexes an Obsidian vault into SQLite and serves
// its link graph to an MCP client (Claude Desktop, Claude Code, etc.) over
// stdio (default, one process per client) or HTTP (-http, a long-lived
// instance shared by several clients).
//
//	obsidian-graph-mcp -vault ~/notes -db ~/notes/.graph.db
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/tscolari/obsidian-graph-mcp/internal/httpserver"
	"github.com/tscolari/obsidian-graph-mcp/internal/index"
	"github.com/tscolari/obsidian-graph-mcp/internal/mcpserver"
	"github.com/tscolari/obsidian-graph-mcp/internal/store"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	vaultDir := flag.String("vault", ".", "path to the Obsidian vault")
	dbPath := flag.String("db", "", "SQLite path (default: <vault>/.graph.db)")
	indexOnly := flag.Bool("index-only", false, "index then exit, do not serve")
	name := flag.String("name", "", "MCP server name for this vault, used to namespace tools when running multiple instances (default: obsidian-graph-<vault folder>)")
	vaultContext := flag.String("context", "", "one-line description of what this vault holds, advertised to the agent (e.g. \"HashiCorp work notes: incidents, projects, people\")")
	httpAddr := flag.String("http", "", "if set, serve MCP over HTTP at this address (e.g. 127.0.0.1:8765) instead of stdio")
	flag.Parse()

	if *dbPath == "" {
		*dbPath = *vaultDir + "/.graph.db"
	}
	if *name == "" {
		*name = "obsidian-graph-" + filepath.Base(filepath.Clean(*vaultDir))
	}

	// MCP speaks JSON-RPC on stdout, so all logging must go to stderr.
	log.SetOutput(os.Stderr)
	ctx := context.Background()

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer st.Close()

	if err := st.ApplySchema(ctx); err != nil {
		log.Fatalf("schema: %v", err)
	}

	stats, err := index.Vault(ctx, st, *vaultDir)
	if err != nil {
		log.Fatalf("index: %v", err)
	}
	log.Printf("indexed: %d notes seen, %d (re)indexed", stats.Seen, stats.Changed)

	if *indexOnly {
		return
	}

	srv := mcpserver.New(st, *name, *vaultContext)

	if *httpAddr == "" {
		if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil {
			log.Fatalf("serve: %v", err)
		}
		return
	}

	log.Printf("serving http on %s (/mcp, /healthz, /reindex)", *httpAddr)
	if err := http.ListenAndServe(*httpAddr, httpserver.New(st, *vaultDir, srv)); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
