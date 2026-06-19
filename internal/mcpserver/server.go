// Package mcpserver exposes the vault graph as MCP tools. Tool input/output
// schemas are inferred from the struct json/jsonschema tags by the SDK's
// generic AddTool, so the LLM sees typed, documented parameters.
package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tscolari/obsidian-graph-mcp/internal/store"
)

// New builds a configured MCP server backed by st.
func New(st *store.Store) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{
		Name:    "obsidian-graph",
		Version: "0.1.0",
	}, nil)

	h := &handlers{st: st}

	mcp.AddTool(s, &mcp.Tool{
		Name:        "search_notes",
		Description: "FIRST STOP when a question touches the user's own projects, decisions, people, incidents, or prior work — their knowledge lives in these notes, not in the codebase or your training data. Also use when you hit an internal term, acronym, or project name you can't resolve elsewhere. Returns entry-point notes to traverse from.",
	}, h.search)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "read_note",
		Description: "Read a note's full markdown by title or vault-relative path. Use after search_notes/neighborhood/origin_chain to read the specific notes you decided are relevant.",
	}, h.read)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "note_links",
		Description: "Show how a note connects: its outgoing wikilinks and its backlinks, grouped by relation (body / origin / references / …). Use to see what the user deliberately linked to and from a note before deciding what to read next.",
	}, h.links)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "neighborhood",
		Description: "After finding an entry note, call this to gather the correlated context the user has hand-linked around it, within N hops (links followed in both directions). The primary way to assemble relevant background. Pass rels=[\"origin\",\"references\"] to follow only curated frontmatter relations and ignore incidental body mentions.",
	}, h.neighborhood)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "origin_chain",
		Description: "Use when asked WHY a note/project/decision exists or WHERE it came from. Follows a note's Origin frontmatter link directionally to its root — the provenance/genealogy trace — returning the ordered chain from the note up to its origin root.",
	}, h.originChain)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "notes_by_tag",
		Description: "List notes carrying a given tag (frontmatter or inline #tag). Use to find all of the user's notes in a category (e.g. incidents, people, reading) when you don't have a specific entry-point title.",
	}, h.byTag)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "dangling_links",
		Description: "List wikilinks that point at notes which don't exist yet — the user's knowledge gaps. The rel shows the relation, so a dangling Origin/References is distinguishable from a passing body mention. Use to spot missing context or surface gaps worth flagging to the user.",
	}, h.dangling)

	return s
}

type handlers struct{ st *store.Store }

type searchIn struct {
	Query string `json:"query" jsonschema:"text to match in titles and bodies"`
	Limit int    `json:"limit,omitempty" jsonschema:"max results (default 10)"`
}
type searchOut struct {
	Hits []store.Hit `json:"hits"`
}

func (h *handlers) search(ctx context.Context, _ *mcp.CallToolRequest, in searchIn) (*mcp.CallToolResult, searchOut, error) {
	hits, err := h.st.Search(ctx, in.Query, in.Limit)
	if err != nil {
		return nil, searchOut{}, err
	}
	var b strings.Builder
	for _, hit := range hits {
		fmt.Fprintf(&b, "- %s  (%s)\n", hit.Title, hit.Path)
	}
	return text(b.String()), searchOut{Hits: hits}, nil
}

type refIn struct {
	Ref string `json:"ref" jsonschema:"note title or vault-relative path"`
}
type readOut struct {
	Title   string `json:"title"`
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (h *handlers) read(ctx context.Context, _ *mcp.CallToolRequest, in refIn) (*mcp.CallToolResult, readOut, error) {
	title, path, content, err := h.st.ReadNote(ctx, in.Ref)
	if err != nil {
		return nil, readOut{}, err
	}
	return text(content), readOut{Title: title, Path: path, Content: content}, nil
}

type titleIn struct {
	Title string `json:"title" jsonschema:"note title"`
}
type linksOut struct {
	Outlinks  []store.Ref `json:"outlinks"`
	Backlinks []store.Ref `json:"backlinks"`
}

func (h *handlers) links(ctx context.Context, _ *mcp.CallToolRequest, in titleIn) (*mcp.CallToolResult, linksOut, error) {
	out, back, err := h.st.Links(ctx, in.Title)
	if err != nil {
		return nil, linksOut{}, err
	}
	var b strings.Builder
	b.WriteString("outlinks:\n")
	writeRefsByRel(&b, out)
	b.WriteString("backlinks:\n")
	writeRefsByRel(&b, back)
	return text(b.String()), linksOut{Outlinks: out, Backlinks: back}, nil
}

// writeRefsByRel renders refs grouped by relation; "" (body links) is labelled
// "body". Refs are already ordered by rel then title.
func writeRefsByRel(b *strings.Builder, refs []store.Ref) {
	last := "\x00"
	for _, r := range refs {
		if r.Rel != last {
			last = r.Rel
			label := r.Rel
			if label == "" {
				label = "body"
			}
			fmt.Fprintf(b, "  [%s]\n", label)
		}
		fmt.Fprintf(b, "    %s\n", r.Title)
	}
}

type neighborhoodIn struct {
	Title string   `json:"title" jsonschema:"note to start from"`
	Depth int      `json:"depth,omitempty" jsonschema:"max hops, default 2"`
	Rels  []string `json:"rels,omitempty" jsonschema:"restrict to these relation types (e.g. origin, references); empty = all edges"`
}
type neighborhoodOut struct {
	Nodes []store.Neighbor `json:"nodes"`
}

func (h *handlers) neighborhood(ctx context.Context, _ *mcp.CallToolRequest, in neighborhoodIn) (*mcp.CallToolResult, neighborhoodOut, error) {
	depth := in.Depth
	if depth <= 0 {
		depth = 2
	}
	nodes, err := h.st.Neighborhood(ctx, in.Title, depth, in.Rels)
	if err != nil {
		return nil, neighborhoodOut{}, err
	}
	var b strings.Builder
	for _, n := range nodes {
		fmt.Fprintf(&b, "%s%s\n", strings.Repeat("  ", n.Depth), n.Title)
	}
	return text(b.String()), neighborhoodOut{Nodes: nodes}, nil
}

type originChainIn struct {
	Title string `json:"title" jsonschema:"note to trace Origin from"`
	Depth int    `json:"depth,omitempty" jsonschema:"max Origin hops, default 10"`
}
type originChainOut struct {
	Chain []store.Neighbor `json:"chain"`
}

func (h *handlers) originChain(ctx context.Context, _ *mcp.CallToolRequest, in originChainIn) (*mcp.CallToolResult, originChainOut, error) {
	depth := in.Depth
	if depth <= 0 {
		depth = 10
	}
	chain, err := h.st.OriginChain(ctx, in.Title, depth)
	if err != nil {
		return nil, originChainOut{}, err
	}
	var b strings.Builder
	for i, n := range chain {
		if i > 0 {
			b.WriteString(" → ")
		}
		b.WriteString(n.Title)
	}
	return text(b.String()), originChainOut{Chain: chain}, nil
}

type tagIn struct {
	Tag string `json:"tag" jsonschema:"tag name without the leading #"`
}
type notesOut struct {
	Notes []store.Ref `json:"notes"`
}

func (h *handlers) byTag(ctx context.Context, _ *mcp.CallToolRequest, in tagIn) (*mcp.CallToolResult, notesOut, error) {
	notes, err := h.st.NotesByTag(ctx, in.Tag)
	if err != nil {
		return nil, notesOut{}, err
	}
	return text(fmt.Sprintf("%d notes tagged %q", len(notes), in.Tag)), notesOut{Notes: notes}, nil
}

type emptyIn struct{}
type danglingOut struct {
	Links []store.Dangling `json:"links"`
}

func (h *handlers) dangling(ctx context.Context, _ *mcp.CallToolRequest, _ emptyIn) (*mcp.CallToolResult, danglingOut, error) {
	d, err := h.st.Dangling(ctx)
	if err != nil {
		return nil, danglingOut{}, err
	}
	return text(fmt.Sprintf("%d dangling links", len(d))), danglingOut{Links: d}, nil
}

func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}
