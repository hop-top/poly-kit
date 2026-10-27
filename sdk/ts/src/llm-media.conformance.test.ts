// Runs the media_fetch section of contracts/llm-v1/vectors.json: what a
// URL media source refuses to download. Go is canonical; a failure here
// means this SDK drifted, not that a vector should change.

import { afterEach, describe, expect, it } from "vitest";

import { HTTPStatusError, LLMError, urlSource } from "./llm";
import { MediaTooLargeError, MediaURLRefusedError, _setMediaFetchSeams } from "./llm-media";
import { fakeServer, loadVectors, strictKeys, type FakeServer } from "../test/llm/conformance-helpers";

interface MediaFetchVector {
  name: string;
  url: string;
  max_bytes?: number;
  server?: {
    status: number;
    headers?: Record<string, string>;
    body?: string;
    body_size?: number;
  };
  error_kind?: string;
  message?: string;
  want_body?: string;
  want_size?: number;
  attempts?: number;
}

const vectors = loadVectors<{ media_fetch: MediaFetchVector[] }>().media_fetch;

function kind(e: unknown): string {
  if (e instanceof MediaURLRefusedError) return "refused";
  if (e instanceof MediaTooLargeError) return "too_large";
  if (e instanceof HTTPStatusError && e.statusCode! >= 300 && e.statusCode! < 400) return "redirect";
  if (e instanceof LLMError && e.message.includes(": http ")) return "http_status";
  return `other: ${String(e)}`;
}

let srv: FakeServer | undefined;
let restore: (() => void) | undefined;

afterEach(async () => {
  restore?.();
  restore = undefined;
  await srv?.close();
  srv = undefined;
});

describe("llm-v1 conformance: media_fetch", () => {
  it("has vectors", () => expect(vectors.length).toBeGreaterThan(0));

  it.each(vectors.map((v) => [v.name, v] as const))("%s", async (_n, v) => {
    strictKeys(v, [
      "name", "url", "max_bytes", "server", "error_kind", "message", "want_body", "want_size", "attempts",
    ], "media_fetch");
    let url = v.url;
    if (v.server) {
      const s = v.server;
      strictKeys(s, ["status", "headers", "body", "body_size"], "server");
      expect(url.startsWith("/"), "a served url is a path").toBe(true);
      srv = await fakeServer((_hit, res) => {
        res.writeHead(s.status, s.headers ?? {});
        res.end(s.body_size ? "x".repeat(s.body_size) : (s.body ?? ""));
      });
      url = srv.url + url;
      // The loopback server stands in for a public host; every other
      // check stays.
      restore = _setMediaFetchSeams({
        allow: (a, isPublic) => /^127\./.test(a) || a === "::1" || isPublic(a),
      });
    }
    const src = urlSource(url, v.max_bytes ? { maxBytes: v.max_bytes } : {});
    if (v.error_kind) {
      const err = await src.read().then(
        () => undefined,
        (e: unknown) => e,
      );
      expect(err, "want an error").toBeDefined();
      expect(kind(err)).toBe(v.error_kind);
      if (v.message) expect((err as Error).message).toBe(v.message);
    } else {
      const got = await src.read();
      if (v.want_body) expect(Buffer.from(got).toString()).toBe(v.want_body);
      if (v.want_size) expect(got.length).toBe(v.want_size);
    }
    if (v.attempts !== undefined) expect(srv?.hits.length).toBe(v.attempts);
  });
});
