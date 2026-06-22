import { requestUrl } from "obsidian";
import { chmod, mkdir, writeFile } from "fs/promises";
import { dirname } from "path";
import { getPlatformAssetName } from "./platform";

export { assetNameForPlatform, getPlatformAssetName, isSupportedPlatform } from "./platform";

const REPO = "tscolari/obsidian-graph-mcp";

export async function downloadBinary(version: string, destPath: string): Promise<void> {
  const asset = getPlatformAssetName();
  if (!asset) {
    throw new Error(`Unsupported platform: ${process.platform}/${process.arch}`);
  }
  const url = `https://github.com/${REPO}/releases/download/${version}/${asset}`;
  const res = await requestUrl({ url });
  await mkdir(dirname(destPath), { recursive: true });
  await writeFile(destPath, Buffer.from(res.arrayBuffer));
  await chmod(destPath, 0o755);
}
