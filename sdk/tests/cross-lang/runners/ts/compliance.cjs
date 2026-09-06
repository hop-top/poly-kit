#!/usr/bin/env node
/**
 * Node runner for the cross-language compliance conformance harness.
 *
 * Runs the in-tree SDK's compliance checker against the shared opt-in
 * fixture and emits the observed score, denominator, and per-factor
 * status as a single stable JSON object to
 * KIT_CROSS_LANG_COMPLIANCE_OUT.
 *
 * Unlike the ordering runner, this one does NOT consume dist/: the
 * compliance module is not part of the package exports map and tsup does
 * not build it, so there is no bundle to read. The orchestrator bundles
 * src/compliance.ts with the SDK's own esbuild into a temp CJS file and
 * passes the path in KIT_CROSS_LANG_COMPLIANCE_TS_BUNDLE — which also
 * guarantees the harness tests the working tree rather than a stale
 * artifact.
 *
 * Only the STATIC pass runs (empty binary path). Runtime checks execute a
 * binary, which no port could agree on across languages, and F13 is a
 * static check in every port anyway.
 *
 * Keys are emitted sorted and `factors` is an object keyed by factor
 * number rather than a list, so a port that reorders its results without
 * changing any status still compares equal. Order is not the subject here
 * — the score, the denominator, and the per-factor verdicts are.
 */

'use strict';

const fs = require('node:fs');

function sortedStringify(obj) {
  // JSON.stringify visits object keys in insertion order, so build the
  // object with keys already sorted rather than relying on the encoder.
  const sortDeep = (v) => {
    if (Array.isArray(v)) return v.map(sortDeep);
    if (v && typeof v === 'object') {
      const out = {};
      for (const k of Object.keys(v).sort()) out[k] = sortDeep(v[k]);
      return out;
    }
    return v;
  };
  return JSON.stringify(sortDeep(obj), null, 2) + '\n';
}

function main() {
  const fixture = process.env.KIT_CROSS_LANG_COMPLIANCE_FIXTURE;
  const out = process.env.KIT_CROSS_LANG_COMPLIANCE_OUT;
  const bundle = process.env.KIT_CROSS_LANG_COMPLIANCE_TS_BUNDLE;
  if (!fixture || !out || !bundle) {
    console.error(
      'KIT_CROSS_LANG_COMPLIANCE_FIXTURE, _OUT and _TS_BUNDLE must be set',
    );
    return 2;
  }

  const { run } = require(bundle);
  const report = run('', fixture);

  const factors = {};
  const names = {};
  for (const r of report.results) {
    factors[String(r.factor)] = r.status;
    names[String(r.factor)] = r.name;
  }

  fs.writeFileSync(
    out,
    sortedStringify({
      lang: 'ts',
      score: report.score,
      total: report.total,
      factors,
      names,
    }),
  );
  return 0;
}

process.exit(main());
