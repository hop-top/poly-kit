#!/usr/bin/env node
"use strict";

const { execFileSync } = require("child_process");
const fs = require("fs");
const { join, posix } = require("path");
const https = require("https");
const crypto = require("crypto");
const zlib = require("zlib");

// kit releases are tagged `kit/v<version>` on the monorepo; that release
// carries the `kit_<os>_<arch>` archives and checksums.txt.
const REPO = "hop-top/poly-kit";
const BIN_DIR = join(__dirname, "..", "bin");
const PKG = require("../package.json");
const VERSION = PKG.kit && PKG.kit.version || PKG.version;
const CHECKSUMS_MAX_BYTES = 1 << 20;

function which(name) {
  try {
    const cmd = process.platform === "win32" ? "where" : "which";
    return execFileSync(cmd, [name], { stdio: "pipe" }).toString().trim();
  } catch {
    return null;
  }
}

function kitVersion(binPath) {
  try {
    return execFileSync(binPath, ["--version"], { stdio: "pipe" })
      .toString().trim();
  } catch {
    return null;
  }
}

// Accept the same major.minor. `kit --version` prints `kit v<version>`;
// the version is its last word.
function compatible(found, wanted) {
  const words = found.trim().split(/\s+/);
  const f = words[words.length - 1].replace(/^v/, "").split(".");
  const w = wanted.replace(/^v/, "").split(".");
  return f.length > 1 && f[0] === w[0] && f[1] === w[1];
}

function releaseUrl(version, filename) {
  return `https://github.com/${REPO}/releases/download/kit/v${version}/${filename}`;
}

function platformKey() {
  const os = { darwin: "darwin", linux: "linux", win32: "windows" }[process.platform];
  const arch = { x64: "amd64", arm64: "arm64" }[process.arch];
  if (!os || !arch) throw new Error(`Unsupported platform: ${process.platform}/${process.arch}`);
  return `${os}_${arch}`;
}

function get(url) {
  return new Promise((resolve, reject) => {
    const follow = (u) => {
      https.get(u, (res) => {
        if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
          res.resume();
          follow(res.headers.location);
          return;
        }
        if (res.statusCode !== 200) {
          res.resume();
          reject(new Error(`HTTP ${res.statusCode} for ${u}`));
          return;
        }
        resolve(res);
      }).on("error", reject);
    };
    follow(url);
  });
}

function downloadFile(url, dest) {
  return get(url).then((res) => new Promise((resolve, reject) => {
    const file = fs.createWriteStream(dest);
    res.on("error", reject);
    file.on("error", reject);
    file.on("finish", () => file.close(resolve));
    res.pipe(file);
  }));
}

// Download the release checksums file. Any failure rejects: the caller must
// never install a binary it could not verify.
async function fetchChecksums(version) {
  const url = releaseUrl(version, "checksums.txt");
  try {
    const res = await get(url);
    const chunks = [];
    let total = 0;
    for await (const chunk of res) {
      total += chunk.length;
      if (total > CHECKSUMS_MAX_BYTES) throw new Error("checksums file too large");
      chunks.push(chunk);
    }
    return Buffer.concat(chunks).toString("utf8");
  } catch (err) {
    throw new Error(
      `failed to fetch checksums from ${url}: ${err.message}; ` +
      "refusing to install an unverified kit binary",
    );
  }
}

// Parse "<hash>  <file>" (GNU coreutils) or "<hash> <file>"; mirrors the Go
// upgrade package so every SDK reads checksums.txt the same way.
function parseChecksumLine(line) {
  line = line.trim();
  if (!line || line.startsWith("#")) return null;
  const idx = line.indexOf("  ");
  const parts = idx > 0 ? [line.slice(0, idx), line.slice(idx + 2)] : line.split(/\s+/);
  return parts.length === 2 ? parts : null;
}

function findChecksum(text, filename) {
  for (const line of text.split("\n")) {
    const parsed = parseChecksumLine(line);
    if (parsed && posix.basename(parsed[1]) === filename) return parsed[0];
  }
  throw new Error(
    `no checksum found for ${JSON.stringify(filename)} in release checksums; ` +
    "refusing to install an unverified kit binary",
  );
}

function sha256File(path) {
  return crypto.createHash("sha256").update(fs.readFileSync(path)).digest("hex");
}

function verifyArchive(archivePath, archiveName, checksumsText) {
  const expected = findChecksum(checksumsText, archiveName).toLowerCase();
  const actual = sha256File(archivePath);
  if (actual !== expected) {
    throw new Error(`Checksum mismatch for ${archiveName}: expected ${expected}, got ${actual}`);
  }
}

// --- archive reading ---------------------------------------------------------
//
// Only the kit binary is read out of the release archive, in-process: the
// first regular-file member whose basename matches is written to a path
// chosen here (mirrors go/core/upgrade extractTarGz). Member names never
// become filesystem paths, link members are skipped and no shell or external
// tar/unzip is involved.

function memberBasename(name) {
  return name.replace(/\\/g, "/").replace(/\/+$/, "").split("/").pop();
}

function cstring(buf, start, len) {
  const field = buf.subarray(start, start + len);
  const nul = field.indexOf(0);
  return field.subarray(0, nul === -1 ? field.length : nul).toString("utf8");
}

function tarSize(header) {
  if (header[124] & 0x80) throw new Error("tar: base-256 sizes are not supported");
  const size = parseInt(cstring(header, 124, 12).trim() || "0", 8);
  if (!Number.isSafeInteger(size) || size < 0) throw new Error("tar: invalid member size");
  return size;
}

function paxPath(body) {
  let path = null;
  let off = 0;
  while (off < body.length) {
    const space = body.indexOf(0x20, off);
    if (space === -1) break;
    const len = parseInt(body.subarray(off, space).toString("ascii"), 10);
    if (!(len > 0) || off + len > body.length) throw new Error("tar: malformed pax header");
    const record = body.subarray(space + 1, off + len - 1).toString("utf8");
    const eq = record.indexOf("=");
    if (eq !== -1 && record.slice(0, eq) === "path") path = record.slice(eq + 1);
    off += len;
  }
  return path;
}

// Regular-file typeflags: '0', legacy NUL, and '7' (contiguous file).
const TAR_REGULAR = new Set(["0", "\0", "7"]);

function readTarGzMember(archive, binName) {
  const data = zlib.gunzipSync(archive);
  const want = binName.toLowerCase();
  let longName = null;
  let pax = null;
  for (let off = 0; off + 512 <= data.length;) {
    const header = data.subarray(off, off + 512);
    if (header.every((b) => b === 0)) break;
    const size = tarSize(header);
    const body = off + 512;
    if (body + size > data.length) throw new Error("tar: truncated archive");
    const content = data.subarray(body, body + size);
    off = body + Math.ceil(size / 512) * 512;

    const type = String.fromCharCode(header[156]);
    if (type === "L") { longName = cstring(content, 0, content.length); continue; }
    if (type === "x") { pax = paxPath(content); continue; }
    if (type === "g") continue;

    let name = cstring(header, 0, 100);
    if (cstring(header, 257, 6).startsWith("ustar")) {
      const prefix = cstring(header, 345, 155);
      if (prefix) name = `${prefix}/${name}`;
    }
    name = pax || longName || name;
    pax = null;
    longName = null;

    if (TAR_REGULAR.has(type) && memberBasename(name).toLowerCase() === want) {
      return Buffer.from(content);
    }
  }
  return null;
}

const S_IFMT = 0o170000;
const S_IFREG = 0o100000;

function readZipMember(archive, binName) {
  const want = binName.toLowerCase();
  const floor = Math.max(0, archive.length - 22 - 0xffff);
  let eocd = -1;
  for (let i = archive.length - 22; i >= floor; i--) {
    if (archive.readUInt32LE(i) === 0x06054b50) { eocd = i; break; }
  }
  if (eocd === -1) throw new Error("zip: end of central directory not found");
  const count = archive.readUInt16LE(eocd + 10);
  let p = archive.readUInt32LE(eocd + 16);
  if (p === 0xffffffff || count === 0xffff) throw new Error("zip: zip64 archives are not supported");

  for (let i = 0; i < count; i++) {
    if (p + 46 > archive.length || archive.readUInt32LE(p) !== 0x02014b50) {
      throw new Error("zip: corrupt central directory");
    }
    const flags = archive.readUInt16LE(p + 8);
    const method = archive.readUInt16LE(p + 10);
    const compSize = archive.readUInt32LE(p + 20);
    const size = archive.readUInt32LE(p + 24);
    const nameLen = archive.readUInt16LE(p + 28);
    const extraLen = archive.readUInt16LE(p + 30);
    const commentLen = archive.readUInt16LE(p + 32);
    const fileType = (archive.readUInt32LE(p + 38) >>> 16) & S_IFMT;
    const localOff = archive.readUInt32LE(p + 42);
    const name = archive.toString("utf8", p + 46, p + 46 + nameLen);
    p += 46 + nameLen + extraLen + commentLen;

    // No unix file-type bits (e.g. Windows-built archives) means a plain
    // file; anything typed must be a regular file, not a link.
    const regular = !name.endsWith("/") && (fileType === 0 || fileType === S_IFREG);
    if (!regular || memberBasename(name).toLowerCase() !== want) continue;
    if (flags & 0x1) throw new Error("zip: encrypted members are not supported");

    if (localOff + 30 > archive.length || archive.readUInt32LE(localOff) !== 0x04034b50) {
      throw new Error("zip: corrupt local header");
    }
    const start = localOff + 30 + archive.readUInt16LE(localOff + 26) + archive.readUInt16LE(localOff + 28);
    if (start + compSize > archive.length) throw new Error("zip: truncated archive");
    const raw = archive.subarray(start, start + compSize);
    let out;
    if (method === 0) out = Buffer.from(raw);
    else if (method === 8) out = zlib.inflateRawSync(raw);
    else throw new Error(`zip: unsupported compression method ${method}`);
    if (out.length !== size) throw new Error("zip: member size mismatch");
    return out;
  }
  return null;
}

function extractBinary(archivePath, binName, destPath) {
  const archive = fs.readFileSync(archivePath);
  const data = archivePath.endsWith(".zip")
    ? readZipMember(archive, binName)
    : readTarGzMember(archive, binName);
  if (!data) throw new Error(`binary ${binName} not found in archive`);
  fs.writeFileSync(destPath, data, { flag: "wx", mode: 0o755 });
}

const defaultDeps = { which, kitVersion, downloadFile, fetchChecksums };

// Download, verify and install the kit binary into binDir. The archive is
// staged in a private temp dir inside binDir; only a verified binary is ever
// moved to its final path, and the staging dir is always removed.
async function install(opts = {}) {
  const deps = { ...defaultDeps, ...opts.deps };
  const binDir = opts.binDir || BIN_DIR;
  const version = opts.version || VERSION;
  const platform = opts.platform || process.platform;
  const key = opts.platformKey || platformKey();
  const log = opts.log || ((msg) => process.stdout.write(msg));
  const binName = platform === "win32" ? "kit.exe" : "kit";

  // Check PATH first
  const systemBin = deps.which("kit");
  if (systemBin) {
    const ver = deps.kitVersion(systemBin);
    if (ver && compatible(ver, version)) {
      log(`kit-engine: found compatible kit ${ver} at ${systemBin}\n`);
      return systemBin;
    }
  }

  const binPath = join(binDir, binName);
  if (fs.existsSync(binPath)) {
    const ver = deps.kitVersion(binPath);
    if (ver && compatible(ver, version)) {
      log(`kit-engine: binary already present (${ver})\n`);
      return binPath;
    }
  }

  const ext = platform === "win32" ? "zip" : "tar.gz";
  const archiveName = `kit_${key}.${ext}`;
  const ver = version.replace(/^v/, "");
  const url = releaseUrl(ver, archiveName);

  log(`kit-engine: downloading kit v${ver} for ${key}...\n`);
  fs.mkdirSync(binDir, { recursive: true });
  const staging = fs.mkdtempSync(join(binDir, ".kit-install-"));
  try {
    const archivePath = join(staging, archiveName);
    await deps.downloadFile(url, archivePath);

    verifyArchive(archivePath, archiveName, await deps.fetchChecksums(ver));
    log("kit-engine: checksum verified\n");

    const staged = join(staging, binName);
    extractBinary(archivePath, binName, staged);
    if (platform !== "win32") fs.chmodSync(staged, 0o755);
    fs.renameSync(staged, binPath);
  } finally {
    fs.rmSync(staging, { recursive: true, force: true });
  }

  log("kit-engine: download complete\n");
  return binPath;
}

// KIT_INSTALL_OPTIONAL=1 tolerates a failed install (nothing is installed);
// it never bypasses verification.
async function run(env = process.env, opts = {}) {
  try {
    await install(opts);
    return 0;
  } catch (err) {
    if (env.KIT_INSTALL_OPTIONAL === "1") {
      console.warn(`kit-engine postinstall (skipped): ${err.message}`);
      return 0;
    }
    console.error(`kit-engine postinstall: ${err.message}`);
    return 1;
  }
}

module.exports = {
  compatible,
  extractBinary,
  fetchChecksums,
  findChecksum,
  install,
  parseChecksumLine,
  releaseUrl,
  run,
  verifyArchive,
};

if (require.main === module) {
  run().then((code) => {
    process.exitCode = code;
  });
}
