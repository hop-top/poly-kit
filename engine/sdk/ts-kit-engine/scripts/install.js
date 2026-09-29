#!/usr/bin/env node
"use strict";

const { execFileSync } = require("child_process");
const fs = require("fs");
const { join, posix } = require("path");
const https = require("https");
const crypto = require("crypto");

const REPO = "hop-top/kit";
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

function compatible(found, wanted) {
  // Accept same major.minor
  const f = found.replace(/^v/, "").split(".");
  const w = wanted.replace(/^v/, "").split(".");
  return f[0] === w[0] && f[1] === w[1];
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
  const url = `https://github.com/${REPO}/releases/download/v${version}/checksums.txt`;
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

function extractBinary(archivePath, binName, destPath) {
  const { execSync } = require("child_process");
  const dir = join(destPath, "..");
  if (archivePath.endsWith(".zip")) {
    execSync(`unzip -o "${archivePath}" -d "${dir}"`, { stdio: "ignore" });
  } else {
    execSync(`tar -xzf "${archivePath}" -C "${dir}"`, { stdio: "ignore" });
  }
  if (!fs.existsSync(destPath)) throw new Error(`binary ${binName} not found in archive`);
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
  const url = `https://github.com/${REPO}/releases/download/v${ver}/${archiveName}`;

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
  run,
  verifyArchive,
};

if (require.main === module) {
  run().then((code) => {
    process.exitCode = code;
  });
}
