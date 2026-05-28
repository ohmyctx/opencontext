#!/usr/bin/env node

"use strict";

const { execFileSync, execSync } = require("child_process");
const fs = require("fs");
const http = require("http");
const https = require("https");
const path = require("path");

const PACKAGE = require("./package.json");
const VERSION = `v${PACKAGE.version}`;
const NAME = "oc";
const GITHUB_REPO = "ohmyctx/opencontext";

const PLATFORM_MAP = {
  darwin: "darwin",
  linux: "linux",
  win32: "windows",
};

const ARCH_MAP = {
  x64: "amd64",
  arm64: "arm64",
};

function platformInfo() {
  const platform = PLATFORM_MAP[process.platform];
  const arch = ARCH_MAP[process.arch];
  if (!platform || !arch) {
    throw new Error(`Unsupported platform: ${process.platform}/${process.arch}`);
  }
  const ext = platform === "windows" ? ".zip" : ".tar.gz";
  return {
    platform,
    arch,
    ext,
    filename: `${NAME}-${VERSION}-${platform}-${arch}${ext}`,
  };
}

function fetch(url, redirects = 5) {
  return new Promise((resolve, reject) => {
    if (redirects <= 0) return reject(new Error("too many redirects"));
    const mod = url.startsWith("https") ? https : http;
    mod.get(url, { headers: { "User-Agent": "opencontext-npm" } }, (res) => {
      if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
        return resolve(fetch(res.headers.location, redirects - 1));
      }
      if (res.statusCode !== 200) {
        res.resume();
        return reject(new Error(`HTTP ${res.statusCode} for ${url}`));
      }
      const chunks = [];
      res.on("data", (chunk) => chunks.push(chunk));
      res.on("end", () => resolve(Buffer.concat(chunks)));
      res.on("error", reject);
    }).on("error", reject);
  });
}

function installedVersion(binaryPath) {
  try {
    return execFileSync(binaryPath, ["--version"], { encoding: "utf8", timeout: 5000 });
  } catch {
    return "";
  }
}

async function main() {
  const info = platformInfo();
  console.log(`[opencontext] Platform: ${info.platform}/${info.arch}`);

  const binDir = path.join(__dirname, "bin");
  fs.mkdirSync(binDir, { recursive: true });

  const binaryName = info.platform === "windows" ? `${NAME}.exe` : NAME;
  const binaryPath = path.join(binDir, binaryName);
  const current = installedVersion(binaryPath);
  if (current.includes(PACKAGE.version)) {
    console.log(`[opencontext] Binary ${VERSION} already installed.`);
    return;
  }

  const url = `https://github.com/${GITHUB_REPO}/releases/download/${VERSION}/${info.filename}`;
  console.log(`[opencontext] Downloading ${url}`);
  const data = await fetch(url);
  const archive = path.join(binDir, `_tmp${info.ext}`);
  fs.writeFileSync(archive, data);

  try {
    if (info.ext === ".zip") {
      try {
        execSync(`unzip -o "${archive}" -d "${binDir}"`, { stdio: "pipe" });
      } catch {
        execSync(`powershell -Command "Expand-Archive -Force '${archive}' '${binDir}'"`, { stdio: "pipe" });
      }
    } else {
      execSync(`tar xzf "${archive}" -C "${binDir}"`, { stdio: "pipe" });
    }
  } finally {
    try {
      fs.unlinkSync(archive);
    } catch {}
  }

  if (info.platform !== "windows") {
    fs.chmodSync(binaryPath, 0o755);
  }
  if (info.platform === "darwin") {
    try {
      execSync(`xattr -d com.apple.quarantine "${binaryPath}"`, { stdio: "pipe" });
    } catch {}
  }

  console.log(`[opencontext] Installed to ${binaryPath}`);
}

main().catch((err) => {
  console.error(`[opencontext] Installation failed: ${err.message}`);
  console.error(`Download manually from https://github.com/${GITHUB_REPO}/releases/tag/${VERSION}`);
  process.exit(1);
});
