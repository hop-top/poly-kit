/**
 * screenshot.mjs — standalone Playwright screenshot script for spaced web demo.
 *
 * Starts a local static file server, launches Chromium, captures two screenshots:
 *   1. web-terminal.png  — initial hero + terminal (no commands run)
 *   2. web-commands.png  — after running a few commands
 *
 * Saves to examples/spaced/media/ (../../media/ relative to web/).
 * Run from repo root: node examples/spaced/web/scripts/screenshot.mjs
 */

import { chromium } from 'playwright-core';
import http from 'node:http';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const WEB_DIR   = path.resolve(__dirname, '..');
const MEDIA_DIR = path.resolve(__dirname, '../../media');
const PORT      = 3737;

// Ensure media dir exists.
fs.mkdirSync(MEDIA_DIR, { recursive: true });

// ---------------------------------------------------------------------------
// Static file server
// ---------------------------------------------------------------------------

const MIME = {
  '.html': 'text/html',
  '.js':   'application/javascript',
  '.css':  'text/css',
  '.png':  'image/png',
  '.svg':  'image/svg+xml',
  '.ico':  'image/x-icon',
  '.map':  'application/json',
  '.json': 'application/json',
};

function startServer() {
  return new Promise((resolve, reject) => {
    const server = http.createServer((req, res) => {
      const requestUrl = req.url ?? '/';
      let urlPath = requestUrl.split('?')[0];
      if (urlPath === '/') urlPath = '/index.html';

      // Prevent path traversal: normalize and reject anything escaping WEB_DIR.
      const filePath = path.normalize(path.join(WEB_DIR, urlPath));
      if (!filePath.startsWith(WEB_DIR + path.sep) && filePath !== WEB_DIR) {
        res.writeHead(403);
        res.end('Forbidden');
        return;
      }
      const ext  = path.extname(filePath);
      const mime = MIME[ext] ?? 'application/octet-stream';

      fs.readFile(filePath, (err, data) => {
        if (err) {
          res.writeHead(404);
          res.end('Not found');
          return;
        }
        res.writeHead(200, { 'Content-Type': mime });
        res.end(data);
      });
    });

    server.listen(PORT, '127.0.0.1', () => {
      console.log(`Static server listening on http://127.0.0.1:${PORT}`);
      resolve(server);
    });
    server.on('error', reject);
  });
}

// ---------------------------------------------------------------------------
// Screenshot helpers
// ---------------------------------------------------------------------------

async function waitReady(page) {
  await page.waitForSelector('#input', { state: 'visible', timeout: 15_000 });
  await page.waitForFunction(
    () => !document.querySelector('#input')?.disabled,
    { timeout: 15_000 },
  );
}

async function runCmd(page, cmd) {
  const input = page.locator('#input');
  await input.fill(cmd);

  // Submit via Enter key.
  await input.press('Enter');

  // Wait until input is re-enabled (command finished).
  await page.waitForFunction(
    () => !document.querySelector('#input')?.disabled,
    { timeout: 15_000 },
  );
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

const server = await startServer();

const browser = await chromium.launch({ headless: true });
const ctx     = await browser.newContext({
  viewport: { width: 1280, height: 800 },
  deviceScaleFactor: 2,
});
const page = await ctx.newPage();

try {
  // ── Screenshot 1: initial hero + terminal ─────────────────────────────────
  await page.goto(`http://127.0.0.1:${PORT}/?skip-demo`, { waitUntil: 'networkidle' });
  await waitReady(page);
  await page.waitForTimeout(500);

  await page.screenshot({
    path:     path.join(MEDIA_DIR, 'web-terminal.png'),
    fullPage: true,
  });
  console.log('Saved web-terminal.png');

  // ── Screenshot 2: after running a few commands ────────────────────────────
  await runCmd(page, 'mission list');
  await runCmd(page, 'daemon list');
  await runCmd(page, 'elon status');
  await page.waitForTimeout(300);

  await page.screenshot({
    path:     path.join(MEDIA_DIR, 'web-commands.png'),
    fullPage: true,
  });
  console.log('Saved web-commands.png');

} finally {
  await browser.close();
  await new Promise(resolve => server.close(resolve));
  console.log('Done.');
}
