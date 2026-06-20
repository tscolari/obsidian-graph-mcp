import { FileSystemAdapter, Plugin } from "obsidian";
import { GraphMcpSettingTab, defaultSettings, type PluginSettings } from "./settings";
import { ProcessManager } from "./process-manager";
import { ReindexWatcher } from "./reindex-watcher";

export default class GraphMcpPlugin extends Plugin {
  settings!: PluginSettings;
  processManager!: ProcessManager;
  reindexWatcher!: ReindexWatcher;
  private statusBarItem!: HTMLElement;

  async onload(): Promise<void> {
    await this.loadSettings();

    this.statusBarItem = this.addStatusBarItem();
    this.updateStatusBar();

    this.processManager = new ProcessManager({
      getSettings: () => this.settings,
      vaultPath: this.getVaultPath(),
      onResolvedPort: async (port) => {
        this.settings.resolvedPort = port;
        await this.saveSettings();
        this.updateStatusBar();
      },
      log: (line) => {
        console.log(`[graph-mcp] ${line}`);
        this.updateStatusBar();
      },
    });

    this.reindexWatcher = new ReindexWatcher({
      vault: this.app.vault,
      getDebounceMs: () => this.settings.reindexDebounceMs,
      isRunning: () => this.processManager.status === "running",
      triggerReindex: () => this.processManager.reindex(),
      onError: (err) => console.error("[graph-mcp] reindex failed", err),
    });
    this.reindexWatcher.register();

    this.addSettingTab(new GraphMcpSettingTab(this.app, this));

    this.addCommand({
      id: "graph-mcp-start",
      name: "Start MCP server",
      callback: async () => {
        await this.processManager.start();
        this.updateStatusBar();
      },
    });
    this.addCommand({
      id: "graph-mcp-stop",
      name: "Stop MCP server",
      callback: async () => {
        await this.processManager.stop();
        this.updateStatusBar();
      },
    });
    this.addCommand({
      id: "graph-mcp-restart",
      name: "Restart MCP server",
      callback: async () => {
        await this.processManager.restart();
        this.updateStatusBar();
      },
    });
    this.addCommand({
      id: "graph-mcp-reindex",
      name: "Reindex now",
      callback: () => this.reindexNow(),
    });

    if (this.settings.autoStart && this.settings.binaryPath) {
      await this.processManager.start();
      this.updateStatusBar();
    }
  }

  async onunload(): Promise<void> {
    this.reindexWatcher.unregister();
    await this.processManager.stop();
  }

  async reindexNow(): Promise<void> {
    await this.processManager.reindex();
  }

  getVaultPath(): string {
    const adapter = this.app.vault.adapter;
    if (adapter instanceof FileSystemAdapter) {
      return adapter.getBasePath();
    }
    throw new Error("graph-mcp requires the desktop file system adapter");
  }

  async loadSettings(): Promise<void> {
    const vaultFolderName = this.app.vault.getName();
    this.settings = Object.assign(defaultSettings(vaultFolderName), await this.loadData());
  }

  async saveSettings(): Promise<void> {
    await this.saveData(this.settings);
  }

  private updateStatusBar(): void {
    const status = this.processManager.status;
    const port = this.processManager.resolvedPort;
    const text =
      status === "running"
        ? `MCP: running :${port}`
        : status === "starting"
          ? "MCP: starting…"
          : status === "error"
            ? "MCP: error"
            : "MCP: stopped";
    this.statusBarItem.setText(text);
  }
}
