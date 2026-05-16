import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import * as os from 'os';
import * as fs from 'fs';
import * as path from 'path';
import { configDir, dataDir, cacheDir, stateDir, mustEnsure } from './xdg';

// Helper: save + restore a set of env vars around a test.
function withEnv(vars: Record<string, string | undefined>, fn: () => void): void {
  const saved: Record<string, string | undefined> = {};
  for (const k of Object.keys(vars)) {
    saved[k] = process.env[k];
    if (vars[k] === undefined) {
      delete process.env[k];
    } else {
      process.env[k] = vars[k] as string;
    }
  }
  try {
    fn();
  } finally {
    for (const k of Object.keys(saved)) {
      if (saved[k] === undefined) {
        delete process.env[k];
      } else {
        process.env[k] = saved[k] as string;
      }
    }
  }
}

// ─── configDir ──────────────────────────────────────────────────────────────

describe('configDir', () => {
  it('uses XDG_CONFIG_HOME when set', () => {
    withEnv({ XDG_CONFIG_HOME: '/tmp/xdg-cfg' }, () => {
      expect(configDir('mytool')).toBe('/tmp/xdg-cfg/mytool');
    });
  });

  it('falls back to OS config dir when XDG_CONFIG_HOME is unset', () => {
    withEnv({ XDG_CONFIG_HOME: undefined }, () => {
      const result = configDir('mytool');
      // Must end with /mytool and be an absolute path.
      expect(path.isAbsolute(result)).toBe(true);
      expect(result.endsWith(path.sep + 'mytool') || result.endsWith('/mytool')).toBe(true);
    });
  });

  it('appends tool name to XDG_CONFIG_HOME', () => {
    withEnv({ XDG_CONFIG_HOME: '/custom/cfg' }, () => {
      expect(configDir('myapp')).toBe('/custom/cfg/myapp');
    });
  });
});

// ─── dataDir ────────────────────────────────────────────────────────────────

describe('dataDir', () => {
  it('uses XDG_DATA_HOME when set', () => {
    withEnv({ XDG_DATA_HOME: '/tmp/xdg-data' }, () => {
      expect(dataDir('mytool')).toBe('/tmp/xdg-data/mytool');
    });
  });

  it('falls back to OS-native path on current platform when XDG_DATA_HOME unset', () => {
    withEnv({ XDG_DATA_HOME: undefined }, () => {
      const result = dataDir('mytool');
      expect(path.isAbsolute(result)).toBe(true);
      expect(result.endsWith(path.sep + 'mytool') || result.endsWith('/mytool')).toBe(true);
    });
  });

  it('darwin fallback: ~/Library/Application Support/<tool>', () => {
    withEnv({ XDG_DATA_HOME: undefined }, () => {
      // Simulate darwin by passing override (tests the internal path logic).
      const home = os.homedir();
      const result = dataDir('mytool', 'darwin' as NodeJS.Platform);
      expect(result).toBe(path.join(home, 'Library', 'Application Support', 'mytool'));
    });
  });

  it('linux fallback: ~/.local/share/<tool>', () => {
    withEnv({ XDG_DATA_HOME: undefined }, () => {
      const home = os.homedir();
      const result = dataDir('mytool', 'linux' as NodeJS.Platform);
      expect(result).toBe(path.join(home, '.local', 'share', 'mytool'));
    });
  });

  it('win32 fallback: %LocalAppData%\\<tool>', () => {
    withEnv({ XDG_DATA_HOME: undefined, LocalAppData: 'C:\\Users\\user\\AppData\\Local' }, () => {
      const result = dataDir('mytool', 'win32' as NodeJS.Platform);
      expect(result).toBe(path.win32.join('C:\\Users\\user\\AppData\\Local', 'mytool'));
    });
  });

  it('win32 throws when LocalAppData unset', () => {
    withEnv({ XDG_DATA_HOME: undefined, LocalAppData: undefined }, () => {
      expect(() => dataDir('mytool', 'win32' as NodeJS.Platform)).toThrow();
    });
  });
});

// ─── cacheDir ───────────────────────────────────────────────────────────────

describe('cacheDir', () => {
  it('uses XDG_CACHE_HOME when set', () => {
    withEnv({ XDG_CACHE_HOME: '/tmp/xdg-cache' }, () => {
      expect(cacheDir('mytool')).toBe('/tmp/xdg-cache/mytool');
    });
  });

  it('falls back to OS cache dir when XDG_CACHE_HOME unset', () => {
    withEnv({ XDG_CACHE_HOME: undefined }, () => {
      const result = cacheDir('mytool');
      expect(path.isAbsolute(result)).toBe(true);
      expect(result.endsWith(path.sep + 'mytool') || result.endsWith('/mytool')).toBe(true);
    });
  });
});

// ─── stateDir ───────────────────────────────────────────────────────────────

describe('stateDir', () => {
  it('uses XDG_STATE_HOME when set', () => {
    withEnv({ XDG_STATE_HOME: '/tmp/xdg-state' }, () => {
      expect(stateDir('mytool')).toBe('/tmp/xdg-state/mytool');
    });
  });

  it('darwin fallback: ~/Library/Application Support/<tool>/state', () => {
    withEnv({ XDG_STATE_HOME: undefined }, () => {
      const home = os.homedir();
      const result = stateDir('mytool', 'darwin' as NodeJS.Platform);
      expect(result).toBe(path.join(home, 'Library', 'Application Support', 'mytool', 'state'));
    });
  });

  it('linux fallback: ~/.local/state/<tool>', () => {
    withEnv({ XDG_STATE_HOME: undefined }, () => {
      const home = os.homedir();
      const result = stateDir('mytool', 'linux' as NodeJS.Platform);
      expect(result).toBe(path.join(home, '.local', 'state', 'mytool'));
    });
  });

  it('win32 fallback: %LocalAppData%\\<tool>\\state', () => {
    withEnv({ XDG_STATE_HOME: undefined, LocalAppData: 'C:\\Users\\user\\AppData\\Local' }, () => {
      const result = stateDir('mytool', 'win32' as NodeJS.Platform);
      expect(result).toBe(
        path.win32.join('C:\\Users\\user\\AppData\\Local', 'mytool', 'state'),
      );
    });
  });

  it('win32 throws when LocalAppData unset', () => {
    withEnv({ XDG_STATE_HOME: undefined, LocalAppData: undefined }, () => {
      expect(() => stateDir('mytool', 'win32' as NodeJS.Platform)).toThrow();
    });
  });

  it('falls back to OS-native path on current platform when XDG_STATE_HOME unset', () => {
    withEnv({ XDG_STATE_HOME: undefined }, () => {
      const result = stateDir('mytool');
      expect(path.isAbsolute(result)).toBe(true);
    });
  });
});

// ─── mustEnsure ─────────────────────────────────────────────────────────────

describe('mustEnsure', () => {
  let tmpBase: string;

  beforeEach(() => {
    tmpBase = fs.mkdtempSync(path.join(os.tmpdir(), 'xdg-test-'));
  });

  afterEach(() => {
    fs.rmSync(tmpBase, { recursive: true, force: true });
  });

  it('creates the directory and returns its path', () => {
    const target = path.join(tmpBase, 'nested', 'subdir');
    const result = mustEnsure(target);
    expect(result).toBe(target);
    expect(fs.existsSync(target)).toBe(true);
  });

  it('is idempotent — no error if dir already exists', () => {
    const target = path.join(tmpBase, 'existing');
    fs.mkdirSync(target);
    expect(() => mustEnsure(target)).not.toThrow();
    expect(fs.existsSync(target)).toBe(true);
  });

  it('throws when path is a file, not a directory', () => {
    const file = path.join(tmpBase, 'afile');
    fs.writeFileSync(file, 'data');
    expect(() => mustEnsure(file)).toThrow();
  });
});
