import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { createRequire } from "module";
import { EventEmitter } from "events";
import { Readable } from "stream";
import { createHash } from "crypto";
import { gzipSync } from "zlib";
import * as fs from "fs";
import * as os from "os";
import { join } from "path";

// Load through Node's own require so the script's `require.main` guard holds
// and it shares the real `https` module object with the tests.
const nodeRequire = createRequire(import.meta.url);
const install = nodeRequire("../scripts/install.js");
const https = nodeRequire("https");

const ARCHIVE = "kit_linux_amd64.tar.gz";
const BINARY = Buffer.from("#!/bin/sh\necho kit\n");

// --- archive builders --------------------------------------------------------

type TarEntry = { name: string; data?: Buffer; type?: string; linkname?: string };

function tarHeader(e: TarEntry, size: number): Buffer {
  const h = Buffer.alloc(512);
  h.write(e.name, 0, 100, "utf8");
  h.write("0000755\0", 100, "ascii");
  h.write("0000000\0", 108, "ascii");
  h.write("0000000\0", 116, "ascii");
  h.write(size.toString(8).padStart(11, "0") + "\0", 124, "ascii");
  h.write("00000000000\0", 136, "ascii");
  h.write(e.type ?? "0", 156, "ascii");
  if (e.linkname) h.write(e.linkname, 157, 100, "utf8");
  h.write("ustar\0", 257, "ascii");
  h.write("00", 263, "ascii");
  h.fill(" ", 148, 156);
  let sum = 0;
  for (const b of h) sum += b;
  h.write(sum.toString(8).padStart(6, "0") + "\0 ", 148, "ascii");
  return h;
}

function tarGz(entries: TarEntry[]): Buffer {
  const blocks: Buffer[] = [];
  for (const e of entries) {
    const data = e.data ?? Buffer.alloc(0);
    blocks.push(tarHeader(e, data.length), data);
    const pad = (512 - (data.length % 512)) % 512;
    blocks.push(Buffer.alloc(pad));
  }
  blocks.push(Buffer.alloc(1024));
  return gzipSync(Buffer.concat(blocks));
}

function sha256(buf: Buffer): string {
  return createHash("sha256").update(buf).digest("hex");
}

// --- install harness ---------------------------------------------------------

let tmp: string;
let binDir: string;

beforeEach(() => {
  tmp = fs.mkdtempSync(join(os.tmpdir(), "kit-install-test-"));
  binDir = join(tmp, "bin");
  vi.spyOn(console, "warn").mockImplementation(() => {});
  vi.spyOn(console, "error").mockImplementation(() => {});
});

afterEach(() => {
  vi.restoreAllMocks();
  fs.rmSync(tmp, { recursive: true, force: true });
});

function harness(archive: Buffer, checksums: string | Error) {
  return {
    binDir,
    version: "1.2.3",
    platform: "linux",
    platformKey: "linux_amd64",
    log: () => {},
    deps: {
      which: () => null,
      kitVersion: () => null,
      downloadFile: async (url: string, dest: string) => {
        expect(url.endsWith(`/${ARCHIVE}`)).toBe(true);
        fs.writeFileSync(dest, archive);
      },
      fetchChecksums: async () => {
        if (checksums instanceof Error) throw checksums;
        return checksums;
      },
    },
  };
}

function binDirEntries(): string[] {
  return fs.existsSync(binDir) ? fs.readdirSync(binDir) : [];
}

const goodArchive = () => tarGz([{ name: "kit", data: BINARY }]);

// --- checksum parsing --------------------------------------------------------

describe("findChecksum", () => {
  it("matches GNU two-space, single-space and path-prefixed entries", () => {
    const text = [
      "# goreleaser",
      "",
      "aaa  kit_darwin_arm64.tar.gz",
      "bbb ./dist/kit_linux_amd64.tar.gz",
    ].join("\n");
    expect(install.findChecksum(text, "kit_darwin_arm64.tar.gz")).toBe("aaa");
    expect(install.findChecksum(text, ARCHIVE)).toBe("bbb");
  });

  it("throws when the archive has no entry", () => {
    expect(() => install.findChecksum("aaa  other.tar.gz\n", ARCHIVE)).toThrow(/no checksum/);
    expect(() => install.findChecksum("", ARCHIVE)).toThrow(/no checksum/);
  });
});

// --- fetchChecksums ----------------------------------------------------------

function fakeGet(status: number, body = "") {
  return vi.spyOn(https, "get").mockImplementation((_url: unknown, cb: unknown) => {
    const res = Object.assign(Readable.from([Buffer.from(body)]), { statusCode: status, headers: {} });
    (cb as (r: unknown) => void)(res);
    return new EventEmitter();
  });
}

describe("fetchChecksums", () => {
  it("rejects on HTTP failure instead of returning nothing", async () => {
    fakeGet(404);
    await expect(install.fetchChecksums("1.2.3")).rejects.toThrow(/checksums.*HTTP 404/);
  });

  it("rejects on network error", async () => {
    vi.spyOn(https, "get").mockImplementation(() => {
      const req = new EventEmitter();
      setImmediate(() => req.emit("error", new Error("ECONNREFUSED")));
      return req;
    });
    await expect(install.fetchChecksums("1.2.3")).rejects.toThrow(/checksums.*ECONNREFUSED/);
  });

  it("returns the body on success", async () => {
    fakeGet(200, `abc  ${ARCHIVE}\n`);
    await expect(install.fetchChecksums("1.2.3")).resolves.toBe(`abc  ${ARCHIVE}\n`);
  });
});

// --- install: fail closed ----------------------------------------------------

describe("install checksum verification", () => {
  it("installs a verified binary and cleans up staging", async () => {
    const archive = goodArchive();
    const bin = await install.install(harness(archive, `${sha256(archive)}  ${ARCHIVE}\n`));
    expect(bin).toBe(join(binDir, "kit"));
    expect(fs.readFileSync(bin)).toEqual(BINARY);
    expect(binDirEntries()).toEqual(["kit"]);
  });

  it("fails closed when checksums cannot be fetched", async () => {
    const h = harness(goodArchive(), new Error("failed to fetch checksums: HTTP 404"));
    await expect(install.install(h)).rejects.toThrow(/checksums/);
    expect(binDirEntries()).toEqual([]);
  });

  it("fails closed when the archive has no checksum entry", async () => {
    const archive = goodArchive();
    const h = harness(archive, `${sha256(archive)}  kit_darwin_arm64.tar.gz\n`);
    await expect(install.install(h)).rejects.toThrow(/no checksum/);
    expect(binDirEntries()).toEqual([]);
  });

  it("fails closed on an empty checksums file", async () => {
    await expect(install.install(harness(goodArchive(), ""))).rejects.toThrow(/no checksum/);
    expect(binDirEntries()).toEqual([]);
  });

  it("fails on checksum mismatch", async () => {
    const h = harness(goodArchive(), `${sha256(Buffer.from("tampered"))}  ${ARCHIVE}\n`);
    await expect(install.install(h)).rejects.toThrow(/Checksum mismatch/);
    expect(binDirEntries()).toEqual([]);
  });

  it("leaves an existing binary untouched when verification fails", async () => {
    fs.mkdirSync(binDir, { recursive: true });
    fs.writeFileSync(join(binDir, "kit"), "old");
    const h = harness(goodArchive(), new Error("offline"));
    await expect(install.install(h)).rejects.toThrow();
    expect(fs.readFileSync(join(binDir, "kit"), "utf8")).toBe("old");
    expect(binDirEntries()).toEqual(["kit"]);
  });
});

describe("run and KIT_INSTALL_OPTIONAL", () => {
  it("exits non-zero on verification failure by default", async () => {
    const code = await install.run({}, harness(goodArchive(), new Error("offline")));
    expect(code).toBe(1);
    expect(binDirEntries()).toEqual([]);
  });

  it("tolerates the failure when optional but installs nothing", async () => {
    const h = harness(goodArchive(), `${sha256(Buffer.from("tampered"))}  ${ARCHIVE}\n`);
    const code = await install.run({ KIT_INSTALL_OPTIONAL: "1" }, h);
    expect(code).toBe(0);
    expect(binDirEntries()).toEqual([]);
  });

  it("tolerates a missing checksums file when optional but installs nothing", async () => {
    const code = await install.run(
      { KIT_INSTALL_OPTIONAL: "1" },
      harness(goodArchive(), new Error("offline")),
    );
    expect(code).toBe(0);
    expect(binDirEntries()).toEqual([]);
  });
});
