/**
 * @module plan
 * @package @hop-top/kit
 *
 * The structured dry-run output of a write or destructive command. Ports
 * Go `cli.Plan` / `cli.RenderPlan`: a handler that sees `isDryRun(cmd)`
 * builds a {@link Plan} of what it would change and renders it instead of
 * acting.
 */

import * as yaml from 'js-yaml';

import { render, TABLE_FORMAT } from './output.js';

/** One declared state change a {@link Plan} would apply. */
export interface Effect {
  /** A short verb: `create`, `update`, `delete`. */
  kind: string;
  /** The addressable resource (`alias:foo`, `/etc/hosts`). */
  target: string;
  /** Whether the inverse command restores prior state losslessly. */
  reversible: boolean;
  /** A free-form one-line note. */
  detail?: string;
}

/** What a command would do if re-issued without `--dry-run`. */
export interface Plan {
  /** The full command path; filled in by {@link renderPlan} when empty. */
  command?: string;
  /** The resolved arguments. */
  args?: Record<string, unknown>;
  /** The ordered changes. */
  effects?: Effect[];
  /** Pre-flight checks that passed while planning. */
  prerequisites_checked?: string[];
  /** Non-fatal advisories for the operator. */
  warnings?: string[];
  /** RFC 3339 UTC time of assembly; stamped when empty. */
  generated_at?: string;
}

/** `YYYY-MM-DDTHH:MM:SSZ` for now. */
function rfc3339Now(): string {
  return new Date().toISOString().replace(/\.\d{3}Z$/, 'Z');
}

/** The plan as its wire object: Go struct key order, empty optionals off. */
function toWire(p: Plan, commandPath: string): Record<string, unknown> {
  const w: Record<string, unknown> = { command: p.command || commandPath };
  if (p.args && Object.keys(p.args).length > 0) w['args'] = p.args;
  w['effects'] = (p.effects ?? []).map((e) => {
    const o: Record<string, unknown> = { kind: e.kind, target: e.target, reversible: e.reversible };
    if (e.detail) o['detail'] = e.detail;
    return o;
  });
  if (p.prerequisites_checked && p.prerequisites_checked.length > 0) {
    w['prerequisites_checked'] = p.prerequisites_checked;
  }
  if (p.warnings && p.warnings.length > 0) w['warnings'] = p.warnings;
  w['generated_at'] = p.generated_at || rfc3339Now();
  return w;
}

/**
 * Writes `plan` as the command's dry-run output in `format`: `json` and
 * `yaml` serialize it, anything else prints the plan table. An empty
 * `command` defaults to `commandPath` and an empty `generated_at` to now.
 */
export function renderPlan(
  w: { write(chunk: string): unknown },
  format: string,
  plan: Plan,
  commandPath = '',
): void {
  const wire = toWire(plan, commandPath);
  if (format === 'json') {
    w.write(`${JSON.stringify(wire, null, 2)}\n`);
    return;
  }
  if (format === 'yaml') {
    w.write(yaml.dump(wire));
    return;
  }
  w.write(`COMMAND   ${wire['command'] as string}\n`);
  w.write(`GENERATED ${wire['generated_at'] as string}\n`);
  for (const warn of plan.warnings ?? []) w.write(`WARNING   ${warn}\n`);
  render(w as NodeJS.WritableStream, TABLE_FORMAT, wire['effects']);
}
