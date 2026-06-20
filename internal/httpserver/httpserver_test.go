package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tscolari/obsidian-graph-mcp/internal/index"
	"github.com/tscolari/obsidian-graph-mcp/internal/mcpserver"
	"github.com/tscolari/obsidian-graph-mcp/internal/store"
)

func newTestServer(t *testing.T) (*httptest.Server, *store.Store, string) {
	t.Helper()
	ctx := context.Background()

	vaultDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(vaultDir, "Alpha.md"), []byte("body of Alpha"), 0o644); err != nil {
		t.Fatalf("seed note: %v", err)
	}

	st, err := store.Open(filepath.Join(vaultDir, "g.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.ApplySchema(ctx); err != nil {
		t.Fatalf("schema: %v", err)
	}
	if _, err := index.Vault(ctx, st, vaultDir); err != nil {
		t.Fatalf("index: %v", err)
	}

	srv := mcpserver.New(st, "test", "")
	ts := httptest.NewServer(New(st, vaultDir, srv))
	t.Cleanup(ts.Close)
	return ts, st, vaultDir
}

func TestHealthz(t *testing.T) {
	ts, _, _ := newTestServer(t)
	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestReindexRejectsGet(t *testing.T) {
	ts, _, _ := newTestServer(t)
	resp, err := http.Get(ts.URL + "/reindex")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
}

func TestReindexPicksUpNewNote(t *testing.T) {
	ts, st, vaultDir := newTestServer(t)
	ctx := context.Background()

	if err := os.WriteFile(filepath.Join(vaultDir, "Beta.md"), []byte("body of Beta"), 0o644); err != nil {
		t.Fatalf("write new note: %v", err)
	}

	resp, err := http.Post(ts.URL+"/reindex", "", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var stats index.Stats
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if stats.Changed < 1 {
		t.Fatalf("changed = %d, want >= 1", stats.Changed)
	}

	hits, err := st.Search(ctx, "Beta", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("Beta not queryable after reindex")
	}
}

func TestReindexPicksUpModifiedNote(t *testing.T) {
	ts, st, vaultDir := newTestServer(t)
	ctx := context.Background()

	if err := os.WriteFile(filepath.Join(vaultDir, "Alpha.md"), []byte("updated body of Alpha"), 0o644); err != nil {
		t.Fatalf("modify note: %v", err)
	}

	resp, err := http.Post(ts.URL+"/reindex", "", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	var stats index.Stats
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if stats.Changed < 1 {
		t.Fatalf("changed = %d, want >= 1", stats.Changed)
	}

	_, _, content, err := st.ReadNote(ctx, "Alpha")
	if err != nil {
		t.Fatalf("read note: %v", err)
	}
	if content != "updated body of Alpha" {
		t.Fatalf("content = %q, want updated body", content)
	}
}

func TestMCPEndToEnd(t *testing.T) {
	ts, _, _ := newTestServer(t)
	ctx := context.Background()

	transport := mcp.NewStreamableClientTransport(ts.URL+"/mcp", nil)
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "search_notes",
		Arguments: map[string]any{"query": "Alpha"},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %+v", res.Content)
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] = %T, want *mcp.TextContent", res.Content[0])
	}
	if tc.Text == "" {
		t.Fatal("expected non-empty search result for Alpha")
	}
}

func TestConcurrentReindexAndToolCall(t *testing.T) {
	ts, _, vaultDir := newTestServer(t)
	ctx := context.Background()

	transport := mcp.NewStreamableClientTransport(ts.URL+"/mcp", nil)
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer session.Close()

	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func(n int) {
			defer wg.Done()
			path := filepath.Join(vaultDir, "Concurrent.md")
			if err := os.WriteFile(path, []byte("rev"), 0o644); err != nil {
				errs <- err
				return
			}
			resp, err := http.Post(ts.URL+"/reindex", "", nil)
			if err != nil {
				errs <- err
				return
			}
			resp.Body.Close()
		}(i)
		go func() {
			defer wg.Done()
			_, err := session.CallTool(ctx, &mcp.CallToolParams{
				Name:      "search_notes",
				Arguments: map[string]any{"query": "Alpha"},
			})
			if err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent op failed: %v", err)
	}
}
