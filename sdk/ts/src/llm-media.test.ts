// URL media fetches are guarded against SSRF: http(s) only, public
// addresses only (checked on what is dialed), no redirects, a size cap,
// and the offline policy.

import { createServer, type Server } from "node:http";
import type { AddressInfo } from "node:net";

import { afterEach, describe, expect, it } from "vitest";

import { urlSource } from "./llm";
import { HTTPStatusError } from "./llm-errors";
import {
  DEFAULT_MAX_MEDIA_BYTES,
  MediaTooLargeError,
  MediaURLRefusedError,
  _setMediaFetchSeams,
  isPublicAddress,
} from "./llm-media";
import { isOfflineError, withOffline } from "./netpolicy";

type Handler = Parameters<typeof createServer>[1];

let restore: (() => void)[] = [];
let servers: Server[] = [];

afterEach(async () => {
  for (const r of restore.reverse()) r();
  restore = [];
  await Promise.all(
    servers.map((s) => {
      s.closeAllConnections();
      return new Promise((r) => s.close(r));
    }),
  );
  servers = [];
});

function allowLoopback(): void {
  restore.push(
    _setMediaFetchSeams({
      allow: (a, isPublic) => /^127\./.test(a) || a === "::1" || isPublic(a),
    }),
  );
}

function fakeResolve(answers: Record<string, string[]>, count?: { n: number }): void {
  restore.push(
    _setMediaFetchSeams({
      resolve: async (host) => {
        if (count) count.n++;
        const a = answers[host];
        if (!a) throw new Error(`no such host ${host}`);
        return a;
      },
    }),
  );
}

async function serve(h: NonNullable<Handler>): Promise<{ url: string; port: number; hits: () => number }> {
  let n = 0;
  const srv = createServer((req, res) => {
    n++;
    h(req, res);
  });
  servers.push(srv);
  await new Promise<void>((r) => srv.listen(0, "127.0.0.1", r));
  const port = (srv.address() as AddressInfo).port;
  return { url: `http://127.0.0.1:${port}`, port, hits: () => n };
}

describe("urlSource guard", () => {
  it.each(["file:///etc/passwd", "ftp://example.com/x.png", "data:image/png;base64,AAAA", "gopher://example.com/x"])(
    "refuses scheme %s",
    async (url) => {
      const err = await urlSource(url).read().catch((e) => e);
      expect(err).toBeInstanceOf(MediaURLRefusedError);
      expect(String(err.message)).toContain("only http or https URLs are fetched");
    },
  );

  it.each([
    "http://127.0.0.1/x.png",
    "http://localhost/x.png",
    "http://[::1]/x.png",
    "http://169.254.169.254/latest/meta-data/",
    "http://10.0.0.1/x.png",
    "http://192.168.1.1/x.png",
    "http://172.16.0.1/x.png",
    "http://0.0.0.0/x.png",
    "http://[::ffff:127.0.0.1]/x.png",
    "http://[::ffff:169.254.169.254]/x.png",
    "http://[fc00::1]/x.png",
    "http://[fd12:3456::1]/x.png",
    "http://[fe80::1]/x.png",
    "http://224.0.0.1/x.png",
    "http://100.64.0.1/x.png",
    "http://[64:ff9b::a9fe:a9fe]/x.png",
  ])("refuses non-public %s", async (url) => {
    const err = await urlSource(url).read().catch((e) => e);
    expect(err).toBeInstanceOf(MediaURLRefusedError);
    expect(String(err.message)).toContain("non-public address");
  });

  it("names the destination without query", async () => {
    const err = await urlSource("http://169.254.169.254/latest/meta-data/?X-Amz-Signature=sekrit")
      .read()
      .catch((e) => e);
    expect(err.message).toBe(
      "media: refusing to fetch http://169.254.169.254/latest/meta-data/: non-public address 169.254.169.254",
    );
  });

  it("refuses userinfo without echoing it", async () => {
    const err = await urlSource("https://user:pw@example.com/x.png").read().catch((e) => e);
    expect(err).toBeInstanceOf(MediaURLRefusedError);
    expect(err.message).toContain("userinfo");
    expect(err.message).not.toContain("pw");
  });

  it("refuses a loopback server by default, before any request", async () => {
    const srv = await serve((_q, s) => s.end("PNG"));
    const err = await urlSource(`${srv.url}/x.png`).read().catch((e) => e);
    expect(err).toBeInstanceOf(MediaURLRefusedError);
    expect(srv.hits()).toBe(0);
  });

  it.each([
    ["private", ["10.1.2.3"]],
    ["metadata", ["169.254.169.254"]],
    ["loopback v6", ["::1"]],
    ["mapped", ["::ffff:192.168.0.1"]],
    ["one of many", ["93.184.216.34", "127.0.0.1"]],
    ["ula", ["fd00::5"]],
    ["no addresses", []],
  ])("refuses a name resolving to %s", async (_n, addrs) => {
    fakeResolve({ "media.example": addrs as string[] });
    const err = await urlSource("http://media.example/x.png").read().catch((e) => e);
    expect(err).toBeInstanceOf(MediaURLRefusedError);
    expect(err.message).toContain("media: refusing to fetch http://media.example/x.png");
  });

  it("dials the vetted address: one lookup, no rebinding", async () => {
    allowLoopback();
    let host = "";
    const srv = await serve((q, s) => {
      host = q.headers.host ?? "";
      s.end("PNG");
    });
    const count = { n: 0 };
    fakeResolve({ "media.example": ["127.0.0.1"] }, count);
    const got = await urlSource(`http://media.example:${srv.port}/x.png`).read();
    expect(Buffer.from(got).toString()).toBe("PNG");
    expect(host).toBe(`media.example:${srv.port}`);
    expect(count.n).toBe(1);
  });

  it("fetches a public host", async () => {
    allowLoopback();
    let path = "";
    const srv = await serve((q, s) => {
      path = q.url ?? "";
      s.setHeader("content-type", "image/png");
      s.end("PNG");
    });
    const got = await urlSource(`${srv.url}/x.png`).read();
    expect(Buffer.from(got).toString()).toBe("PNG");
    expect(path).toBe("/x.png");
  });

  it("does not follow a redirect", async () => {
    allowLoopback();
    const srv = await serve((_q, s) => {
      s.writeHead(302, { location: "http://169.254.169.254/latest/meta-data/" });
      s.end();
    });
    const err = await urlSource(`${srv.url}/x.png`).read().catch((e) => e);
    expect(err).toBeInstanceOf(HTTPStatusError);
    expect(err.statusCode).toBe(302);
    expect(err.message).toContain("redirect not followed");
    expect(srv.hits()).toBe(1);
  });

  it("fails on an error status", async () => {
    allowLoopback();
    const srv = await serve((_q, s) => {
      s.writeHead(404);
      s.end("nope");
    });
    const err = await urlSource(`${srv.url}/x.png`).read().catch((e) => e);
    expect(err.message).toContain("http 404");
  });

  it.each([
    ["declared length", false, 2048],
    ["chunked", true, 1025],
  ])("caps the body (%s)", async (_n, chunked, size) => {
    allowLoopback();
    const body = "x".repeat(2048);
    const srv = await serve((_q, s) => {
      if (!chunked) {
        // Declared over the cap and never finished: only the early
        // check on the declared length can fail it in time.
        s.writeHead(200, { "content-length": String(body.length) });
        s.write(body.slice(0, 16));
        return;
      }
      for (let i = 0; i < 4; i++) s.write(body.slice(0, 512));
      s.end();
    });
    const err = await urlSource(`${srv.url}/x.png`, { maxBytes: 1024 }).read().catch((e) => e);
    expect(err).toBeInstanceOf(MediaTooLargeError);
    expect(err.limit).toBe(1024);
    if (!chunked) expect(err.size).toBe(size);
    else expect(err.size).toBeGreaterThan(1024);
  });

  it("delivers a body exactly at the cap", async () => {
    allowLoopback();
    const srv = await serve((_q, s) => s.end("x".repeat(1024)));
    const got = await urlSource(`${srv.url}/x.png`, { maxBytes: 1024 }).read();
    expect(got.length).toBe(1024);
  });

  it("defaults to 32 MiB", () => {
    expect(DEFAULT_MAX_MEDIA_BYTES).toBe(32 * 1024 * 1024);
  });

  it("honours --offline", async () => {
    fakeResolve({ "media.example": ["93.184.216.34"] });
    const err = await withOffline(true, () => urlSource("https://media.example/x.png").read()).catch((e) => e);
    expect(isOfflineError(err)).toBe(true);
  });
});

describe("isPublicAddress", () => {
  it.each([
    ["93.184.216.34", true],
    ["8.8.8.8", true],
    ["2606:4700::1111", true],
    ["127.0.0.1", false],
    ["10.0.0.1", false],
    ["172.31.255.255", false],
    ["192.168.0.1", false],
    ["169.254.169.254", false],
    ["100.64.0.1", false],
    ["0.0.0.0", false],
    ["0.1.2.3", false],
    ["192.0.2.1", false],
    ["198.18.0.1", false],
    ["198.51.100.1", false],
    ["203.0.113.1", false],
    ["224.0.0.1", false],
    ["240.0.0.1", false],
    ["255.255.255.255", false],
    ["::", false],
    ["::1", false],
    ["::ffff:127.0.0.1", false],
    ["::ffff:10.0.0.1", false],
    ["::ffff:100.64.0.1", false],
    ["::ffff:192.0.2.1", false],
    ["::ffff:93.184.216.34", true],
    ["::127.0.0.1", false],
    ["fc00::1", false],
    ["fdff::1", false],
    ["fe80::1", false],
    ["fec0::1", false],
    ["ff02::1", false],
    ["2001:db8::1", false],
    ["2002:7f00:1::1", false],
    ["64:ff9b::7f00:1", false],
    ["64:ff9b::5db8:d822", true],
    ["64:ff9b:1::1", false],
    ["100::1", false],
    ["fe80::1%en0", false],
    ["not-an-address", false],
  ])("%s -> %s", (addr, want) => {
    expect(isPublicAddress(addr)).toBe(want);
  });
});
