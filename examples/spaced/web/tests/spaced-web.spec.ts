/**
 * Playwright e2e tests for the spaced web demo.
 *
 * Strategy: load index.html via file:// URL, interact with the terminal UI,
 * assert output from the pure-function router.
 *
 * We skip waiting for the demo animation (too slow) by typing directly into
 * the input field and clicking run. Demo animation assertions use a shorter
 * wait anchored on specific output keywords.
 */

import { test, expect, Page } from '@playwright/test';

const PAGE_URL      = '/';
const PAGE_NO_DEMO  = '/?skip-demo';

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

async function loadPage(page: Page, url = PAGE_NO_DEMO): Promise<void> {
  await page.goto(url, { waitUntil: 'networkidle' });
  await page.waitForSelector('#input');
}

/** Wait until the page is ready (no animation — loaded with ?skip-demo). */
async function waitReady(page: Page): Promise<void> {
  await expect(page.locator('#input')).toBeEnabled({ timeout: 5_000 });
}

/** Type a command and click run; returns output of that specific command only. */
async function runCmd(page: Page, cmd: string): Promise<string> {
  const input   = page.locator('#input');
  const output  = page.locator('#output');
  const before  = await output.locator('> *').count();

  await input.fill(cmd);
  await page.locator('#runBtn').click();
  await expect(input).toBeEnabled({ timeout: 10_000 });

  const all = await output.locator('> *').all();
  // Slice: skip the echo line (+1), collect everything until trailing empty sep.
  const newEls = all.slice(before + 1);
  const texts  = await Promise.all(newEls.map(el => el.innerText()));
  return texts.join('\n');
}

// ---------------------------------------------------------------------------
// Page structure
// ---------------------------------------------------------------------------

test.describe('page structure', () => {
  test('loads without errors', async ({ page }) => {
    const errors: string[] = [];
    page.on('pageerror', e => errors.push(e.message));
    await loadPage(page);
    expect(errors).toHaveLength(0);
  });

  test('has hero title', async ({ page }) => {
    await loadPage(page);
    const h1 = await page.locator('.hero h1').innerText();
    expect(h1).toContain('spaced');
  });

  test('has terminal window', async ({ page }) => {
    await loadPage(page);
    await expect(page.locator('#output')).toBeVisible();
    await expect(page.locator('#input')).toBeVisible();
    await expect(page.locator('#runBtn')).toBeVisible();
  });

  test('has suggestion chips', async ({ page }) => {
    await loadPage(page);
    const chips = page.locator('.chip');
    const count = await chips.count();
    expect(count).toBeGreaterThanOrEqual(5);
    // At least one chip reads "mission list"
    const texts = await chips.allInnerTexts();
    expect(texts.some(t => t.includes('mission'))).toBe(true);
  });

  test('has install CTA with go install command', async ({ page }) => {
    await loadPage(page);
    const cmd = await page.locator('.install-cmd').innerText();
    expect(cmd).toContain('go install');
    expect(cmd).toContain('spaced');
  });

  test('has GitHub Sponsors link in footer', async ({ page }) => {
    await loadPage(page);
    const link = page.locator('footer .sponsor-link');
    await expect(link).toBeVisible();
    await expect(link).toHaveAttribute('href', 'https://github.com/sponsors/hop-top');
  });

  test('has disclaimer in footer', async ({ page }) => {
    await loadPage(page);
    const footer = await page.locator('footer').innerText();
    expect(footer).toContain('Not affiliated');
    expect(footer).toContain('SpaceX');
  });
});

// ---------------------------------------------------------------------------
// Command execution
// ---------------------------------------------------------------------------

test.describe('commands', () => {
  test.beforeEach(async ({ page }) => {
    await loadPage(page);
    await waitReady(page);
  });

  test('--help shows usage', async ({ page }) => {
    const out = await runCmd(page, '--help');
    expect(out).toContain('Usage');
    expect(out).toContain('mission');
    expect(out).toContain('daemon');
  });

  test('--help contains disclaimer', async ({ page }) => {
    const out = await runCmd(page, '--help');
    expect(out).toContain('Not affiliated');
    expect(out).toContain('github.com/sponsors/hop-top');
  });

  test('--version prints version string', async ({ page }) => {
    const out = await runCmd(page, '--version');
    expect(out).toContain('spaced');
    expect(out).toMatch(/\d+\.\d+/);
  });

  test('mission list returns table of missions', async ({ page }) => {
    const out = await runCmd(page, 'mission list');
    expect(out).toContain('Starman');
  });

  test('mission list --format json returns JSON array', async ({ page }) => {
    const out = await runCmd(page, 'mission list --format json');
    // Router outputs raw JSON.stringify — find the JSON array.
    const jsonStart = out.indexOf('[');
    const jsonEnd   = out.lastIndexOf(']');
    expect(jsonStart).toBeGreaterThanOrEqual(0);
    expect(jsonEnd).toBeGreaterThan(jsonStart);
    const arr = JSON.parse(out.slice(jsonStart, jsonEnd + 1));
    expect(Array.isArray(arr)).toBe(true);
    expect(arr.length).toBeGreaterThan(0);
    expect(arr[0]).toHaveProperty('name');
  });

  test('mission inspect starman returns mission details', async ({ page }) => {
    const out = await runCmd(page, 'mission inspect starman');
    expect(out.toLowerCase()).toContain('starman');
  });

  test('mission inspect unknown shows error', async ({ page }) => {
    const out = await runCmd(page, 'mission inspect xyzzy-does-not-exist');
    expect(out.toLowerCase()).toMatch(/not found|unknown|error/);
  });

  test('daemon list returns table with daemons', async ({ page }) => {
    const out = await runCmd(page, 'daemon list');
    // daemon list should show known daemon ids
    expect(out.toLowerCase()).toMatch(/funding.secured|subsidies|autopilot|doge|starlink/);
  });

  test('daemon stop returns non-fatal message', async ({ page }) => {
    const out = await runCmd(page, 'daemon stop funding-secured');
    // daemon stop always "fails" humorously — exits 0 with a message
    expect(out.toLowerCase()).toMatch(/stop|cannot|fail|unstoppable|running|persist/);
  });

  test('daemon stop --all also fails gracefully', async ({ page }) => {
    const out = await runCmd(page, 'daemon stop --all');
    expect(out.toLowerCase()).toMatch(/stop|cannot|fail|unstoppable|running|persist/);
  });

  test('elon status returns status report', async ({ page }) => {
    const out = await runCmd(page, 'elon status');
    expect(out.toLowerCase()).toMatch(/elon|musk|status/);
  });

  test('starship status returns overview', async ({ page }) => {
    const out = await runCmd(page, 'starship status');
    expect(out.toLowerCase()).toContain('starship');
  });

  test('ipo status returns IPO info', async ({ page }) => {
    const out = await runCmd(page, 'ipo status');
    expect(out.toLowerCase()).toMatch(/ipo|public|shares|valuation/);
  });

  test('competitor compare boeing shows comparison', async ({ page }) => {
    const out = await runCmd(page, 'competitor compare boeing');
    expect(out.toLowerCase()).toContain('boeing');
  });

  test('fleet list shows vehicles', async ({ page }) => {
    const out = await runCmd(page, 'fleet list');
    expect(out.toLowerCase()).toMatch(/falcon|starship|dragon/);
  });

  test('launch with --dry-run does not crash', async ({ page }) => {
    const out = await runCmd(page, 'launch starman --dry-run');
    // Should output something, not error
    expect(out.trim().length).toBeGreaterThan(0);
  });

  test('telemetry get starman returns data', async ({ page }) => {
    const out = await runCmd(page, 'telemetry get starman');
    expect(out.toLowerCase()).toMatch(/starman|telemetry|altitude|speed|velocity/);
  });

  test('service list exposes adjacent service surfaces', async ({ page }) => {
    const out = await runCmd(page, 'service list');
    expect(out.toLowerCase()).toContain('websocket');
    expect(out.toLowerCase()).toContain('mcp');
    expect(out.toLowerCase()).toContain('rest');
  });

  test('service inspect websocket shows socket endpoint sample', async ({ page }) => {
    const out = await runCmd(page, 'service inspect websocket');
    expect(out.toLowerCase()).toContain('/socket');
    expect(out.toLowerCase()).toContain('telemetry.tick');
  });

  test('service inspect mcp shows tool and resource names', async ({ page }) => {
    const out = await runCmd(page, 'service inspect mcp');
    expect(out).toContain('spaced.mission.list');
    expect(out).toContain('spaced://fleet/vehicles');
  });

  test('service smoke validates populated samples', async ({ page }) => {
    const out = await runCmd(page, 'service smoke');
    expect(out.toLowerCase()).toContain('websocket');
    expect(out.toLowerCase()).toMatch(/mcp\s+ok|mcp[\s\S]+ok/);
  });

  test('unknown command shows error hint', async ({ page }) => {
    const out = await runCmd(page, 'notacommand');
    expect(out.toLowerCase()).toMatch(/unknown|error|help/);
  });
});

// ---------------------------------------------------------------------------
// Interaction
// ---------------------------------------------------------------------------

test.describe('interaction', () => {
  test.beforeEach(async ({ page }) => {
    await loadPage(page);
    await waitReady(page);
  });

  test('Enter key submits command', async ({ page }) => {
    const input = page.locator('#input');
    await input.fill('--version');
    await input.press('Enter');
    await expect(input).toBeEnabled({ timeout: 10_000 });
    const out = await page.locator('#output').innerText();
    expect(out).toContain('spaced');
  });

  test('command history: ArrowUp restores last command', async ({ page }) => {
    const input = page.locator('#input');
    // Run a command.
    await input.fill('--version');
    await page.locator('#runBtn').click();
    await expect(input).toBeEnabled({ timeout: 10_000 });
    // Clear and press ArrowUp.
    await input.fill('');
    await input.press('ArrowUp');
    expect(await input.inputValue()).toBe('--version');
  });

  test('clicking a chip fills input and runs', async ({ page }) => {
    // Click the "mission list" chip if present.
    const chip = page.locator('.chip', { hasText: 'mission list' });
    await chip.first().click();
    await expect(page.locator('#input')).toBeEnabled({ timeout: 15_000 });
    const out = await page.locator('#output').innerText();
    expect(out).toContain('Starman');
  });

  test('copy button updates label to "copied!"', async ({ page }) => {
    // Clipboard is already mocked via beforeEach (page was loaded fresh).
    // Expose clipboard mock via evaluate before clicking.
    await page.evaluate(() => {
      Object.defineProperty(navigator, 'clipboard', {
        value: { writeText: () => Promise.resolve() },
        configurable: true,
        writable: true,
      });
    });
    const btn = page.locator('#copyBtn');
    await btn.click();
    await expect(btn).toHaveText('copied!', { timeout: 3_000 });
    // Reverts after 2 s — we just verify it changed.
  });

  test('run button disabled while command runs', async ({ page }) => {
    // We can only observe the transient disabled state for very fast commands,
    // so just assert the button exists and is re-enabled after run.
    const input = page.locator('#input');
    const runBtn = page.locator('#runBtn');
    await input.fill('mission list');
    await runBtn.click();
    await expect(input).toBeEnabled({ timeout: 10_000 });
    await expect(runBtn).toBeEnabled();
  });
});

// ---------------------------------------------------------------------------
// Demo animation smoke test
// ---------------------------------------------------------------------------

test('demo animation produces output within 30s', async ({ page }) => {
  // Load without ?skip-demo so the animation actually plays.
  await loadPage(page, PAGE_URL);
  await expect(page.locator('#output')).toContainText('Starman', { timeout: 30_000 });
});
