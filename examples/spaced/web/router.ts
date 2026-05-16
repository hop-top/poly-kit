/**
 * router.ts — browser command router for spaced.
 *
 * Pure-function implementation of every spaced command. No Node.js, no
 * Commander, no process. All output returned as strings.
 * Data imported directly from the TS data layer.
 */

import {
  MISSIONS, VEHICLES, COMPETITORS, DAEMONS,
  ELON_QUOTES,
  findMission, findVehicle, findCompetitor, findDaemon,
  pick,
  type Mission, type Vehicle, type Competitor, type Daemon,
} from '../ts/data';
import {
  SERVICE_SURFACES,
  findServiceSurface,
  runServiceSmoke,
  type ServiceSurface,
} from './service-surfaces';

// ---------------------------------------------------------------------------
// Result type
// ---------------------------------------------------------------------------

export interface CmdResult {
  out:  string;
  err:  string;
  code: number;
}

function ok(out: string): CmdResult  { return { out, err: '', code: 0 }; }
function fail(err: string): CmdResult { return { out: '', err, code: 1 }; }

// ---------------------------------------------------------------------------
// Formatting helpers
// ---------------------------------------------------------------------------

function pad(s: string, n: number): string {
  return s.length >= n ? s.slice(0, n - 1) + ' ' : s.padEnd(n);
}

function box(title: string, rows: [string, string][]): string {
  const maxKey  = Math.max(...rows.map(([k]) => k.length));
  const maxVal  = Math.max(...rows.map(([, v]) => v.length));
  const width   = Math.max(title.length + 4, maxKey + maxVal + 6, 56);
  const inner   = width - 2;
  const top     = '  ╭─ ' + title + ' ' + '─'.repeat(Math.max(0, inner - title.length - 3)) + '╮';
  const bot     = '  ╰' + '─'.repeat(inner) + '╯';
  const lines   = [top];
  for (const [k, v] of rows) {
    const kp = k.padEnd(maxKey + 2);
    lines.push(`  │  ${kp}${v.padEnd(inner - maxKey - 6)}│`);
  }
  lines.push(bot);
  return lines.join('\n');
}

function table(headers: string[], colWidths: number[], rows: string[][]): string {
  const headerRow = '  ' + headers.map((h, i) => pad(h, colWidths[i])).join('');
  const sep       = '  ' + '─'.repeat(colWidths.reduce((a, b) => a + b, 0));
  const dataRows  = rows.map(r => '  ' + r.map((c, i) => pad(c, colWidths[i])).join(''));
  return ['', headerRow, sep, ...dataRows, ''].join('\n');
}

function outcomeIcon(o: string): string {
  return o === 'SUCCESS' ? '✓' : o === 'RUD' ? '✗' : o === 'PARTIAL' ? '~' : '?';
}

// ---------------------------------------------------------------------------
// Commands
// ---------------------------------------------------------------------------

// ── mission list ─────────────────────────────────────────────────────────────

function missionList(format: string): CmdResult {
  if (format === 'json') {
    return ok(JSON.stringify(MISSIONS, null, 2));
  }
  if (format === 'yaml') {
    const lines = ['missions:'];
    for (const m of MISSIONS) {
      lines.push(`  - name: "${m.name}"`);
      lines.push(`    vehicle: "${m.vehicle}"`);
      lines.push(`    date: ${m.date}`);
      lines.push(`    outcome: ${m.outcome}`);
    }
    return ok(lines.join('\n'));
  }
  const out = table(
    ['MISSION', 'VEHICLE', 'DATE', 'OUTCOME', 'MARKET MOOD'],
    [22, 18, 12, 10, 22],
    MISSIONS.map(m => [
      m.name,
      m.vehicle,
      m.date,
      `${outcomeIcon(m.outcome)} ${m.outcome}`,
      pick(m.market_mood),
    ]),
  ) + '\n  * RUD = Rapid Unscheduled Disassembly  (company terminology, not ours)\n';
  return ok(out);
}

// ── mission inspect ───────────────────────────────────────────────────────────

function missionInspect(name: string): CmdResult {
  const m = findMission(name);
  if (!m) return fail(`  error: mission not found: "${name}"\n  Run 'spaced mission list' to see available missions.`);
  const rows: [string, string][] = [
    ['Vehicle',   m.vehicle],
    ['Date',      m.date],
    ['Outcome',   `${outcomeIcon(m.outcome)} ${m.outcome}`],
    ['Mood',      pick(m.market_mood)],
    ['Note',      pick(m.notes)],
  ];
  if ((m as Mission & { elon_quote?: string[] }).elon_quote) {
    rows.push(['Elon quote', pick((m as Mission & { elon_quote: string[] }).elon_quote)]);
  }
  return ok(box(`MISSION: ${m.name}`, rows));
}

// ── mission search ────────────────────────────────────────────────────────────

function missionSearch(query: string): CmdResult {
  const q = query.toLowerCase();
  const results = MISSIONS.filter(m =>
    m.name.toLowerCase().includes(q) ||
    m.vehicle.toLowerCase().includes(q) ||
    m.outcome.toLowerCase().includes(q) ||
    m.notes.some(n => n.toLowerCase().includes(q)),
  );
  if (!results.length) {
    return ok(`  No missions matched "${query}".\n  (The missions happened though. We have footage.)`);
  }
  const lines = ['', `  Found ${results.length} mission(s) matching "${query}":`, ''];
  for (const m of results) {
    lines.push(`  ${outcomeIcon(m.outcome)} ${m.name}  [${m.vehicle} · ${m.date}]`);
  }
  lines.push('');
  return ok(lines.join('\n'));
}

// ── launch ────────────────────────────────────────────────────────────────────

function launch(missionName: string, opts: {
  payload?: string; orbit?: string; dryRun?: boolean; output?: string;
}): CmdResult {
  const m = findMission(missionName) ?? { name: missionName, vehicle: 'Unknown', outcome: 'TBD', notes: ['Mission not in the historical record.'] };
  const payload = opts.payload ? opts.payload.split(',').map(s => s.trim()) : ['unspecified'];
  const orbit   = opts.orbit ?? 'TBD';
  const rows: [string, string][] = [
    ['Mission', m.name],
    ['Vehicle', m.vehicle],
    ['Orbit',   orbit],
    ['Payload', payload.join(', ')],
  ];
  if (opts.dryRun) {
    rows.push(['Status', 'DRY RUN — no rockets harmed']);
    if (m.outcome === 'RUD') {
      rows.push(['Historical note', `This ended in ${m.outcome}. ${pick(m.notes)}`]);
    }
    return ok('\n  ⚡ DRY RUN — no actual rockets will be harmed\n\n' + box(`LAUNCH: ${m.name}`, rows));
  }
  rows.push(['Status', 'LAUNCHED ✓']);
  return ok(box(`LAUNCH: ${m.name}`, rows));
}

// ── abort ─────────────────────────────────────────────────────────────────────

const ABORT_PRECEDENTS = [
  'Cybertruck specs', 'Hyperloop', 'Tesla Semi', 'Full Self-Driving',
  '"funding secured"', 'Optimus', 'the Boring Company flamethrower',
  'Twitter purchase',
];

function abortMission(missionName: string, reason: string): CmdResult {
  const out = [
    '',
    '  ✗ ABORT SEQUENCE INITIATED',
    '',
    `  Reason           ${reason || 'unspecified'}`,
    '  Validity         Legally sufficient (probably)',
    `  Precedent        Yes (see: ${ABORT_PRECEDENTS.slice(0, 4).join(', ')},`,
    `                   ${ABORT_PRECEDENTS.slice(4).join(', ')})`,
    '',
    '  Mission scrubbed. Range safety nominal.',
    '  The tweet has since been deleted.',
    '  The SEC has since filed a subpoena.',
    '',
    '  Have a nice day.',
    '',
  ].join('\n');
  return ok(out);
}

// ── telemetry get ─────────────────────────────────────────────────────────────

function telemetryGet(missionName: string, format: string): CmdResult {
  const m = findMission(missionName);
  if (!m) return fail(`  error: mission not found: "${missionName}"`);

  const channels = [
    { channel: 'altitude',      value: String(100 + Math.floor(Math.random() * 300)), unit: 'km',  status: 'NOMINAL' },
    { channel: 'velocity',      value: String(2700 + Math.floor(Math.random() * 500)), unit: 'm/s', status: 'NOMINAL' },
    { channel: 'throttle',      value: `${80 + Math.floor(Math.random() * 15)}%`,     unit: '%',   status: 'NOMINAL' },
    { channel: 'propellant',    value: `${60 + Math.floor(Math.random() * 30)}%`,     unit: '%',   status: m.outcome === 'RUD' ? 'ANOMALY' : 'NOMINAL' },
    { channel: 'engine-status', value: m.outcome === 'RUD' ? 'RUD IMMINENT' : 'NOMINAL', unit: '—', status: m.outcome === 'RUD' ? 'WARNING' : 'NOMINAL' },
    { channel: 'comms',         value: 'established', unit: '—', status: 'NOMINAL' },
  ];

  if (format === 'json') {
    return ok(JSON.stringify(channels, null, 2));
  }
  if (format === 'yaml') {
    const lines = ['telemetry:'];
    for (const c of channels) {
      lines.push(`  - channel: ${c.channel}`);
      lines.push(`    value: "${c.value}"`);
      lines.push(`    unit: "${c.unit}"`);
      lines.push(`    status: ${c.status}`);
    }
    return ok(lines.join('\n'));
  }
  const rows = table(
    ['CHANNEL', 'VALUE', 'UNIT', 'STATUS'],
    [20, 16, 8, 12],
    channels.map(c => [c.channel, c.value, c.unit, c.status]),
  );
  return ok(`\n  TELEMETRY: ${m.name}\n` + rows);
}

// ── fleet list ────────────────────────────────────────────────────────────────

function fleetList(): CmdResult {
  const out = table(
    ['VEHICLE', 'TYPE', 'STATUS', 'LAUNCHES'],
    [22, 30, 16, 10],
    VEHICLES.map(v => [v.name, v.type, v.status, String(v.launches)]),
  );
  return ok(out);
}

// ── fleet vehicle inspect ─────────────────────────────────────────────────────

function fleetVehicleInspect(name: string, systems?: string): CmdResult {
  const v = findVehicle(name);
  if (!v) return fail(`  error: unknown vehicle: "${name}"\n  Run 'spaced fleet list' to see available vehicles.`);
  const rows: [string, string][] = [
    ['Type',      v.type],
    ['Status',    v.status],
    ['Launches',  String(v.launches)],
    ['Reusable',  v.reusable ? 'Yes' : 'No'],
    ['Fun fact',  v.fun_fact],
  ];
  let sysSection = '';
  if (v.systems && Object.keys(v.systems).length) {
    const wanted = systems
      ? systems.split(',').map(s => s.trim().toLowerCase())
      : Object.keys(v.systems);
    const filtered = Object.entries(v.systems).filter(([k]) =>
      wanted.some(w => k.toLowerCase().includes(w)),
    );
    if (filtered.length) {
      sysSection = '\n\n  Systems:\n' + filtered.map(([k, desc]) =>
        `    ${k.padEnd(20)} ${desc}`,
      ).join('\n');
    }
  }
  return ok(box(`VEHICLE: ${v.name}`, rows) + sysSection);
}

// ── starship status ───────────────────────────────────────────────────────────

function starshipStatus(): CmdResult {
  const out = [
    '',
    '  ╭─ STARSHIP — THE BIG ONE ─────────────────────────────────────────────╮',
    '  │  Height     121m  (taller than the Statue of Liberty. noted.)        │',
    '  │  Thrust     ~74.4 MN  (more than any rocket ever built)              │',
    '  │  Status     FLIGHT TEST (enthusiastically)                           │',
    '  │                                                                      │',
    '  │  IFT-1  2023-04-20   RUD at 4 min   "It cleared the pad!" — Elon    │',
    '  │  IFT-2  2023-11-18   RUD at 8 min   "Successful!" — also Elon       │',
    '  │  IFT-3  2024-03-14   RUD at 49 min  Getting warmer.                 │',
    '  │  IFT-4  2024-06-06   SUCCESS        Crowd lost minds.               │',
    '  │  IFT-5  2024-10-13   SUCCESS        Booster caught by Mechazilla.   │',
    '  │  IFT-6  2025-01-16   PARTIAL        Booster caught. Ship lost.      │',
    '  │                                                                      │',
    '  │  Moon contract   NASA, $2.9B. Blue Origin: very normal about it.    │',
    '  │  Mars plan       "2026 uncrewed. 2029 crewed." (filed: ambitious)   │',
    '  │                                                                      │',
    '  │  Mechazilla note                                                     │',
    '  │    A robot caught a 70-ton rocket booster with chopsticks.          │',
    '  │    This is real. You are awake. Everything is fine.                 │',
    '  ╰──────────────────────────────────────────────────────────────────────╯',
    '',
  ].join('\n');
  return ok(out);
}

// ── elon status ───────────────────────────────────────────────────────────────

const VENTURES = [
  'Tesla (TSLA) — EVs, FSD, energy, shareholder litigation',
  'SpaceX — rockets, Starlink, DOGE-adjacent orbital superiority',
  'X (formerly Twitter) — social media, payments, everything app aspirations',
  'Neuralink — brain-computer interface, FDA approval: partial',
  'The Boring Company — tunnels, Vegas Loop, still boring',
  'xAI (Grok) — AGI, or at least a chatbot with fewer restrictions',
  'DOGE — Department of Government Efficiency (advisory, unofficial, load-bearing)',
];

const MOODS = [
  'POSTING (threat level: elevated)',
  'ACQUIRING SOMETHING (due diligence: incomplete)',
  'TWEETING AT REGULATORS (response: pending)',
  'FOUNDING A COMPANY (count: 7)',
  'NOMINALIZING AN ANOMALY (outcome: RUD)',
  'OPTIMIZING (headcount: downward)',
];

function elonStatus(): CmdResult {
  const rows: [string, string][] = [
    ['Full name',  'Elon Reeve Musk'],
    ['Born',       '1971-06-28, Pretoria, South Africa'],
    ['Net worth',  'fluctuates with TSLA; check Bloomberg Terminal'],
    ['Mood',       pick(MOODS)],
    ['Quote',      `"${pick(ELON_QUOTES)}"`],
    ['SEC status', 'ongoing'],
    ['Children',   '12 (confirmed) (probably)'],
    ['Mars ETA',   '"2029" (was 2018, 2022, 2024, 2026)'],
  ];
  const ventures = '\n\n  Current roles:\n' + VENTURES.map(v => `    • ${v}`).join('\n');
  const disclaimer = '\n\n  This dashboard is satirical. Any resemblance to actual\n  executive behavior is unfortunate but accurate.\n';
  return ok(box('ELON MUSK — STATUS DASHBOARD', rows) + ventures + disclaimer);
}

// ── ipo status ────────────────────────────────────────────────────────────────

function ipoStatus(): CmdResult {
  const timeline: [string, string][] = [
    ['2012', '"Maybe someday" — Elon'],
    ['2017', '"Not while Mars is uncolonized" — Elon'],
    ['2020', '"Starlink IPO first" — Elon'],
    ['2024', '"2025 maybe" — Elon (new Elon)'],
    ['2025', '👀'],
  ];
  const rows: [string, string][] = [
    ['Status',      'Imminent™  (since 2012)'],
    ['Valuation',   '$350B  (Elon\'s number, unaudited, vibes-based)'],
    ['SEC mood',    'Complicated 🌹'],
    ['Prediction',  'It will happen when Starship lands on Mars,'],
    ['',            'whichever comes first.'],
  ];
  const tl = '\n\n  Timeline:\n' + timeline.map(([y, n]) => `    ${y.padEnd(8)}${n}`).join('\n');
  return ok(box('SPACEX IPO WATCH', rows) + tl + '\n');
}

// ── competitor compare ────────────────────────────────────────────────────────

function competitorCompare(name: string, metric?: string): CmdResult {
  const c = findCompetitor(name);
  if (!c) {
    const names = COMPETITORS.map(x => x.name).join(', ');
    return fail(`  error: competitor not found: "${name}"\n  Known: ${names}`);
  }
  const rows: [string, string][] = [
    ['Founded',     String(c.founded)],
    ['Rockets',     c.rockets.join(', ')],
    ['Success rate', c.launch_success_rate],
    ['Achievement', c.notable_achievement],
    ['Failure',     c.notable_failure],
    ['Elon opinion', c.elon_opinion],
  ];
  if (c.metrics) {
    const wanted = metric
      ? metric.split(',').map(s => s.trim().toLowerCase())
      : Object.keys(c.metrics);
    for (const [k, v] of Object.entries(c.metrics)) {
      if (wanted.some(w => k.toLowerCase().includes(w))) {
        rows.push([k, v]);
      }
    }
  }
  rows.push(['vs SpaceX', 'SpaceX Falcon 9: ~$2,600/kg to LEO (reused)']);
  rows.push(['',          'SpaceX Starship: ~$100/kg (target; aspirational)']);
  return ok(box(`COMPETITOR: ${c.name}`, rows) +
    '\n\n  Disclaimer: This comparison is satirical. SpaceX may also have\n  failures we have chosen not to highlight out of narrative convenience.\n');
}

// ── daemon list ───────────────────────────────────────────────────────────────

function daemonList(): CmdResult {
  const rows = DAEMONS.map(d => [d.id, d.started, d.status]);
  const out = [
    '',
    '  ╭─ ACTIVE DAEMONS ───────────────────────────────────────────────────╮',
    '  │  These processes are running in the background. Allegedly.         │',
    '  ╰────────────────────────────────────────────────────────────────────╯',
    '',
    table(['ID', 'STATUS', 'SINCE'], [36, 12, 12], rows),
    `  ${DAEMONS.filter(d => d.status === 'RUNNING').length} daemons running. Use 'spaced daemon status <id>' for media references.`,
    `  Use 'spaced daemon stop <id>' to attempt termination. Good luck.`,
    '',
  ].join('\n');
  return ok(out);
}

// ── daemon status ─────────────────────────────────────────────────────────────

function daemonStatus(id: string): CmdResult {
  const d = findDaemon(id);
  if (!d) return fail(`  error: daemon not found: "${id}"\n  Run 'spaced daemon list' to see active daemons.`);
  const rows: [string, string][] = [
    ['ID',      d.id],
    ['Status',  d.status],
    ['Started', d.started],
    ['Summary', d.summary],
  ];
  const refs = d.references.length
    ? '\n\n  Media references:\n' + d.references.map(r =>
        `    → ${r.source} (${r.date})\n      "${r.summary}"${r.author ? `\n       — ${r.author}` : ''}`,
      ).join('\n\n')
    : '';
  return ok(box(`DAEMON: ${d.id}`, rows) + refs + '\n');
}

// ── daemon stop ───────────────────────────────────────────────────────────────

const STOP_LORE: Record<string, string> = {
  'funding-secured':              'The SEC tried. It cost $40M and took 4 years. The tweet is still up.',
  'twitter-acquisition-chaos':    'You cannot stop the chaos. The chaos is the product.',
  'doge-conflict-of-interest':    'This daemon cannot be stopped by CLI. It can only be stopped by Congress.\n  Congress has questions about whether it has the authority to stop it.',
  'starship-faa-delays':          'Resolved in 2024. But the memory lingers. And the permits are still slow.',
  'tesla-autopilot-investigations':'NHTSA is still investigating. NHTSA is very patient.',
  'spacex-settlement-nlrb':       'Settled. Undisclosed terms. The daemon considers itself victorious.',
  'neuralink-animal-welfare':     'Reuters published the findings. The findings remain published.',
  'sec-vs-elon-twitter-poll':     'The poll closed. The SEC did not close. These are different things.',
};

function daemonStop(id: string, all: boolean): CmdResult {
  if (all) {
    const out = [
      '',
      `  ✗ STOP FAILED: all daemons (${DAEMONS.length}/${DAEMONS.length})`,
      '',
      `  Stopped             : 0`,
      `  Still running       : ${DAEMONS.length}`,
      '  New daemons spawned : 1',
      '    → musk-response-to-this-cli  [RUNNING since just now]',
      '',
      '  The daemons are self-perpetuating. This is a known issue.',
      '  Recommended action: document them. Stop filing for injunctions.',
      '',
    ].join('\n');
    return ok(out);
  }

  const d = findDaemon(id);
  const lore = STOP_LORE[id] ?? 'This daemon has legal representation. It will not be stopped.';
  const name = d?.id ?? id;
  const out = [
    '',
    `  ✗ STOP FAILED: ${name}`,
    '',
    `  ${lore}`,
    '',
    `  Suggestion: try  spaced daemon stop --all`,
    `  (it won\'t work either, but the error message is funnier)`,
    '',
  ].join('\n');
  return ok(out);
}

// ── service surfaces ─────────────────────────────────────────────────────────

function serviceList(format: string): CmdResult {
  if (format === 'json') {
    return ok(JSON.stringify(SERVICE_SURFACES, null, 2));
  }
  if (format === 'yaml') {
    const lines = ['services:'];
    for (const s of SERVICE_SURFACES) {
      lines.push(`  - id: ${s.id}`);
      lines.push(`    kind: ${s.kind}`);
      lines.push(`    transport: "${s.transport}"`);
      lines.push(`    status: ${s.status}`);
      lines.push(`    endpoints: ${s.endpoints.length}`);
    }
    return ok(lines.join('\n'));
  }
  return ok(table(
    ['SURFACE', 'KIND', 'TRANSPORT', 'ENDPOINTS', 'STATUS'],
    [18, 12, 30, 10, 10],
    SERVICE_SURFACES.map(s => [
      s.id,
      s.kind,
      s.transport,
      String(s.endpoints.length),
      s.status,
    ]),
  ) + '\n  Run spaced service inspect <surface> for endpoint samples.\n');
}

function serviceInspect(id: string, format: string): CmdResult {
  const surface = findServiceSurface(id);
  if (!surface) {
    const names = SERVICE_SURFACES.map(s => s.id).join(', ');
    return fail(`  error: service surface not found: "${id}"\n  Known: ${names}`);
  }
  if (format === 'json') {
    return ok(JSON.stringify(surface, null, 2));
  }
  if (format === 'yaml') {
    return ok(serviceSurfaceYaml(surface));
  }

  const rows: [string, string][] = [
    ['ID', surface.id],
    ['Kind', surface.kind],
    ['Transport', surface.transport],
    ['Status', surface.status],
    ['Summary', surface.summary],
  ];
  const endpoints = surface.endpoints.map(e => [
    `  ${e.method.padEnd(8)} ${e.path}`,
    `    ${e.purpose}`,
    `    sample: ${samplePreview(e.sample)}`,
  ].join('\n')).join('\n\n');
  return ok(box(`SERVICE: ${surface.name}`, rows) + '\n\n  Endpoints:\n' + endpoints + '\n');
}

function serviceSmoke(format: string): CmdResult {
  const checks = runServiceSmoke();
  if (format === 'json') {
    return ok(JSON.stringify(checks, null, 2));
  }
  const rows = checks.map(c => [c.surface, c.ok ? 'ok' : 'fail', c.detail]);
  return ok(table(['SURFACE', 'STATUS', 'DETAIL'], [18, 10, 28], rows));
}

function serviceSurfaceYaml(surface: ServiceSurface): string {
  const lines = [
    `id: ${surface.id}`,
    `name: "${surface.name}"`,
    `kind: ${surface.kind}`,
    `transport: "${surface.transport}"`,
    `status: ${surface.status}`,
    'endpoints:',
  ];
  for (const e of surface.endpoints) {
    lines.push(`  - method: ${e.method}`);
    lines.push(`    path: "${e.path}"`);
    lines.push(`    purpose: "${e.purpose}"`);
  }
  return lines.join('\n');
}

function samplePreview(sample: string): string {
  const compact = sample.replace(/\s+/g, ' ').trim();
  return compact.length > 120 ? compact.slice(0, 117) + '...' : compact;
}

// ---------------------------------------------------------------------------
// Argument parser (shell-like tokeniser)
// ---------------------------------------------------------------------------

function tokenise(input: string): string[] {
  const tokens: string[] = [];
  let cur = '';
  let inQ = false;
  let qCh = '';
  for (const ch of input.trim()) {
    if (inQ) {
      if (ch === qCh) { inQ = false; }
      else { cur += ch; }
    } else if (ch === '"' || ch === "'") {
      inQ = true; qCh = ch;
    } else if (ch === ' ') {
      if (cur) { tokens.push(cur); cur = ''; }
    } else {
      cur += ch;
    }
  }
  if (cur) tokens.push(cur);
  return tokens;
}

// ---------------------------------------------------------------------------
// Top-level router
// ---------------------------------------------------------------------------

export function route(input: string): CmdResult {
  const tokens = tokenise(input);
  if (!tokens.length) return ok('');

  // Strip leading "spaced" if present.
  const args = tokens[0] === 'spaced' ? tokens.slice(1) : tokens;
  if (!args.length) return showHelp();

  // Global flags.
  let format = 'table';
  const clean: string[] = [];
  for (let i = 0; i < args.length; i++) {
    if (args[i] === '--format' && args[i + 1]) { format = args[++i]; }
    else if (args[i]?.startsWith('--format=')) { format = args[i].slice(9); }
    else if (args[i] === '--help' || args[i] === '-h') { return showHelp(); }
    else if (args[i] === '--version' || args[i] === '-v') {
      return ok('spaced 0.1.0');
    }
    else if (args[i] === '--quiet' || args[i] === '--no-color') { /* ignore in browser */ }
    else { clean.push(args[i]); }
  }

  const [cmd, sub, ...rest] = clean;

  switch (cmd) {

    // ── mission ──
    case 'mission':
      if (!sub || sub === 'list') return missionList(format);
      if (sub === 'inspect')      return missionInspect(rest[0] ?? '');
      if (sub === 'search') {
        const qi = rest.indexOf('--query');
        const q  = qi >= 0 ? rest[qi + 1] : rest[0];
        return missionSearch(q ?? '');
      }
      return fail(`  error: unknown mission subcommand: "${sub}"`);

    // ── launch ──
    case 'launch': {
      const mName   = sub ?? '';
      const payIdx  = rest.indexOf('--payload');
      const orbIdx  = rest.indexOf('--orbit');
      const outIdx  = rest.findIndex(a => a === '-o' || a === '--output');
      const dryRun  = rest.includes('--dry-run');
      return launch(mName, {
        payload: payIdx >= 0 ? rest[payIdx + 1] : undefined,
        orbit:   orbIdx >= 0 ? rest[orbIdx + 1] : undefined,
        dryRun,
        output:  outIdx >= 0 ? rest[outIdx + 1] : undefined,
      });
    }

    // ── abort ──
    case 'abort': {
      const ri = rest.indexOf('--reason');
      return abortMission(sub ?? '', ri >= 0 ? rest[ri + 1] : '');
    }

    // ── telemetry ──
    case 'telemetry':
      if (sub === 'get') return telemetryGet(rest[0] ?? '', format);
      return fail(`  error: unknown telemetry subcommand: "${sub ?? ''}"`);

    // ── countdown ──
    case 'countdown':
      return ok(`\n  T-minus: unknown  (${sub ?? 'unspecified'} not scheduled)\n  Stand by. Or don't. We're not your boss.\n`);

    // ── fleet ──
    case 'fleet':
      if (!sub || sub === 'list') return fleetList();
      if (sub === 'vehicle' && rest[0] === 'inspect') {
        const nameIdx = 1;
        const sysIdx  = rest.indexOf('--systems');
        const name    = rest[nameIdx] ?? '';
        const systems = sysIdx >= 0 ? rest[sysIdx + 1] : undefined;
        return fleetVehicleInspect(name, systems);
      }
      return fail(`  error: unknown fleet subcommand: "${sub}"`);

    // ── starship ──
    case 'starship':
      if (!sub || sub === 'status') return starshipStatus();
      if (sub === 'history')        return starshipStatus(); // same data
      return fail(`  error: unknown starship subcommand: "${sub}"`);

    // ── elon ──
    case 'elon':
      if (!sub || sub === 'status') return elonStatus();
      return fail(`  error: unknown elon subcommand: "${sub}"`);

    // ── ipo ──
    case 'ipo':
      if (!sub || sub === 'status') return ipoStatus();
      return fail(`  error: unknown ipo subcommand: "${sub}"`);

    // ── competitor ──
    case 'competitor':
      if (sub === 'compare') {
        const mi = rest.findIndex(a => a === '--metric');
        return competitorCompare(rest[0] ?? '', mi >= 0 ? rest[mi + 1] : undefined);
      }
      return fail(`  error: unknown competitor subcommand: "${sub}"`);

    // ── daemon ──
    case 'daemon':
      if (!sub || sub === 'list')   return daemonList();
      if (sub === 'status')         return daemonStatus(rest[0] ?? '');
      if (sub === 'stop') {
        const all = rest.includes('--all');
        return daemonStop(all ? '' : rest.find(r => !r.startsWith('--')) ?? '', all);
      }
      return fail(`  error: unknown daemon subcommand: "${sub}"`);

    // ── service ──
    case 'service':
      if (!sub || sub === 'list')    return serviceList(format);
      if (sub === 'inspect')         return serviceInspect(rest[0] ?? '', format);
      if (sub === 'smoke')           return serviceSmoke(format);
      return fail(`  error: unknown service subcommand: "${sub}"`);

    default:
      return fail(`  error: unknown command: "${cmd}"\n  Run 'spaced --help' to see available commands.`);
  }
}

// ---------------------------------------------------------------------------
// Demo / suggestion content
// ---------------------------------------------------------------------------

export const DEMO_SEQUENCE: string[] = [
  'spaced --help',
  'spaced mission list',
  'spaced mission inspect starman',
  'spaced daemon list',
  'spaced daemon stop funding-secured',
  'spaced service list',
  'spaced service inspect websocket',
  'spaced elon status',
  'spaced starship status',
  'spaced ipo status',
  'spaced competitor compare boeing',
];

export const SUGGESTIONS: string[] = [
  'mission list',
  'mission inspect SN8',
  'starship status',
  'starship history',
  'elon status',
  'ipo status',
  'daemon list',
  'daemon status funding-secured',
  'daemon stop --all',
  'service list',
  'service inspect websocket',
  'service inspect mcp',
  'service smoke',
  'competitor compare "Blue Origin"',
  'fleet list',
  'fleet vehicle inspect "Falcon 9"',
  'launch starman --dry-run',
  'launch SN8 --payload cargo,crew --orbit leo',
  'mission list --format json',
  'telemetry get starman',
];

// ---------------------------------------------------------------------------
// Help text
// ---------------------------------------------------------------------------

function showHelp(): CmdResult {
  const out = `
  spaced — satirical SpaceX CLI historian

  Usage: spaced [options] <command>

  Commands:
    mission list                    List all missions
    mission inspect <name>          Deep-dive a mission
    mission search --query <q>      Search missions
    launch <mission>                Launch sequence (satirical)
    abort <mission> --reason <r>    Abort a mission
    telemetry get <mission>         Mission telemetry
    fleet list                      List vehicle fleet
    fleet vehicle inspect <name>    Inspect a vehicle
    starship status                 Starship program overview
    elon status                     Elon Musk status report
    ipo status                      SpaceX IPO watch
    competitor compare <name>       Compare vs competitor
    daemon list                     List active daemons
    daemon status <id>              Daemon details + media refs
    daemon stop <id>                Attempt to stop (fails gracefully)
    daemon stop --all               Attempt to stop all (also fails)
    service list                    List adjacent service surfaces
    service inspect <surface>       Show REST/WebSocket/MCP endpoint samples
    service smoke                   Validate service samples are populated

  Options:
    --format <fmt>    table | json | yaml  (default: table)
    --quiet           Suppress non-essential output
    --no-color        Disable ANSI colour
    -v, --version     Print version
    -h, --help        This help

  Not affiliated with, endorsed by, or in any way authorized by SpaceX,
  Elon Musk, DOGE, NASA, the FAA, or the Starman mannequin currently past Mars.
  We would, however, accept a sponsorship (https://github.com/sponsors/hop-top).
  Cash, Starlink credits, or a ride on the next Crew Dragon all acceptable.
`;
  return ok(out);
}
