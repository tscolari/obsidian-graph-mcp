import assert from "node:assert/strict";
import { test } from "node:test";
import { TOOLS, type SnippetInputs } from "./config-snippets.ts";

const inputs: SnippetInputs = {
  name: "obsidian-work",
  port: 8765,
  binaryPath: "/usr/local/bin/obsidian-graph-mcp",
  vaultPath: "/home/user/notes",
};

test("claude-desktop http snippet is valid JSON pointing at /mcp", () => {
  const tool = TOOLS.find((t) => t.id === "claude-desktop")!;
  const parsed = JSON.parse(tool.http(inputs));
  assert.equal(parsed.mcpServers["obsidian-work"].type, "http");
  assert.equal(parsed.mcpServers["obsidian-work"].url, "http://127.0.0.1:8765/mcp");
});

test("claude-desktop stdio snippet carries binary path and vault args", () => {
  const tool = TOOLS.find((t) => t.id === "claude-desktop")!;
  const parsed = JSON.parse(tool.stdio(inputs));
  const entry = parsed.mcpServers["obsidian-work"];
  assert.equal(entry.command, inputs.binaryPath);
  assert.deepEqual(entry.args, ["-vault", inputs.vaultPath, "-name", inputs.name]);
});

test("claude-code http snippet uses claude mcp add --transport http", () => {
  const tool = TOOLS.find((t) => t.id === "claude-code")!;
  assert.equal(
    tool.http(inputs),
    "claude mcp add --transport http obsidian-work http://127.0.0.1:8765/mcp",
  );
});

test("claude-code stdio snippet uses -- separator", () => {
  const tool = TOOLS.find((t) => t.id === "claude-code")!;
  assert.equal(
    tool.stdio(inputs),
    "claude mcp add obsidian-work -- /usr/local/bin/obsidian-graph-mcp -vault /home/user/notes -name obsidian-work",
  );
});

test("opencode http snippet uses type remote", () => {
  const tool = TOOLS.find((t) => t.id === "opencode")!;
  const parsed = JSON.parse(tool.http(inputs));
  assert.equal(parsed.mcp["obsidian-work"].type, "remote");
  assert.equal(parsed.mcp["obsidian-work"].url, "http://127.0.0.1:8765/mcp");
});

test("opencode stdio snippet uses type local with command array", () => {
  const tool = TOOLS.find((t) => t.id === "opencode")!;
  const parsed = JSON.parse(tool.stdio(inputs));
  assert.equal(parsed.mcp["obsidian-work"].type, "local");
  assert.deepEqual(parsed.mcp["obsidian-work"].command, [
    inputs.binaryPath,
    "-vault",
    inputs.vaultPath,
    "-name",
    inputs.name,
  ]);
});

test("codex http snippet is a [mcp_servers.<name>] TOML table with a url", () => {
  const tool = TOOLS.find((t) => t.id === "codex")!;
  assert.equal(
    tool.http(inputs),
    '[mcp_servers.obsidian-work]\nurl = "http://127.0.0.1:8765/mcp"',
  );
});

test("codex stdio snippet is a [mcp_servers.<name>] TOML table with command/args", () => {
  const tool = TOOLS.find((t) => t.id === "codex")!;
  assert.equal(
    tool.stdio(inputs),
    '[mcp_servers.obsidian-work]\ncommand = "/usr/local/bin/obsidian-graph-mcp"\nargs = ["-vault", "/home/user/notes", "-name", "obsidian-work"]',
  );
});

test("all tools produce both http and stdio variants for every input", () => {
  for (const tool of TOOLS) {
    assert.ok(tool.http(inputs).length > 0, `${tool.id} http empty`);
    assert.ok(tool.stdio(inputs).length > 0, `${tool.id} stdio empty`);
  }
});
