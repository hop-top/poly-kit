#!/usr/bin/env node
"use strict";

const { execFileSync } = require("child_process");
const { existsSync, mkdirSync, createWriteStream, unlinkSync } = require("fs");
const { join } = require("path");
const https = require("https");
const crypto = require("crypto");

const REPO = "hop-top/kit";
const BIN_DIR = join(__dirname, "..", "bin");
const BIN_NAME = process.platform === "win32" ? "kit.exe" : "kit";
const PKG = require("../package.json");
const VERSION = PKG.kit && PKG.kit.version || PKG.version;

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
          follow(res.headers.location);
          return;
        }
        if (res.statusCode !== 200) {
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
  return new Promise((resolve, reject) => {
    const follow = (u) => {
      https.get(u, (res) => {
        if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
          follow(res.headers.location);
          return;
        }
        if (res.statusCode !== 200) {
          reject(new Error(`Download failed: ${res.statusCode}`));
          return;
        }
        const file = createWriteStream(dest);
        res.pipe(file);
        file.on("finish", () => file.close(resolve));
      }).on("error", reject);
    };
    follow(url);
  });
}

async function fetchChecksums(version) {
  const url = `https://github.com/${REPO}/releases/download/v${version}/checksums.txt`;
  try {
    const res = await get(url);
    const chunks = [];
    for await (const chunk of res) chunks.push(chunk);
    const text = Buffer.concat(chunks).toString();
    const map = {};
    for (const line of text.split("\n")) {
      const [hash, name] = line.trim().split(/\s+/);
      if (hash && name) map[name] = hash;
    }
    return map;
  } catch {
    return null;
  }
}

function sha256File(path) {
  const { readFileSync } = require("fs");
  const data = readFileSync(path);
  return crypto.createHash("sha256").update(data).digest("hex");
}

async function extractTarGz(archive, destDir) {
  const { execSync } = require("child_process");
  execSync(`tar -xzf "${archive}" -C "${destDir}"`, { stdio: "ignore" });
}

async function extractZip(archive, destDir) {
  const { execSync } = require("child_process");
  execSync(`unzip -o "${archive}" -d "${destDir}"`, { stdio: "ignore" });
}

async function main() {
  // Check PATH first
  const systemBin = which("kit");
  if (systemBin) {
    const ver = kitVersion(systemBin);
    if (ver && compatible(ver, VERSION)) {
      process.stdout.write(`kit-engine: found compatible kit ${ver} at ${systemBin}\n`);
      return;
    }
  }

  const binPath = join(BIN_DIR, BIN_NAME);
  if (existsSync(binPath)) {
    const ver = kitVersion(binPath);
    if (ver && compatible(ver, VERSION)) {
      process.stdout.write(`kit-engine: binary already present (${ver})\n`);
      return;
    }
  }

  const key = platformKey();
  const ext = process.platform === "win32" ? "zip" : "tar.gz";
  const archiveName = `kit_${key}.${ext}`;
  const ver = VERSION.replace(/^v/, "");
  const url = `https://github.com/${REPO}/releases/download/v${ver}/${archiveName}`;

  process.stdout.write(`kit-engine: downloading kit v${ver} for ${key}...\n`);
  mkdirSync(BIN_DIR, { recursive: true });

  const archivePath = join(BIN_DIR, archiveName);
  await downloadFile(url, archivePath);

  // Verify checksum
  const checksums = await fetchChecksums(ver);
  if (checksums) {
    const expected = checksums[archiveName];
    if (expected) {
      const actual = sha256File(archivePath);
      if (actual !== expected) {
        unlinkSync(archivePath);
        throw new Error(`Checksum mismatch: expected ${expected}, got ${actual}`);
      }
      process.stdout.write("kit-engine: checksum verified\n");
    }
  }

  // Extract
  if (ext === "tar.gz") {
    await extractTarGz(archivePath, BIN_DIR);
  } else {
    await extractZip(archivePath, BIN_DIR);
  }
  unlinkSync(archivePath);

  // Ensure executable
  if (process.platform !== "win32") {
    const { chmodSync } = require("fs");
    chmodSync(binPath, 0o755);
  }

  process.stdout.write("kit-engine: download complete\n");
}

main().catch((err) => {
  if (process.env.KIT_INSTALL_OPTIONAL === "1") {
    console.warn(`kit-engine postinstall (skipped): ${err.message}`);
  } else {
    console.error(`kit-engine postinstall: ${err.message}`);
    process.exit(1);
  }
});
