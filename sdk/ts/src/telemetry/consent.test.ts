import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { promises as fs } from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';
import { loadConsent, consentPath, deniedConsent } from './consent';

// Each test gets its own XDG_CONFIG_HOME pointed at a fresh temp dir
// so we never touch the real ~/.config/kit/telemetry.yaml.
let tmpRoot: string;
let savedConfigHome: string | undefined;

beforeEach(async () => {
  savedConfigHome = process.env.XDG_CONFIG_HOME;
  tmpRoot = await fs.mkdtemp(path.join(os.tmpdir(), 'kit-telemetry-consent-'));
  process.env.XDG_CONFIG_HOME = tmpRoot;
});

afterEach(async () => {
  if (savedConfigHome === undefined) {
    delete process.env.XDG_CONFIG_HOME;
  } else {
    process.env.XDG_CONFIG_HOME = savedConfigHome;
  }
  await fs.rm(tmpRoot, { recursive: true, force: true });
});

async function writeConsentFile(contents: string): Promise<void> {
  const p = consentPath();
  await fs.mkdir(path.dirname(p), { recursive: true, mode: 0o700 });
  await fs.writeFile(p, contents, { mode: 0o600 });
}

describe('consentPath', () => {
  it('resolves under XDG_CONFIG_HOME/kit/telemetry.yaml', () => {
    expect(consentPath()).toBe(path.join(tmpRoot, 'kit', 'telemetry.yaml'));
  });
});

describe('loadConsent', () => {
  it('returns deniedConsent when the file is missing', async () => {
    const got = await loadConsent();
    expect(got).toEqual(deniedConsent);
  });

  it('returns deniedConsent for malformed YAML', async () => {
    await writeConsentFile('this: is: not: yaml: [oops');
    const got = await loadConsent();
    expect(got).toEqual(deniedConsent);
  });

  it('returns deniedConsent when the root is not a mapping', async () => {
    await writeConsentFile('- just-a-list\n- of-strings\n');
    const got = await loadConsent();
    expect(got).toEqual(deniedConsent);
  });

  it('returns deniedConsent when telemetry block is missing', async () => {
    await writeConsentFile('unrelated:\n  key: value\n');
    const got = await loadConsent();
    expect(got).toEqual(deniedConsent);
  });

  it('returns deniedConsent when telemetry.consent block is missing', async () => {
    await writeConsentFile('telemetry:\n  other: stuff\n');
    const got = await loadConsent();
    expect(got).toEqual(deniedConsent);
  });

  it('returns deniedConsent for unknown state value', async () => {
    await writeConsentFile(
      [
        'telemetry:',
        '  consent:',
        '    state: maybe',
        '    prompt_version: 1',
        '',
      ].join('\n'),
    );
    const got = await loadConsent();
    expect(got).toEqual(deniedConsent);
  });

  it('parses a granted decision with all fields', async () => {
    await writeConsentFile(
      [
        'telemetry:',
        '  consent:',
        '    state: granted',
        '    prompt_version: 1',
        '    decision_source: prompt',
        '    decided_at: 2026-05-19T12:00:00Z',
        '',
      ].join('\n'),
    );
    const got = await loadConsent();
    expect(got).toEqual({
      allowed: true,
      promptVersion: 1,
      decisionSource: 'prompt',
      decidedAt: '2026-05-19T12:00:00Z',
    });
  });

  it('parses a denied decision with all fields', async () => {
    await writeConsentFile(
      [
        'telemetry:',
        '  consent:',
        '    state: denied',
        '    prompt_version: 2',
        '    decision_source: prompt',
        '    decided_at: 2026-05-19T12:00:00Z',
        '',
      ].join('\n'),
    );
    const got = await loadConsent();
    expect(got).toEqual({
      allowed: false,
      promptVersion: 2,
      decisionSource: 'prompt',
      decidedAt: '2026-05-19T12:00:00Z',
    });
  });

  it('defaults missing optional fields to safe values', async () => {
    await writeConsentFile(
      ['telemetry:', '  consent:', '    state: granted', ''].join('\n'),
    );
    const got = await loadConsent();
    expect(got.allowed).toBe(true);
    expect(got.promptVersion).toBe(0);
    expect(got.decisionSource).toBe('config');
    expect(got.decidedAt).toBeUndefined();
  });

  it('preserves sibling config keys without breaking parsing', async () => {
    // The file is the kit AppConfig; siblings of telemetry.consent may
    // exist. Loader must ignore them.
    await writeConsentFile(
      [
        'unrelated:',
        '  partition: ok',
        'telemetry:',
        '  other_partition: yes',
        '  consent:',
        '    state: granted',
        '    prompt_version: 3',
        '',
      ].join('\n'),
    );
    const got = await loadConsent();
    expect(got.allowed).toBe(true);
    expect(got.promptVersion).toBe(3);
  });
});
