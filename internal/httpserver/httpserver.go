// Package httpserver wires the MCP server, a liveness probe, and a reindex
// trigger into a single http.Handler for networked (non-stdio) deployments —
// e.g. a long-lived process shared by multiple MCP clients, spawned and
// driven by an Obsidian plugin.
package httpserver

import (
	"encoding/json"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tscolari/obsidian-graph-mcp/internal/index"
	"github.com/tscolari/obsidian-graph-mcp/internal/store"
)

// New builds the HTTP handler for a networked instance: the MCP endpoint at
// /mcp, a liveness probe at /healthz, and a reindex trigger at /reindex.
func New(st *store.Store, vaultDir string, mcpSrv *mcp.Server) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpSrv }, nil))
	mux.HandleFunc("/healthz", handleHealthz)
	mux.HandleFunc("/reindex", handleReindex(st, vaultDir))
	return mux
}

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func handleReindex(st *store.Store, vaultDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		stats, err := index.Vault(r.Context(), st, vaultDir)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(stats)
	}
}
