import { existsSync } from "fs";
import { join } from "path";
import { FileSystemAdapter, Notice, Plugin } from "obsidian";
import { GraphMcpSettingTab, defaultSettings, type PluginSettings } from "./settings";
import { ProcessManager } from "./process-manager";
import { ReindexWatcher } from "./reindex-watcher";
import { downloadBinary, isSupportedPlatform } from "./downloader";

export default class GraphMcpPlugin extends Plugin {
  settings!: PluginSettings;
  processManager!: ProcessManager;
  reindexWatcher!: ReindexWatcher;
  private statusBarItem!: HTMLElement;

  async onload(): Promise<void> {
    await this.loadSettings();

    this.statusBarItem = this.addStatusBarItem();

    this.processManager = new ProcessManager({
      getSettings: () => ({
        ...this.settings,
        binaryPath: this.settings.binaryPath || this.managedBinaryPath(),
      }),
      vaultPath: this.getVaultPath(),
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

    this.updateStatusBar();

    if (this.settings.autoStart) {
      const effectivePath = this.settings.binaryPath || this.managedBinaryPath();
      if (existsSync(effectivePath)) {
        await this.processManager.start();
        this.updateStatusBar();
      } else if (!this.settings.binaryPath && isSupportedPlatform()) {
        // No override set and managed binary missing — download then start.
        this.autoDownloadBinary({ thenStart: true });
      }
    }
  }

  async onunload(): Promise<void> {
    this.reindexWatcher.unregister();
    await this.processManager.stop();
  }

  async reindexNow(): Promise<void> {
    await this.processManager.reindex();
  }

  managedBinaryPath(): string {
    const adapter = this.app.vault.adapter;
    if (!(adapter instanceof FileSystemAdapter)) {
      throw new Error("graph-mcp requires the desktop file system adapter");
    }
    return join(
      adapter.getBasePath(),
      this.app.vault.configDir,
      "plugins",
      this.manifest.id,
      "bin",
      "obsidian-graph-mcp",
    );
  }

  async autoDownloadBinary({ thenStart = false } = {}): Promise<void> {
    const notice = new Notice("Graph MCP: downloading binary…", 0);
    try {
      await downloadBinary(this.manifest.version, this.managedBinaryPath());
      notice.hide();
      new Notice("Graph MCP: binary downloaded.");
      if (thenStart && this.settings.autoStart) {
        await this.processManager.start();
        this.updateStatusBar();
      }
    } catch (err) {
      notice.hide();
      const msg = err instanceof Error ? err.message : String(err);
      new Notice(`Graph MCP: download failed — ${msg}`, 8000);
      console.error("[graph-mcp] binary download failed", err);
    }
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
