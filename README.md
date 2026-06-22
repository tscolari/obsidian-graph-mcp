# obsidian-graph-mcp

A small Go MCP server that turns an Obsidian vault's `[[wikilink]]` structure into
a queryable knowledge graph. Your hand-curated links *are* the graph — no entity
extraction, no ontology, no embeddings required. An MCP client (Claude Desktop,
Claude Code) can search for an entry-point note and then traverse the link graph
for correlated context.

## Layout

```
main.go                      flags, index, serve over stdio or HTTP
internal/vault/parser.go     [[wikilinks]], embeds, frontmatter tags, #hashtags
internal/store/store.go      SQLite schema + graph queries (traversal/links/tags)
internal/index/indexer.go    walk vault, parse, upsert incrementally, resolve links
internal/mcpserver/server.go MCP tools wrapping the store
internal/httpserver/        /mcp, /healthz, /reindex for networked deployments
```

## Data model

Notes are rows; wikilinks are edges in a `links` table. An unresolved link keeps
`dst_id NULL`, so dangling links (knowledge gaps) stay queryable. Resolution is
two-pass: `[[folder/Note]]` resolves by path first (honouring the explicit prefix);
`[[Note]]` resolves by case-insensitive title. A path-prefixed link that doesn't
match any path stays dangling — no silent title fallback.

Each edge carries a **relation type** (`rel`): body wikilinks get `rel=''`, while
wikilinks in **frontmatter properties** become typed edges tagged with the property
name — `origin`, `references`, `created at`, etc. These are the hand-curated,
directional relations (a vault-wide `Origin`/`References` convention), so the graph
distinguishes "deliberately linked as provenance" from "incidentally mentioned in the
body". Frontmatter parsing takes only `[[wikilink]]` values (plain scalars like Jira
IDs are ignored) and skips template placeholders (`[[<% ... %>]]`, `[[{{date}}]]`).

## Frontmatter format

Any frontmatter property whose value contains a `[[wikilink]]` becomes a typed
edge in the graph. The lowercased property name is the edge type (`rel`).

```yaml
---
title: "My Note"           # overrides the filename stem as the note's display title
Origin: "[[Parent Project]]"
References:
  - "[[RFC 001]]"
  - "[[Design Doc]]"
  - PROJ-42                # plain text — not a wikilink, ignored
tags: [go, architecture]   # indexed as tags, not as link targets
---
Body text with a [[casual mention]] and [[RFC 001]] again.
```

| Frontmatter | Result |
|-------------|--------|
| `Origin: "[[Parent Project]]"` | one edge: `rel=origin` → `Parent Project` |
| `References:` block list | edges: `rel=references` → `RFC 001`, `Design Doc` |
| `PROJ-42` in the list | ignored — no `[[…]]` |
| `tags: [go, architecture]` | tags indexed; no link edges |
| `[[RFC 001]]` in the body | separate edge: `rel=""` (body) → `RFC 001` |

`RFC 001` ends up with **two** edges — one `references` and one body — because
deduplication is per `(rel, target)` pair, not just target.

**What is ignored:**
- Plain-text scalars (`Jira: PLAT-2784`)
- Template placeholders inside wikilinks: `[[<% tp.date.now() %>]]`, `[[{{date}}]]`
- Wikilinks inside fenced code blocks or inline backticks

**Naming conventions** — `Origin` and `References` are the idiomatic property
names used by the `origin_chain` tool (follows `Origin` links to the root) and
the `neighborhood` `rels` filter, but the parser gives no special treatment to
any property name. You can use whatever names fit your vault; they simply
become `rel` values on edges.

## Build & run

```sh
go mod tidy
go build -o obsidian-graph-mcp .
./obsidian-graph-mcp -vault ~/notes -index-only   # smoke-test the index
```

## Serve over HTTP (for long-lived, shared instances)

By default the server speaks MCP over stdio, spawned fresh by each client (one
process per client). Pass `-http <addr>` to instead serve over HTTP, so a single
already-indexed process can be shared by several MCP clients at once — e.g. an
instance kept running by an Obsidian plugin for as long as the vault is open:

```sh
./obsidian-graph-mcp -vault ~/notes -http 127.0.0.1:8765
```

| route | method | purpose |
|-------|--------|---------|
| `/mcp` | POST/GET (Streamable HTTP) | the MCP endpoint — point clients here |
| `/healthz` | GET | liveness probe; 200 once the process is up (indexing already ran) |
| `/reindex` | POST | re-walks the vault, returns `{"seen":N,"changed":N}`; cheap no-op when nothing changed (content-hash based) |

`-http` and stdio are mutually exclusive per process; pick one per instance.

### Obsidian plugin contract

There's no way to load a compiled Go binary as an Obsidian plugin directly —
plugins are JS/TS bundles loaded into Obsidian's Electron process. The
supported pattern (used by e.g. `obsidian-git` to drive the real `git` binary)
is a thin plugin that manages this binary as a subprocess via Node's
`child_process`. The [`obsidian-plugin/`](./obsidian-plugin) folder in this
repo implements exactly that against the `-http` mode above. It:

- **Spawn**, on vault open: `obsidian-graph-mcp -vault <abs vault path> -http 127.0.0.1:<port> -name <derived> -context <optional>` as a background process. stdout/stderr are free for logging in HTTP mode (unlike stdio mode, where stdout carries JSON-RPC).
- **Use a fixed, user-configured port** (no implicit scanning/auto-bumping): the port a client's MCP config points at must stay stable, so on collision the fix is to change the setting, not have the plugin silently pick a different port.
- **Wait for readiness** by polling `GET /healthz` (e.g. every 200ms, ~10s timeout) before treating the vault as ready — a 200 means indexing already completed before the listener opened.
- **Trigger reindexing** with `POST /reindex` on Obsidian's save/rename/delete vault events. Debouncing is the plugin's job, not the server's: `/reindex` always walks the full tree (cheaply, via content hash) and does no internal queuing.
- **Shut down** on vault close by killing the spawned process by its tracked PID (there's no `/shutdown` route — process lifecycle is OS-level only). Don't kill on plugin disable, since a Claude Desktop session may still be attached; vault close is the right trigger.
- **Avoid duplicate instances** on a vault open in multiple windows by checking the configured port's `/healthz` before spawning a second process against the same SQLite file.

## Install the Obsidian plugin

### Via BRAT (recommended)

[BRAT](https://github.com/TfTHacker/obsidian42-brat) lets you install the plugin
directly from this repo, with one-click updates.

1. Install **BRAT** from the Obsidian community plugins.
2. Open **Settings → BRAT → Add Beta Plugin** and enter:
   ```
   tscolari/obsidian-graph-mcp
   ```
3. Enable **Graph MCP** in **Settings → Community Plugins**.
4. Open **Settings → Graph MCP**, set the **Binary path** to the compiled
   `obsidian-graph-mcp` binary (see [Build & run](#build--run) above), and
   optionally toggle **Auto-start**.

BRAT downloads `main.js` and `manifest.json` from the latest GitHub release and
keeps the plugin up to date automatically.

### Local dev (symlink)

See [`obsidian-plugin/README.md`](./obsidian-plugin/README.md) for how to build
and symlink the plugin directory into a vault for local development.

## Wire into Claude Desktop / Claude Code

`claude_desktop_config.json` (or `claude mcp add`), spawning over stdio:

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

Or, against an already-running HTTP instance (see above):

```json
{
  "mcpServers": {
    "obsidian-graph": {
      "type": "http",
      "url": "http://127.0.0.1:8765/mcp"
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
- `internal/httpserver/httpserver_test.go` — `/healthz`, `/reindex` (method
  guard, picks up new/modified notes), an end-to-end MCP tool call over
  `/mcp` via the go-sdk's own client transport, and a concurrent
  reindex-vs-tool-call smoke test.

## To extend

- **Search at scale:** replace the `LIKE` scan with an FTS5 virtual table.
- **Vector entry points:** add embeddings via `sqlite-vec`; keep `neighborhood`
  for depth. Vector finds the door, the link graph walks the house.
- **Resolution:** current resolve is wholesale; for large vaults resolve only the
  changed note's targets + any links that newly point at it.
- **Aliases:** index frontmatter `aliases:` so links resolve to alternate names.
- ~~**Watch mode**~~ — superseded by `-http` + `/reindex`: a future Obsidian
  plugin can drive reindexing directly off Obsidian's own file-save events
  (see "Obsidian plugin contract" above) rather than Go reinventing `fsnotify`.
