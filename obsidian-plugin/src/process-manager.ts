import { ChildProcess, spawn } from "child_process";
import * as http from "http";
import * as net from "net";
import type { PluginSettings } from "./settings";

export type ServerStatus = "stopped" | "starting" | "running" | "error";

const HOST = "127.0.0.1";
const MAX_PORT_ATTEMPTS = 20;
const HEALTHZ_POLL_INTERVAL_MS = 200;
const HEALTHZ_TIMEOUT_MS = 10_000;
const LOG_TAIL_LIMIT = 50;

function findFreePort(startPort: number, maxAttempts: number): Promise<number> {
  return new Promise((resolve, reject) => {
    const tryPort = (port: number, attemptsLeft: number) => {
      const server = net.createServer();
      server.once("error", (err: NodeJS.ErrnoException) => {
        server.close();
        if (err.code === "EADDRINUSE" && attemptsLeft > 0) {
          tryPort(port + 1, attemptsLeft - 1);
        } else {
          reject(err);
        }
      });
      server.once("listening", () => {
        server.close(() => resolve(port));
      });
      server.listen(port, HOST);
    };
    tryPort(startPort, maxAttempts);
  });
}

function pollHealthz(port: number, timeoutMs: number): Promise<boolean> {
  const deadline = Date.now() + timeoutMs;
  return new Promise((resolve) => {
    const attempt = () => {
      const req = http.get({ host: HOST, port, path: "/healthz", timeout: 1000 }, (res) => {
        res.resume();
        resolve(res.statusCode === 200);
      });
      req.on("error", () => {
        if (Date.now() >= deadline) {
          resolve(false);
        } else {
          setTimeout(attempt, HEALTHZ_POLL_INTERVAL_MS);
        }
      });
      req.on("timeout", () => req.destroy());
    };
    attempt();
  });
}

function postReindex(port: number): Promise<void> {
  return new Promise((resolve, reject) => {
    const req = http.request(
      { host: HOST, port, path: "/reindex", method: "POST", timeout: 30_000 },
      (res) => {
        res.resume();
        res.on("end", () => resolve());
      },
    );
    req.on("error", reject);
    req.on("timeout", () => req.destroy());
    req.end();
  });
}

export interface ProcessManagerDeps {
  getSettings: () => PluginSettings;
  vaultPath: string;
  onResolvedPort: (port: number | null) => void | Promise<void>;
  log: (line: string) => void;
}

export class ProcessManager {
  status: ServerStatus = "stopped";
  resolvedPort: number | null = null;

  private child: ChildProcess | null = null;
  private ownsProcess = false;
  private logTail: string[] = [];
  private deps: ProcessManagerDeps;

  constructor(deps: ProcessManagerDeps) {
    this.deps = deps;
    this.resolvedPort = deps.getSettings().resolvedPort;
  }

  getLogTail(): string[] {
    return this.logTail;
  }

  getBaseUrl(): string | null {
    return this.resolvedPort ? `http://${HOST}:${this.resolvedPort}` : null;
  }

  async start(): Promise<void> {
    const settings = this.deps.getSettings();
    if (!settings.binaryPath) {
      this.status = "error";
      this.pushLog("no binary path configured");
      return;
    }

    if (settings.resolvedPort) {
      const healthy = await pollHealthz(settings.resolvedPort, 500);
      if (healthy) {
        this.resolvedPort = settings.resolvedPort;
        this.ownsProcess = false;
        this.status = "running";
        this.pushLog(`reattached to existing instance on port ${this.resolvedPort}`);
        return;
      }
    }

    this.status = "starting";
    let port: number;
    try {
      port = await findFreePort(settings.port, MAX_PORT_ATTEMPTS);
    } catch (err) {
      this.status = "error";
      this.pushLog(`no free port found starting at ${settings.port}: ${err}`);
      return;
    }

    const args = [
      "-vault",
      this.deps.vaultPath,
      "-http",
      `${HOST}:${port}`,
      "-name",
      settings.name,
    ];
    if (settings.context) {
      args.push("-context", settings.context);
    }

    const child = spawn(settings.binaryPath, args, { cwd: this.deps.vaultPath });
    this.child = child;
    this.ownsProcess = true;

    child.stderr?.on("data", (chunk: Buffer) => {
      for (const line of chunk.toString().split("\n")) {
        if (line.trim()) this.pushLog(line.trim());
      }
    });
    child.on("exit", (code) => {
      if (this.status !== "stopped") {
        this.status = "error";
        this.pushLog(`process exited unexpectedly (code ${code})`);
      }
      this.child = null;
    });

    const healthy = await pollHealthz(port, HEALTHZ_TIMEOUT_MS);
    if (!healthy) {
      this.status = "error";
      this.pushLog("timed out waiting for /healthz");
      child.kill();
      this.child = null;
      return;
    }

    this.resolvedPort = port;
    this.status = "running";
    await this.deps.onResolvedPort(port);
  }

  async stop(): Promise<void> {
    this.status = "stopped";
    if (this.child && this.ownsProcess) {
      this.child.kill();
    }
    this.child = null;
    this.ownsProcess = false;
  }

  async restart(): Promise<void> {
    await this.stop();
    await this.start();
  }

  async reindex(): Promise<void> {
    if (!this.resolvedPort) return;
    await postReindex(this.resolvedPort);
  }

  private pushLog(line: string): void {
    this.logTail.push(line);
    if (this.logTail.length > LOG_TAIL_LIMIT) this.logTail.shift();
    this.deps.log(line);
  }
}
