import assert from "node:assert/strict";
import { test } from "node:test";
import { assetNameForPlatform } from "./platform.ts";

test("linux x64 maps to linux-amd64 asset", () => {
  assert.equal(assetNameForPlatform("linux", "x64"), "obsidian-graph-mcp-linux-amd64");
});

test("linux arm64 maps to linux-arm64 asset", () => {
  assert.equal(assetNameForPlatform("linux", "arm64"), "obsidian-graph-mcp-linux-arm64");
});

test("darwin x64 maps to darwin-amd64 asset", () => {
  assert.equal(assetNameForPlatform("darwin", "x64"), "obsidian-graph-mcp-darwin-amd64");
});

test("darwin arm64 maps to darwin-arm64 asset", () => {
  assert.equal(assetNameForPlatform("darwin", "arm64"), "obsidian-graph-mcp-darwin-arm64");
});

test("unsupported platform returns null", () => {
  assert.equal(assetNameForPlatform("win32", "x64"), null);
});

test("unknown arch on known platform returns null", () => {
  assert.equal(assetNameForPlatform("linux", "ia32"), null);
});
