import { TAbstractFile, Vault } from "obsidian";

export interface ReindexWatcherDeps {
  vault: Vault;
  getDebounceMs: () => number;
  isRunning: () => boolean;
  triggerReindex: () => Promise<void>;
  onError: (err: unknown) => void;
}

// Debounces Obsidian's own file-change events into a single /reindex call,
// so the Go binary never needs its own fsnotify watch loop.
export class ReindexWatcher {
  private deps: ReindexWatcherDeps;
  private timer: ReturnType<typeof setTimeout> | null = null;
  private disposers: (() => void)[] = [];

  constructor(deps: ReindexWatcherDeps) {
    this.deps = deps;
  }

  register(): void {
    const handler = (file: TAbstractFile) => {
      if (!file.path.endsWith(".md")) return;
      this.schedule();
    };
    for (const event of ["modify", "delete", "rename"] as const) {
      // @ts-expect-error -- 'rename' has a different signature, file param is still first arg
      const ref = this.deps.vault.on(event, handler);
      this.disposers.push(() => this.deps.vault.offref(ref));
    }
  }

  unregister(): void {
    if (this.timer) clearTimeout(this.timer);
    this.timer = null;
    for (const dispose of this.disposers) dispose();
    this.disposers = [];
  }

  private schedule(): void {
    if (!this.deps.isRunning()) return;
    if (this.timer) clearTimeout(this.timer);
    this.timer = setTimeout(() => {
      this.deps.triggerReindex().catch(this.deps.onError);
    }, this.deps.getDebounceMs());
  }
}
