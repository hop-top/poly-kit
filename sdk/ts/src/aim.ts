/**
 * aim.ts — AI model registry: types, source, cache, registry, query parser.
 * Port of Go's aim package.
 */
import * as fs from 'fs';
import * as path from 'path';
import { cacheDir } from './xdg';

// ── Types ──────────────────────────────────────────────────────
export interface Modalities { input?: string[]; output?: string[] }
export interface Limit { context?: number; output?: number }
export interface Cost { input?: number; output?: number }

export interface Model {
  id: string; name: string; provider?: string; family?: string;
  input: string[]; output: string[];
  tool_call: boolean; reasoning: boolean; open_weights: boolean;
  context: number; maxOutput: number;
  costInput: number; costOutput: number;
  modalities?: Modalities; limit?: Limit; cost?: Cost;
}

export interface Provider {
  id: string; name: string; models: Record<string, Model>;
}

export interface Filter {
  input?: string; output?: string; provider?: string; family?: string;
  toolCall?: boolean; reasoning?: boolean; openWeights?: boolean;
  query?: string;
}

/** Flatten nested wire fields into top-level Model fields. */
export function normalize(m: Model): Model {
  const n = { ...m };
  n.input = n.modalities?.input ?? n.input ?? [];
  n.output = n.modalities?.output ?? n.output ?? [];
  n.context = n.limit?.context ?? n.context ?? 0;
  n.maxOutput = n.limit?.output ?? n.maxOutput ?? 0;
  n.costInput = n.cost?.input ?? n.costInput ?? 0;
  n.costOutput = n.cost?.output ?? n.costOutput ?? 0;
  n.tool_call = n.tool_call ?? false;
  n.reasoning = n.reasoning ?? false;
  n.open_weights = n.open_weights ?? false;
  return n;
}

// ── Source ──────────────────────────────────────────────────────
export interface Source {
  fetch(signal?: AbortSignal): Promise<Record<string, Provider>>;
}
export interface ETagSource extends Source {
  fetchWithETag(etag: string, signal?: AbortSignal): Promise<{
    providers: Record<string, Provider> | null;
    etag: string; notModified: boolean;
  }>;
}

export interface ModelsDevSourceOptions {
  url?: string; timeout?: number; maxSize?: number;
}

function validateProviders(raw: Record<string, Provider>) {
  for (const [key, p] of Object.entries(raw)) {
    if (key !== p.id)
      throw new Error(`aim: map key "${key}" != provider.id "${p.id}"`);
    for (const [id, m] of Object.entries(p.models)) {
      const normalized = normalize(m);
      if (!normalized.provider) normalized.provider = key;
      p.models[id] = normalized;
    }
  }
  return raw;
}

export class ModelsDevSource implements ETagSource {
  private url: string;
  private timeout: number;
  private maxSize: number;

  constructor(opts?: ModelsDevSourceOptions) {
    this.url = opts?.url ?? 'https://models.dev/api.json';
    this.timeout = opts?.timeout ?? 30_000;
    this.maxSize = opts?.maxSize ?? 50 * 1024 * 1024;
  }

  private doFetch(headers: Record<string, string>, signal?: AbortSignal) {
    const ctrl = new AbortController();
    const timer = setTimeout(() => ctrl.abort(), this.timeout);
    if (signal) signal.addEventListener('abort', () => ctrl.abort(), { once: true });
    return { ctrl, timer, promise: globalThis.fetch(this.url, {
      headers, signal: ctrl.signal,
    }) };
  }

  private async readBody(resp: Response) {
    const text = await resp.text();
    if (text.length > this.maxSize)
      throw new Error(`aim: response exceeds max size (${this.maxSize} bytes)`);
    return validateProviders(JSON.parse(text));
  }

  async fetch(signal?: AbortSignal): Promise<Record<string, Provider>> {
    const { timer, promise } = this.doFetch({ Accept: 'application/json' }, signal);
    try {
      const resp = await promise;
      if (resp.status !== 200)
        throw new Error(`aim: unexpected status ${resp.status} from ${this.url}`);
      return this.readBody(resp);
    } finally { clearTimeout(timer); }
  }

  async fetchWithETag(etag: string, signal?: AbortSignal) {
    const hdrs: Record<string, string> = { Accept: 'application/json' };
    if (etag) hdrs['If-None-Match'] = etag;
    const { timer, promise } = this.doFetch(hdrs, signal);
    try {
      const resp = await promise;
      if (resp.status === 304)
        return { providers: null, etag, notModified: true as const };
      if (resp.status !== 200)
        throw new Error(`aim: unexpected status ${resp.status} from ${this.url}`);
      const providers = await this.readBody(resp);
      return {
        providers, notModified: false as const,
        etag: resp.headers.get('etag') ?? '',
      };
    } finally { clearTimeout(timer); }
  }
}

// ── Cache ──────────────────────────────────────────────────────
const DATA_FILE = 'providers.json';
const ETAG_FILE = 'etag';
const LOCK_FILE = '.lock';
const LOCK_STALE_MS = 30_000;
const LOCK_POLL_MS = 50;
const LOCK_TIMEOUT_MS = 10_000;

function acquireLock(dir: string): void {
  const lockPath = path.join(dir, LOCK_FILE);
  const deadline = Date.now() + LOCK_TIMEOUT_MS;
  while (true) {
    try {
      fs.writeFileSync(lockPath, String(process.pid), { flag: 'wx' });
      return;
    } catch {
      // break stale locks older than LOCK_STALE_MS
      try {
        const stat = fs.statSync(lockPath);
        if (Date.now() - stat.mtimeMs > LOCK_STALE_MS) {
          fs.unlinkSync(lockPath);
          continue;
        }
      } catch { /* lock disappeared */ continue; }
      if (Date.now() >= deadline)
        throw new Error('aim: lock acquisition timed out');
      // busy-wait (sync cache is already blocking)
      const until = Date.now() + LOCK_POLL_MS;
      while (Date.now() < until) { /* spin */ }
    }
  }
}

function releaseLock(dir: string): void {
  try { fs.unlinkSync(path.join(dir, LOCK_FILE)); } catch { /* ok */ }
}

interface CacheEnvelope {
  fetched_at: string; providers: Record<string, Provider>;
}
export interface CacheOptions { ttl?: number; dir?: string }

export class Cache implements Source {
  private src: Source;
  private ttl: number;
  private dir: string;

  constructor(src: Source, opts?: CacheOptions) {
    this.src = src;
    this.ttl = opts?.ttl ?? 86_400_000;
    this.dir = opts?.dir ?? path.join(cacheDir(''), 'hop/aim');
  }

  async fetch(signal?: AbortSignal): Promise<Record<string, Provider>> {
    const env = this.load();
    if (env && Date.now() - new Date(env.fetched_at).getTime() < this.ttl)
      return env.providers;
    try { return await this.refresh(signal); }
    catch {
      if (env) return env.providers;
      throw new Error('aim: fetch failed and no cached data available');
    }
  }

  async forceRefresh(signal?: AbortSignal) { return this.refresh(signal); }

  private async refresh(signal?: AbortSignal) {
    fs.mkdirSync(this.dir, { recursive: true, mode: 0o755 });
    acquireLock(this.dir);
    try {
      let providers: Record<string, Provider>;
      const src = this.src;
      if (typeof (src as ETagSource).fetchWithETag === 'function') {
        const etag = this.readFile(ETAG_FILE);
        const r = await (src as ETagSource).fetchWithETag(etag, signal);
        if (r.notModified) {
          const env = this.load();
          if (env) { env.fetched_at = new Date().toISOString(); this.store(env); return env.providers; }
          // cache corrupt/missing despite 304 — fall through to unconditional fetch
        }
        providers = r.providers ?? await src.fetch(signal);
        if (r.etag) this.writeFile(ETAG_FILE, r.etag);
      } else {
        providers = await src.fetch(signal);
      }
      this.store({ fetched_at: new Date().toISOString(), providers });
      return providers;
    } finally { releaseLock(this.dir); }
  }

  private load(): CacheEnvelope | null {
    try {
      const env: CacheEnvelope = JSON.parse(
        fs.readFileSync(path.join(this.dir, DATA_FILE), 'utf-8'),
      );
      // re-normalize to populate fields lost in JSON round-trip
      for (const [key, p] of Object.entries(env.providers)) {
        for (const [id, m] of Object.entries(p.models)) {
          const n = normalize(m);
          if (!n.provider) n.provider = key;
          p.models[id] = n;
        }
      }
      return env;
    } catch { return null; }
  }

  private store(env: CacheEnvelope) {
    const tmp = path.join(this.dir, DATA_FILE + '.tmp');
    fs.writeFileSync(tmp, JSON.stringify(env), { mode: 0o644 });
    fs.renameSync(tmp, path.join(this.dir, DATA_FILE));
  }

  private readFile(name: string): string {
    try { return fs.readFileSync(path.join(this.dir, name), 'utf-8'); }
    catch { return ''; }
  }

  private writeFile(name: string, data: string) {
    const tmp = path.join(this.dir, name + '.tmp');
    try { fs.writeFileSync(tmp, data, { mode: 0o644 }); fs.renameSync(tmp, path.join(this.dir, name)); }
    catch { /* best-effort */ }
  }
}

// ── Query Parser ───────────────────────────────────────────────
export function parseQuery(q: string): Filter {
  const f: Filter = {};
  const free: string[] = [];
  for (let i = 0, n = q.length; i < n;) {
    if (q[i] === ' ' || q[i] === '\t') { i++; continue; }
    let val: string; let quoted = false;
    if (q[i] === '"') {
      const j = q.indexOf('"', i + 1);
      if (j < 0) throw new Error('unterminated quote');
      val = q.slice(i + 1, j); quoted = true; i = j + 1;
    } else {
      let j = i;
      while (j < n && q[j] !== ' ' && q[j] !== '\t') j++;
      val = q.slice(i, j); i = j;
    }
    if (quoted) { free.push(val); continue; }
    const ci = val.indexOf(':');
    if (ci < 0) { free.push(val); continue; }
    const key = val.slice(0, ci), v = val.slice(ci + 1);
    if (key === '') throw new Error(`malformed tag "${val}": missing key`);
    if (v === '') throw new Error(`empty value for tag "${key}"`);
    switch (key) {
      case 'provider': f.provider = csv(f.provider, v); break;
      case 'family': f.family = csv(f.family, v); break;
      case 'in': f.input = csv(f.input, v); break;
      case 'out': f.output = csv(f.output, v); break;
      case 'toolcall': case 'reasoning': case 'openweights': {
        const b = toBool(v, key);
        if (key === 'toolcall') f.toolCall = b;
        else if (key === 'reasoning') f.reasoning = b;
        else f.openWeights = b;
        break;
      }
      default: throw new Error(`unknown tag "${key}"`);
    }
  }
  if (free.length > 0) f.query = free.join(' ');
  return f;
}

function toBool(v: string, tag: string): boolean {
  if (v === 'true') return true;
  if (v === 'false') return false;
  throw new Error(`invalid bool value "${v}" for tag "${tag}": must be true or false`);
}

function csv(a: string | undefined, b: string) { return a ? a + ',' + b : b; }

// ── Registry ───────────────────────────────────────────────────
export interface RegistryOptions {
  sources?: Source[]; cacheDir?: string; cacheTTL?: number;
}

export class Registry {
  private cache: Cache;
  private providers: Record<string, Provider> | null = null;

  constructor(opts?: RegistryOptions) {
    const sources = opts?.sources ?? [new ModelsDevSource()];
    const merged: Source = {
      async fetch(signal?: AbortSignal) {
        const r: Record<string, Provider> = {};
        for (const s of sources) Object.assign(r, await s.fetch(signal));
        return r;
      },
    };
    this.cache = new Cache(merged, { dir: opts?.cacheDir, ttl: opts?.cacheTTL });
  }

  async ensure(): Promise<void> {
    if (this.providers) return;
    try { this.providers = await this.cache.fetch(); }
    catch { this.providers = {}; }
  }

  async refresh() { this.providers = await this.cache.forceRefresh(); }

  async getProviders(): Promise<Provider[]> {
    await this.ensure();
    return Object.values(this.providers!).sort((a, b) => a.id.localeCompare(b.id));
  }

  async models(f: Filter): Promise<Model[]> {
    await this.ensure();
    const out: Model[] = [];
    for (const p of Object.values(this.providers!))
      for (const m of Object.values(p.models))
        if (matchesFilter(m, f)) out.push(m);
    out.sort((a, b) =>
      a.provider !== b.provider
        ? (a.provider ?? '').localeCompare(b.provider ?? '')
        : a.id.localeCompare(b.id));
    return out;
  }

  async get(provider: string, model: string): Promise<Model | undefined> {
    await this.ensure();
    return this.providers![provider]?.models[model];
  }

  async query(q: string): Promise<Model[]> { return this.models(parseQuery(q)); }
}

// ── Filter matching ────────────────────────────────────────────
function csvMatch(filter: string, value: string | undefined): boolean {
  return filter.split(',').some(v => v.trim() === value);
}

function matchesFilter(m: Model, f: Filter): boolean {
  if (f.provider && !csvMatch(f.provider, m.provider)) return false;
  if (f.family && !csvMatch(f.family, m.family)) return false;
  if (f.input && !modSubset(f.input, m.input)) return false;
  if (f.output && !modSubset(f.output, m.output)) return false;
  if (f.toolCall !== undefined && m.tool_call !== f.toolCall) return false;
  if (f.reasoning !== undefined && m.reasoning !== f.reasoning) return false;
  if (f.openWeights !== undefined && m.open_weights !== f.openWeights) return false;
  if (f.query) {
    const q = f.query.toLowerCase();
    if (!m.id.toLowerCase().includes(q) && !m.name.toLowerCase().includes(q)) return false;
  }
  return true;
}

function modSubset(filter: string, mods: string[]): boolean {
  const set = new Set(mods.map(v => v.toLowerCase().trim()));
  for (const w of filter.split(',')) {
    const t = w.toLowerCase().trim();
    if (t && !set.has(t)) return false;
  }
  return true;
}
