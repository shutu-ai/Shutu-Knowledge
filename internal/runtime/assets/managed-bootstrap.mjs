import { createHash } from "node:crypto";
import { readFile, stat, writeFile } from "node:fs/promises";
import { spawnSync } from "node:child_process";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const args = process.argv.slice(2);
function argument(name, fallback = "") {
  const index = args.indexOf(name);
  return index >= 0 && args[index + 1] ? args[index + 1] : fallback;
}

const root = path.resolve(argument("--runtime-home", path.dirname(fileURLToPath(import.meta.url))));
const lockfile = path.join(root, "package-lock.json");
const marker = path.join(root, "node_modules", ".shutu-runtime-ready");
const lockHash = createHash("sha256").update(await readFile(lockfile)).digest("hex");
let ready = false;
try {
  ready = (await readFile(marker, "utf8")).trim() === lockHash &&
    (await stat(path.join(root, "node_modules", "@huggingface", "transformers"))).isDirectory();
} catch {}

if (!ready) {
  // Use the command name on Windows: shell=true is required for npm.cmd, and
  // passing the absolute Program Files path unquoted makes cmd.exe split it.
  const npm = process.platform === "win32" ? "npm.cmd" : path.join(path.dirname(process.execPath), "npm");
  const result = spawnSync(npm, ["ci", "--prefix", root, "--ignore-scripts", "--no-audit", "--no-fund"], {
    cwd: root,
    env: { ...process.env, NPM_CONFIG_CACHE: path.join(root, "npm-cache"), CI: "true" },
    shell: process.platform === "win32",
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
  });
  if (result.error || result.status !== 0) {
    const detail = result.error?.message ?? (result.stderr || result.stdout || `exit ${result.status}`).trim();
    throw new Error(`managed runtime dependency install failed: ${detail}`);
  }
  await writeFile(marker, `${lockHash}\n`, { mode: 0o600 });
}

await import(pathToFileURL(path.join(root, "managed-runtime.mjs")).href);
