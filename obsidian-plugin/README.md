# Graph MCP (Obsidian plugin)

Supervises the `obsidian-graph-mcp` binary as a long-lived background process
for the vault it's installed in: spawns it (or reattaches to an already-running
instance) on vault open, debounces Obsidian's own save/rename/delete events into
`POST /reindex` calls, and exposes a settings tab with copyable config snippets
for Claude Desktop, Claude Code, OpenCode, and Codex. See the [Obsidian plugin
contract](../README.md#obsidian-plugin-contract) in the main README for the
protocol this implements.

## Prerequisites

- A built `obsidian-graph-mcp` binary (see the [main README](../README.md#build--run)) —
  the plugin doesn't build or download it; you set the path in its settings tab.
- Node.js (for building the plugin itself).

## Build

```sh
cd obsidian-plugin
npm install
npm run build
```

This runs a type-check and bundles `src/main.ts` into `main.js` (production
mode: no sourcemaps, minified).

## Install locally (unpacked)

Obsidian loads plugins from `<vault>/.obsidian/plugins/<plugin-id>/`. After
building, copy (or symlink, for live development) the three required files
into a folder named after the plugin's `id` (`obsidian-graph-mcp`) inside the
target vault:

```sh
VAULT=/abs/path/to/your-vault
mkdir -p "$VAULT/.obsidian/plugins/obsidian-graph-mcp"
cp manifest.json main.js versions.json "$VAULT/.obsidian/plugins/obsidian-graph-mcp/"
```

For active development, symlink instead so rebuilds show up without re-copying:

```sh
ln -s "$(pwd)/manifest.json" "$(pwd)/main.js" "$(pwd)/versions.json" \
  "$VAULT/.obsidian/plugins/obsidian-graph-mcp/"
```

Then in Obsidian:

1. Settings → Community plugins → turn off **Restricted mode** if it's on.
2. Reload the list (or restart Obsidian) so the new plugin appears.
3. Enable **Graph MCP**.
4. Open its settings tab and set the **Binary path** to your built
   `obsidian-graph-mcp` executable. Adjust port/name/context/auto-start/reindex
   debounce as needed, then use the **Start** button (or reopen the vault, if
   auto-start is on) to launch the server.

## Dev loop

```sh
npm run dev    # esbuild in watch-free dev mode (unminified, sourcemapped)
npm test       # node:test over src/**/*.test.ts via tsx
```

After editing source, re-run `npm run dev` (or `npm run build`) and reload
Obsidian's plugin list (or use the "Reload app without saving" command) to
pick up the new `main.js`.
