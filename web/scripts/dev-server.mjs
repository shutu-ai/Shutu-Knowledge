import { createServer } from "node:http";
import { readFile } from "node:fs/promises";

const types = {
  ".css": "text/css; charset=utf-8",
  ".html": "text/html; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".json": "application/json; charset=utf-8",
  ".svg": "image/svg+xml",
};

createServer(async (request, response) => {
  const path = request.url === "/" ? "/index.html" : request.url.split("?")[0];
  try {
    const file = await readFile(new URL(`../../internal/web/dist${path}`, import.meta.url));
    response.writeHead(200, { "content-type": types[path.slice(path.lastIndexOf("."))] ?? "application/octet-stream" });
    response.end(file);
  } catch {
    const file = await readFile(new URL("../../internal/web/dist/index.html", import.meta.url));
    response.writeHead(200, { "content-type": types[".html"] });
    response.end(file);
  }
}).listen(process.env.PORT ?? 5173, "127.0.0.1", () => {
  console.log(`web dev server: http://127.0.0.1:${process.env.PORT ?? 5173}`);
});
