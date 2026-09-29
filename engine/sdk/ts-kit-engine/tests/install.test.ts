import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { createRequire } from "module";
import { EventEmitter } from "events";
import { Readable } from "stream";
import { createHash } from "crypto";
import { deflateRawSync, gzipSync } from "zlib";
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

function paxRecord(key: string, value: string): Buffer {
  const body = ` ${key}=${value}\n`;
  let len = body.length;
  while (`${len}${body}`.length !== len) len = `${len}${body}`.length;
  return Buffer.from(`${len}${body}`);
}

type ZipEntry = { name: string; data?: Buffer; mode?: number; deflate?: boolean };

function zip(entries: ZipEntry[]): Buffer {
  const locals: Buffer[] = [];
  const centrals: Buffer[] = [];
  let offset = 0;
  for (const e of entries) {
    const raw = e.data ?? Buffer.alloc(0);
    const body = e.deflate ? deflateRawSync(raw) : raw;
    const name = Buffer.from(e.name);
    const method = e.deflate ? 8 : 0;
    const local = Buffer.alloc(30);
    local.writeUInt32LE(0x04034b50, 0);
    local.writeUInt16LE(20, 4);
    local.writeUInt16LE(method, 8);
    local.writeUInt32LE(body.length, 18);
    local.writeUInt32LE(raw.length, 22);
    local.writeUInt16LE(name.length, 26);
    const central = Buffer.alloc(46);
    central.writeUInt32LE(0x02014b50, 0);
    central.writeUInt16LE((3 << 8) | 20, 4); // made by unix
    central.writeUInt16LE(20, 6);
    central.writeUInt16LE(method, 10);
    central.writeUInt32LE(body.length, 20);
    central.writeUInt32LE(raw.length, 24);
    central.writeUInt16LE(name.length, 28);
    central.writeUInt32LE(((e.mode ?? 0o100755) << 16) >>> 0, 38);
    central.writeUInt32LE(offset, 42);
    locals.push(local, name, body);
    centrals.push(central, name);
    offset += local.length + name.length + body.length;
  }
  const cd = Buffer.concat(centrals);
  const eocd = Buffer.alloc(22);
  eocd.writeUInt32LE(0x06054b50, 0);
  eocd.writeUInt16LE(entries.length, 8);
  eocd.writeUInt16LE(entries.length, 10);
  eocd.writeUInt32LE(cd.length, 12);
  eocd.writeUInt32LE(offset, 16);
  return Buffer.concat([...locals, cd, eocd]);
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

// --- release location --------------------------------------------------------

// kit releases are tagged `kit/v<version>` on hop-top/poly-kit, and that
// release carries the archives + checksums.txt (.goreleaser.yaml).
const RELEASE = "https://github.com/hop-top/poly-kit/releases/download/kit/v0.5.0-alpha.16";
const REPO_ROOT = join(__dirname, "..", "..", "..", "..");
const PKG = JSON.parse(fs.readFileSync(join(__dirname, "..", "package.json"), "utf8"));

describe("release location", () => {
  it("downloads the archive from the kit component release", async () => {
    const archive = goodArchive();
    const h = harness(archive, `${sha256(archive)}  ${ARCHIVE}\n`);
    const urls: string[] = [];
    const download = h.deps.downloadFile;
    h.deps.downloadFile = async (url: string, dest: string) => {
      urls.push(url);
      await download(url, dest);
    };
    await install.install({ ...h, version: "v0.5.0-alpha.16" });
    expect(urls).toEqual([`${RELEASE}/${ARCHIVE}`]);
  });

  it("reads checksums.txt from the same release", async () => {
    const get = fakeGet(200, "");
    await install.fetchChecksums("0.5.0-alpha.16");
    expect(get.mock.calls[0][0]).toBe(`${RELEASE}/checksums.txt`);
  });

  it("pins the kit release the manifest carries, bumped by the release PR", () => {
    const manifest = JSON.parse(
      fs.readFileSync(join(REPO_ROOT, ".github", ".release-please-manifest.json"), "utf8"),
    );
    expect(PKG.kit.version).toBe(manifest["."]);

    const config = JSON.parse(
      fs.readFileSync(join(REPO_ROOT, ".github", "release-please-config.json"), "utf8"),
    );
    expect(config.packages["."]["extra-files"]).toContainEqual({
      type: "json",
      path: "engine/sdk/ts-kit-engine/package.json",
      jsonpath: "$.kit.version",
    });
  });
});

describe("compatible", () => {
  it.each(["kit v1.2.9\n", "kit v1.2.0-alpha.3", "kit version 1.2.9", "v1.2.9", "1.2.9"])(
    "reads the version off `kit --version` output %j",
    (out) => {
      expect(install.compatible(out, "1.2.3")).toBe(true);
    },
  );

  it.each(["kit v1.3.0", "kit v2.2.3", "", "\n"])("rejects %j", (out) => {
    expect(install.compatible(out, "1.2.3")).toBe(false);
  });
});

// --- extraction --------------------------------------------------------------

describe("extractBinary", () => {
  let box: string;
  let dest: string;

  beforeEach(() => {
    box = join(tmp, "box");
    fs.mkdirSync(join(box, "stage"), { recursive: true });
    dest = join(box, "stage", "kit");
  });

  function extract(archive: Buffer, name = "a.tar.gz", binName = "kit", out = dest) {
    const p = join(box, name);
    fs.writeFileSync(p, archive);
    install.extractBinary(p, binName, out);
    return out;
  }

  const stageEntries = () => fs.readdirSync(join(box, "stage"));

  it("extracts a nested binary and nothing else", () => {
    extract(
      tarGz([
        { name: "README.md", data: Buffer.from("readme") },
        { name: "kit_1.2.3_linux_amd64/", type: "5" },
        { name: "kit_1.2.3_linux_amd64/kit", data: BINARY },
      ]),
    );
    expect(fs.readFileSync(dest)).toEqual(BINARY);
    expect(stageEntries()).toEqual(["kit"]);
  });

  it("ignores traversal and absolute members", () => {
    extract(
      tarGz([
        { name: "../escape", data: Buffer.from("pwn") },
        { name: "../../escape2", data: Buffer.from("pwn") },
        { name: join(box, "abs-escape"), data: Buffer.from("pwn") },
        { name: "kit", data: BINARY },
      ]),
    );
    expect(fs.existsSync(join(box, "escape"))).toBe(false);
    expect(fs.existsSync(join(tmp, "escape2"))).toBe(false);
    expect(fs.existsSync(join(box, "abs-escape"))).toBe(false);
    expect(stageEntries()).toEqual(["kit"]);
    expect(fs.readFileSync(dest)).toEqual(BINARY);
  });

  it("cannot write through a symlinked directory", () => {
    const outside = join(tmp, "outside");
    fs.mkdirSync(outside);
    extract(
      tarGz([
        { name: "d", type: "2", linkname: outside },
        { name: "d/pwn", data: Buffer.from("pwn") },
        { name: "kit", data: BINARY },
      ]),
    );
    expect(fs.existsSync(join(outside, "pwn"))).toBe(false);
    expect(fs.readFileSync(dest)).toEqual(BINARY);
  });

  it.each([
    ["symlink", "2"],
    ["hardlink", "1"],
  ])("rejects a %s posing as the binary", (_kind, type) => {
    const target = join(tmp, "target");
    fs.writeFileSync(target, "not kit");
    expect(() => extract(tarGz([{ name: "kit", type, linkname: target }]))).toThrow(/not found/);
    expect(stageEntries()).toEqual([]);
  });

  it("honours pax and GNU long names", () => {
    const long = `${"d".repeat(120)}/kit`;
    extract(
      tarGz([
        { name: "PaxHeader", type: "x", data: paxRecord("path", long) },
        { name: "truncated-name", data: BINARY },
      ]),
    );
    expect(fs.readFileSync(dest)).toEqual(BINARY);

    fs.rmSync(dest);
    extract(
      tarGz([
        { name: "././@LongLink", type: "L", data: Buffer.from(`${long}\0`) },
        { name: "truncated-name", data: BINARY },
      ]),
    );
    expect(fs.readFileSync(dest)).toEqual(BINARY);
  });

  it("rejects a truncated archive", () => {
    const full = tarGz([{ name: "kit", data: Buffer.alloc(4096, 1) }]);
    const { gunzipSync } = nodeRequire("zlib");
    const cut = gzipSync(gunzipSync(full).subarray(0, 1024));
    expect(() => extract(cut)).toThrow();
    expect(stageEntries()).toEqual([]);
  });

  it("does not pass archive paths through a shell", () => {
    // Each probe would create $KIT_PWN<n> if the path reached a shell.
    const weird = join(box, '$(touch "$KIT_PWN1")`touch "$KIT_PWN2"`";touch "$KIT_PWN3";"');
    fs.mkdirSync(weird);
    const probes = [1, 2, 3].map((n) => join(box, `PWNED${n}`));
    probes.forEach((probe, i) => vi.stubEnv(`KIT_PWN${i + 1}`, probe));
    const out = join(weird, "kit");
    const p = join(weird, "a.tar.gz");
    fs.writeFileSync(p, tarGz([{ name: "kit", data: BINARY }]));
    try {
      install.extractBinary(p, "kit", out);
    } finally {
      vi.unstubAllEnvs();
    }
    expect(fs.readFileSync(out)).toEqual(BINARY);
    for (const probe of probes) expect(fs.existsSync(probe)).toBe(false);
  });

  it("extracts stored and deflated zip members", () => {
    extract(zip([{ name: "kit_1.2.3_windows_amd64/kit.exe", data: BINARY }]), "a.zip", "kit.exe", join(box, "stage", "kit.exe"));
    expect(fs.readFileSync(join(box, "stage", "kit.exe"))).toEqual(BINARY);
    fs.rmSync(join(box, "stage", "kit.exe"));
    extract(zip([{ name: "kit.exe", data: BINARY, deflate: true }]), "a.zip", "kit.exe", join(box, "stage", "kit.exe"));
    expect(fs.readFileSync(join(box, "stage", "kit.exe"))).toEqual(BINARY);
  });

  it("ignores zip traversal members", () => {
    const out = join(box, "stage", "kit.exe");
    extract(
      zip([
        { name: "../escape", data: Buffer.from("pwn") },
        { name: "sub/../../escape2", data: Buffer.from("pwn") },
        { name: "kit.exe", data: BINARY },
      ]),
      "a.zip",
      "kit.exe",
      out,
    );
    expect(fs.existsSync(join(box, "escape"))).toBe(false);
    expect(fs.existsSync(join(box, "escape2"))).toBe(false);
    expect(stageEntries()).toEqual(["kit.exe"]);
  });

  it("rejects a zip symlink posing as the binary", () => {
    const out = join(box, "stage", "kit.exe");
    expect(() =>
      extract(zip([{ name: "kit.exe", data: Buffer.from("/etc/passwd"), mode: 0o120777 }]), "a.zip", "kit.exe", out),
    ).toThrow(/not found/);
    expect(stageEntries()).toEqual([]);
  });

  it("install rejects a symlinked binary end to end", async () => {
    const archive = tarGz([{ name: "kit", type: "2", linkname: "/bin/sh" }]);
    await expect(install.install(harness(archive, `${sha256(archive)}  ${ARCHIVE}\n`))).rejects.toThrow(/not found/);
    expect(binDirEntries()).toEqual([]);
  });
});
