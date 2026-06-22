export function assetNameForPlatform(platform: string, arch: string): string | null {
  if (platform === "linux" && arch === "x64") return "obsidian-graph-mcp-linux-amd64";
  if (platform === "linux" && arch === "arm64") return "obsidian-graph-mcp-linux-arm64";
  if (platform === "darwin" && arch === "x64") return "obsidian-graph-mcp-darwin-amd64";
  if (platform === "darwin" && arch === "arm64") return "obsidian-graph-mcp-darwin-arm64";
  return null;
}

export function getPlatformAssetName(): string | null {
  return assetNameForPlatform(process.platform, process.arch);
}

export function isSupportedPlatform(): boolean {
  return getPlatformAssetName() !== null;
}
