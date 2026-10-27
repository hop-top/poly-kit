/**
 * Vitest setup: points XDG_CONFIG_HOME at an empty temp dir per test
 * file, so a developer's own config (llm.yaml included) never reaches
 * a test. A test that needs a config writes it there or sets its own.
 *
 * @internal
 */

import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterAll } from "vitest";

const dir = mkdtempSync(join(tmpdir(), "kit-ts-xdg-"));
process.env["XDG_CONFIG_HOME"] = dir;
afterAll(() => rmSync(dir, { recursive: true, force: true }));
