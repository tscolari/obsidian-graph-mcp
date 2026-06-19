// Command obsidian-graph-mcp indexes an Obsidian vault into SQLite and serves
// its link graph to an MCP client (Claude Desktop, Claude Code, etc.) over stdio.
//
//	obsidian-graph-mcp -vault ~/notes -db ~/notes/.graph.db
package main

import (
	"context"
	"flag"
	"log"
	"os"

	"github.com/tscolari/obsidian-graph-mcp/internal/index"
	"github.com/tscolari/obsidian-graph-mcp/internal/mcpserver"
	"github.com/tscolari/obsidian-graph-mcp/internal/store"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	vaultDir := flag.String("vault", ".", "path to the Obsidian vault")
	dbPath := flag.String("db", "", "SQLite path (default: <vault>/.graph.db)")
	indexOnly := flag.Bool("index-only", false, "index then exit, do not serve")
	flag.Parse()

	if *dbPath == "" {
		*dbPath = *vaultDir + "/.graph.db"
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

	srv := mcpserver.New(st)
	if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
