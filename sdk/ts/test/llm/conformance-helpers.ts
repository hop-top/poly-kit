/**
 * Shared helpers for the llm-v1 conformance runners: the vector file,
 * a loopback HTTP server and env isolation. Test-only.
 *
 * @internal
 */

import { existsSync, readFileSync } from "node:fs";
import { createServer, type IncomingMessage, type ServerResponse } from "node:http";
import type { AddressInfo } from "node:net";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

/** Loads contracts/llm-v1/vectors.json from the repo. */
export function loadVectors<T>(): T {
  let dir = dirname(fileURLToPath(import.meta.url));
  for (let i = 0; i < 10; i++) {
    const p = resolve(dir, "contracts/llm-v1/vectors.json");
    if (existsSync(p)) return JSON.parse(readFileSync(p, "utf8")) as T;
    dir = dirname(dir);
  }
  throw new Error("contracts/llm-v1/vectors.json not found");
}

export interface Hit {
  method: string;
  url: string;
  headers: IncomingMessage["headers"];
  body: string;
}

export interface FakeServer {
  url: string;
  hits: Hit[];
  close(): Promise<void>;
}

/** A loopback server answering every request with `handler`. */
export async function fakeServer(
  handler: (hit: Hit, res: ServerResponse, n: number) => void
): Promise<FakeServer> {
  const hits: Hit[] = [];
  const srv = createServer((req, res) => {
    const chunks: Buffer[] = [];
    req.on("data", (c: Buffer) => chunks.push(c));
    req.on("end", () => {
      const hit: Hit = {
        method: req.method ?? "",
        url: req.url ?? "",
        headers: req.headers,
        body: Buffer.concat(chunks).toString("utf8"),
      };
      hits.push(hit);
      handler(hit, res, hits.length - 1);
    });
  });
  await new Promise<void>((r) => srv.listen(0, "127.0.0.1", r));
  const { port } = srv.address() as AddressInfo;
  return {
    url: `http://127.0.0.1:${port}`,
    hits,
    close: () =>
      new Promise<void>((r) => {
        srv.closeAllConnections();
        srv.close(() => r());
      }),
  };
}

/** Writes `body` with `contentType` and status 200. */
export function reply(res: ServerResponse, contentType: string, body: string, status = 200): void {
  res.writeHead(status, { "content-type": contentType });
  res.end(body);
}

/** Env vars a runner clears before each vector and restores after. */
export const LLM_ENV = [
  "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL",
  "ANTHROPIC_WORKSPACE_ID", "LLM_BASE_URL", "LLM_API_KEY", "LLM_PROVIDER",
  "LLM_FALLBACK", "OPENAI_API_KEY", "ROUTELLM_BASE_URL",
];

/** Snapshots `keys`, clears them, and returns a restore function. */
export function isolateEnv(keys: string[] = LLM_ENV): () => void {
  const saved = new Map(keys.map((k) => [k, process.env[k]]));
  for (const k of keys) delete process.env[k];
  return () => {
    for (const [k, v] of saved) {
      if (v === undefined) delete process.env[k];
      else process.env[k] = v;
    }
  };
}

/** Fails on any key of `obj` outside `allowed`: vectors are strict. */
export function strictKeys(obj: object, allowed: string[], where: string): void {
  for (const k of Object.keys(obj)) {
    if (!allowed.includes(k)) throw new Error(`${where}: unknown field ${JSON.stringify(k)}`);
  }
}
