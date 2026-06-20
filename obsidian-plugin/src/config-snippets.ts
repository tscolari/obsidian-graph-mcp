// Pure functions turning current plugin settings into copy-paste config
// snippets for the coding tools this plugin supports. Each tool gets two
// variants: against the long-lived HTTP instance this plugin supervises, and
// a one-off stdio invocation for tools/configs that spawn their own process.

export interface SnippetInputs {
  name: string;
  port: number;
  binaryPath: string;
  vaultPath: string;
}

export interface ToolSnippets {
  id: string;
  label: string;
  http(i: SnippetInputs): string;
  stdio(i: SnippetInputs): string;
}

function httpUrl(i: SnippetInputs): string {
  return `http://127.0.0.1:${i.port}/mcp`;
}

const claudeDesktop: ToolSnippets = {
  id: "claude-desktop",
  label: "Claude Desktop",
  http: (i) =>
    JSON.stringify(
      { mcpServers: { [i.name]: { type: "http", url: httpUrl(i) } } },
      null,
      2,
    ),
  stdio: (i) =>
    JSON.stringify(
      {
        mcpServers: {
          [i.name]: {
            command: i.binaryPath,
            args: ["-vault", i.vaultPath, "-name", i.name],
          },
        },
      },
      null,
      2,
    ),
};

const claudeCode: ToolSnippets = {
  id: "claude-code",
  label: "Claude Code (CLI)",
  http: (i) => `claude mcp add --transport http ${i.name} ${httpUrl(i)}`,
  stdio: (i) =>
    `claude mcp add ${i.name} -- ${i.binaryPath} -vault ${i.vaultPath} -name ${i.name}`,
};

const opencode: ToolSnippets = {
  id: "opencode",
  label: "OpenCode",
  http: (i) =>
    JSON.stringify(
      { mcp: { [i.name]: { type: "remote", url: httpUrl(i) } } },
      null,
      2,
    ),
  stdio: (i) =>
    JSON.stringify(
      {
        mcp: {
          [i.name]: {
            type: "local",
            command: [i.binaryPath, "-vault", i.vaultPath, "-name", i.name],
          },
        },
      },
      null,
      2,
    ),
};

const codex: ToolSnippets = {
  id: "codex",
  label: "Codex",
  http: (i) => `[mcp_servers.${i.name}]\nurl = "${httpUrl(i)}"`,
  stdio: (i) =>
    `[mcp_servers.${i.name}]\ncommand = "${i.binaryPath}"\nargs = ["-vault", "${i.vaultPath}", "-name", "${i.name}"]`,
};

export const TOOLS: ToolSnippets[] = [claudeDesktop, claudeCode, opencode, codex];
