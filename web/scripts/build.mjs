import { createHash } from "node:crypto";
import { existsSync } from "node:fs";
import { cp, mkdir, readdir, readFile, rm, writeFile } from "node:fs/promises";

const source = new URL("../src/", import.meta.url);
const target = new URL("../../internal/web/dist/", import.meta.url);

// Windows file watchers can briefly hold the directory itself. Clear its
// entries instead of removing the directory, then recreate the known layout.
if (existsSync(target)) {
  for (const entry of await readdir(target, { withFileTypes: true })) {
    const path = new URL(entry.isDirectory() ? `${entry.name}/` : entry.name, target);
    await rm(path, { recursive: true, force: true });
  }
}
await mkdir(target, { recursive: true });
await cp(source, target, { recursive: true });

const files = [];
async function walk(dir) {
  for (const entry of await readdir(dir, { withFileTypes: true })) {
    const path = new URL(`${dir.pathname}${entry.name}${entry.isDirectory() ? "/" : ""}`, dir);
    if (entry.isDirectory()) await walk(path);
    else files.push(path);
  }
}
await walk(target);

const contentHash = createHash("sha256");
for (const file of files.sort((a, b) => String(a).localeCompare(String(b)))) {
  const bytes = await readFile(file);
  const relative = decodeURIComponent(new URL(file).href.slice(target.href.length));
  contentHash.update(relative);
  contentHash.update(new Uint8Array([0]));
  contentHash.update(bytes);
}

await writeFile(
  new URL("build-info.json", target),
  JSON.stringify({
    builtAt: new Date().toISOString(),
    buildId: contentHash.digest("hex"),
    files: files.length,
  }, null, 2) + "\n",
);
console.log(`built ${files.length} web files`);
