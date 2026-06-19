# obsidian-graph-mcp

A small Go MCP server that turns an Obsidian vault's `[[wikilink]]` structure into
a queryable knowledge graph. Your hand-curated links *are* the graph — no entity
extraction, no ontology, no embeddings required. An MCP client (Claude Desktop,
Claude Code) can search for an entry-point note and then traverse the link graph
for correlated context.

## Layout

```
main.go                      flags, index, serve over stdio
internal/vault/parser.go     [[wikilinks]], embeds, frontmatter tags, #hashtags
internal/store/store.go      SQLite schema + graph queries (traversal/links/tags)
internal/index/indexer.go    walk vault, parse, upsert incrementally, resolve links
internal/mcpserver/server.go MCP tools wrapping the store
```

## Data model

Notes are rows; wikilinks are edges in a `links` table. An unresolved link keeps
`dst_id NULL`, so dangling links (knowledge gaps) stay queryable. Resolution maps a
link target to a note by case-insensitive title match.

Each edge carries a **relation type** (`rel`): body wikilinks get `rel=''`, while
wikilinks in **frontmatter properties** become typed edges tagged with the property
name — `origin`, `references`, `created at`, etc. These are the hand-curated,
directional relations (a vault-wide `Origin`/`References` convention), so the graph
distinguishes "deliberately linked as provenance" from "incidentally mentioned in the
body". Frontmatter parsing takes only `[[wikilink]]` values (plain scalars like Jira
IDs are ignored) and skips template placeholders (`[[<% ... %>]]`, `[[{{date}}]]`).

## Build & run

```sh
go mod tidy
go build -o obsidian-graph-mcp .
./obsidian-graph-mcp -vault ~/notes -index-only   # smoke-test the index
```

## Wire into Claude Desktop / Claude Code

`claude_desktop_config.json` (or `claude mcp add`):

```json
{
  "mcpServers": {
    "obsidian-graph": {
      "command": "/abs/path/obsidian-graph-mcp",
      "args": ["-vault", "/abs/path/to/notes"]
    }
  }
}
```

### Multiple vaults

Run one instance per vault. Give each a distinct `-name` (so the agent sees its
tools namespaced as `mcp__<name>__search_notes` and can tell vaults apart) and a
`-context` blurb (advertised to clients as server instructions). `-name` defaults
to `obsidian-graph-<vault folder>`.

```json
{
  "mcpServers": {
    "obsidian-work": {
      "command": "/abs/path/obsidian-graph-mcp",
      "args": ["-vault", "/abs/path/work-vault",
               "-name", "obsidian-work",
               "-context", "Current job: incidents, projects, people, decisions"]
    },
    "obsidian-personal": {
      "command": "/abs/path/obsidian-graph-mcp",
      "args": ["-vault", "/abs/path/personal-vault",
               "-name", "obsidian-personal",
               "-context", "Personal life: reading lists, journaling, notes"]
    }
  }
}
```

Then keep the routing table in `AGENTS.md` in sync so the agent picks the right
vault per question.

## Tools exposed

| tool | purpose |
|------|---------|
| `search_notes` | find entry-point notes (title/body match) |
| `read_note` | full markdown by title or path |
| `note_links` | outlinks + backlinks for a note, grouped by relation (body/origin/references/…) |
| `neighborhood` | **n-hop undirected link neighbourhood — the core "correlated context" query** (optional `rels` filter to follow only e.g. `origin`/`references`) |
| `origin_chain` | **follow a note's `Origin` frontmatter link directionally to its root — the provenance/genealogy trace** |
| `notes_by_tag` | notes carrying a tag |
| `dangling_links` | wikilinks pointing at non-existent notes (carries the `rel`, so a dangling `Origin` is distinguishable from a body mention) |

The intended agent flow mirrors hybrid GraphRAG: `search_notes` (broad entry point)
→ `neighborhood` (relational depth) → `read_note` on the chosen nodes.

## Validated

Run `go test ./...`. Two stdlib-`testing` suites cover the packages carrying the
real logic:

- `internal/vault/parser_test.go` — alias/heading/folder stripping, embeds,
  code-block exclusion (inline + fenced), link dedup, frontmatter title
  override, all tag forms (inline array, block list, inline `#tag`), content
  hashing, and **frontmatter typed links** (scalar `Origin`, `References` block
  lists picking only `[[…]]`, template-placeholder skipping, per-`rel` dedup).
  ~95% coverage.
- `internal/store/store_test.go` — runs against a temp SQLite DB: upsert
  changed-flag, case-insensitive link resolution + dangling links (with `rel`),
  the recursive-CTE neighbourhood (cycle-safe, depth-bounded, shortest-depth,
  undirected) **plus its `rel` filter**, the directed **`origin_chain`** trace
  (ordered, cycle-safe, stops at unresolved), relation-typed `note_links`, the
  **v1→v2 schema migration**, tag lookup, search, and `read_note`. ~80% coverage.

## To extend

- **Search at scale:** replace the `LIKE` scan with an FTS5 virtual table.
- **Vector entry points:** add embeddings via `sqlite-vec`; keep `neighborhood`
  for depth. Vector finds the door, the link graph walks the house.
- **Resolution:** current resolve is wholesale; for large vaults resolve only the
  changed note's targets + any links that newly point at it.
- **Aliases:** index frontmatter `aliases:` so links resolve to alternate names.
- **Watch mode:** fsnotify on the vault for live re-indexing (incremental already
  skips unchanged files by content hash).
