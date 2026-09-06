import { cp, mkdir, readdir, readFile, rm, writeFile } from "node:fs/promises";

const source = new URL("../src/", import.meta.url);
const target = new URL("../../internal/web/dist/", import.meta.url);

await rm(target, { recursive: true, force: true });
await mkdir(target, { recursive: true });
await cp(source, target, { recursive: true });

const files = [];
async function walk(dir) {
  for (const entry of await readdir(dir, { withFileTypes: true })) {
    const path = new URL(`${dir.pathname}${entry.name}${entry.isDirectory() ? "/" : ""}`, dir);
    if (entry.isDirectory()) await walk(path);
    else files.push(path.pathname);
  }
}
await walk(target);

await writeFile(
  new URL("build-info.json", target),
  JSON.stringify({ builtAt: new Date().toISOString(), files: files.length }, null, 2) + "\n",
);
console.log(`built ${files.length} web files`);
