import { App, Notice, PluginSettingTab, Setting } from "obsidian";
import type GraphMcpPlugin from "./main";
import { TOOLS, type SnippetInputs } from "./config-snippets";

export interface PluginSettings {
  binaryPath: string;
  port: number;
  resolvedPort: number | null;
  name: string;
  context: string;
  autoStart: boolean;
  reindexDebounceMs: number;
}

export function defaultSettings(vaultFolderName: string): PluginSettings {
  return {
    binaryPath: "",
    port: 8765,
    resolvedPort: null,
    name: `obsidian-graph-${vaultFolderName}`,
    context: "",
    autoStart: true,
    reindexDebounceMs: 1500,
  };
}

export class GraphMcpSettingTab extends PluginSettingTab {
  plugin: GraphMcpPlugin;

  constructor(app: App, plugin: GraphMcpPlugin) {
    super(app, plugin);
    this.plugin = plugin;
  }

  display(): void {
    this.containerEl.empty();
    this.renderConfig();
    this.renderStatus();
    this.renderToolSnippets();
  }

  hide(): void {
    this.containerEl.empty();
  }

  private renderConfig(): void {
    const { containerEl, plugin } = this;
    containerEl.createEl("h2", { text: "Graph MCP" });

    new Setting(containerEl)
      .setName("Binary path")
      .setDesc("Absolute path to the obsidian-graph-mcp executable.")
      .addText((text) =>
        text
          .setPlaceholder("/usr/local/bin/obsidian-graph-mcp")
          .setValue(plugin.settings.binaryPath)
          .onChange(async (value) => {
            plugin.settings.binaryPath = value.trim();
            await plugin.saveSettings();
          }),
      );

    new Setting(containerEl)
      .setName("Port")
      .setDesc(
        "Preferred port for the HTTP MCP endpoint. If taken, the plugin tries the next ones up.",
      )
      .addText((text) =>
        text
          .setPlaceholder("8765")
          .setValue(String(plugin.settings.port))
          .onChange(async (value) => {
            const n = parseInt(value, 10);
            if (!Number.isNaN(n) && n > 0 && n < 65536) {
              plugin.settings.port = n;
              await plugin.saveSettings();
            }
          }),
      );

    new Setting(containerEl)
      .setName("Server name")
      .setDesc(
        "Namespaces the MCP tools (mcp__<name>__search_notes) so an agent can tell vaults apart.",
      )
      .addText((text) =>
        text
          .setValue(plugin.settings.name)
          .onChange(async (value) => {
            plugin.settings.name = value.trim();
            await plugin.saveSettings();
          }),
      );

    new Setting(containerEl)
      .setName("Context")
      .setDesc("One-line description of what this vault holds, advertised to the agent.")
      .addTextArea((text) =>
        text
          .setPlaceholder("Current job: incidents, projects, people, decisions")
          .setValue(plugin.settings.context)
          .onChange(async (value) => {
            plugin.settings.context = value;
            await plugin.saveSettings();
          }),
      );

    new Setting(containerEl)
      .setName("Auto-start")
      .setDesc("Start the MCP server automatically when this vault opens.")
      .addToggle((toggle) =>
        toggle.setValue(plugin.settings.autoStart).onChange(async (value) => {
          plugin.settings.autoStart = value;
          await plugin.saveSettings();
        }),
      );

    new Setting(containerEl)
      .setName("Reindex debounce (ms)")
      .setDesc(
        "How long to wait after the last file change before triggering a reindex.",
      )
      .addText((text) =>
        text
          .setValue(String(plugin.settings.reindexDebounceMs))
          .onChange(async (value) => {
            const n = parseInt(value, 10);
            if (!Number.isNaN(n) && n >= 0) {
              plugin.settings.reindexDebounceMs = n;
              await plugin.saveSettings();
            }
          }),
      );
  }

  private renderStatus(): void {
    const { containerEl, plugin } = this;
    containerEl.createEl("h3", { text: "Server status" });

    const status = plugin.processManager.status;
    const port = plugin.processManager.resolvedPort;
    const statusText =
      status === "running"
        ? `Running on port ${port}`
        : status === "starting"
          ? "Starting…"
          : status === "error"
            ? `Error — ${plugin.processManager.getLogTail().slice(-1)[0] ?? "see console"}`
            : "Stopped";

    new Setting(containerEl)
      .setName("Status")
      .setDesc(statusText)
      .addButton((btn) =>
        btn.setButtonText("Start").onClick(async () => {
          if (!plugin.settings.binaryPath) {
            new Notice("Set the binary path first.");
            return;
          }
          await plugin.processManager.start();
          this.display();
        }),
      )
      .addButton((btn) =>
        btn.setButtonText("Stop").onClick(async () => {
          await plugin.processManager.stop();
          this.display();
        }),
      )
      .addButton((btn) =>
        btn.setButtonText("Restart").onClick(async () => {
          await plugin.processManager.restart();
          this.display();
        }),
      )
      .addButton((btn) =>
        btn.setButtonText("Reindex now").onClick(async () => {
          await plugin.reindexNow();
          new Notice("Reindex triggered.");
        }),
      );
  }

  private renderToolSnippets(): void {
    const { containerEl, plugin } = this;
    containerEl.createEl("h3", { text: "Wire into your coding tool" });
    containerEl.createEl("p", {
      text:
        "HTTP snippets point at the long-lived instance this plugin manages. " +
        "Stdio snippets are one-off invocations for tools that spawn their own process.",
    });

    const vaultPath = plugin.getVaultPath();
    const inputs: SnippetInputs = {
      name: plugin.settings.name,
      port: plugin.processManager.resolvedPort ?? plugin.settings.port,
      binaryPath: plugin.settings.binaryPath || "<path to obsidian-graph-mcp>",
      vaultPath,
    };

    for (const tool of TOOLS) {
      containerEl.createEl("h4", { text: tool.label });

      const renderSnippet = (label: string, code: string) => {
        const wrap = containerEl.createDiv();
        wrap.createEl("strong", { text: label });
        const pre = wrap.createEl("pre");
        pre.createEl("code", { text: code });
        const copyBtn = wrap.createEl("button", { text: "Copy" });
        copyBtn.onclick = async () => {
          await navigator.clipboard.writeText(code);
          new Notice(`${tool.label} (${label}) config copied.`);
        };
      };

      renderSnippet("HTTP", tool.http(inputs));
      renderSnippet("stdio", tool.stdio(inputs));
    }
  }
}
