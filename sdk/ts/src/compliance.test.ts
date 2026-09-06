import { describe, it, expect } from "vitest";
import * as path from "path";
import * as fs from "fs";
import * as os from "os";
import {
  Factor,
  factorName,
  runStatic,
  run,
  formatReport,
  type CheckResult,
} from "./compliance";

const TOOLSPEC = path.resolve(
  __dirname,
  "../../../examples/spaced/spaced.toolspec.yaml",
);

describe("compliance", () => {
  describe("factorName", () => {
    it("returns name for all 13 factors", () => {
      for (
        let f = Factor.SelfDescribing;
        f <= Factor.ConsentingTelemetry;
        f++
      ) {
        expect(factorName(f)).not.toBe("");
        expect(factorName(f)).not.toMatch(/^Factor\(/);
      }
    });
  });

  describe("runStatic", () => {
    it("checks spaced toolspec", () => {
      const results = runStatic(TOOLSPEC);
      expect(results.length).toBe(12);

      const byFactor = new Map<number, CheckResult>();
      for (const r of results) byFactor.set(r.factor, r);

      // Passing factors
      expect(byFactor.get(Factor.SelfDescribing)?.status)
        .toBe("pass");
      expect(byFactor.get(Factor.StructuredIO)?.status)
        .toBe("pass");
      expect(byFactor.get(Factor.ContractsErrors)?.status)
        .toBe("pass");
      expect(byFactor.get(Factor.Preview)?.status)
        .toBe("pass");
      expect(byFactor.get(Factor.Idempotency)?.status)
        .toBe("pass");
      expect(byFactor.get(Factor.StateTransparency)?.status)
        .toBe("pass");
      expect(byFactor.get(Factor.SafeDelegation)?.status)
        .toBe("pass");
      expect(byFactor.get(Factor.Evolution)?.status)
        .toBe("pass");

      // Runtime-only (skipped)
      expect(byFactor.get(Factor.StreamDiscipline)?.status)
        .toBe("skip");
      expect(byFactor.get(Factor.ObservableOps)?.status)
        .toBe("skip");
      expect(byFactor.get(Factor.Provenance)?.status)
        .toBe("skip");
    });

    it("fails on empty spec", () => {
      const tmp = path.join(
        os.tmpdir(),
        `compliance-test-${Date.now()}.yaml`,
      );
      fs.writeFileSync(tmp, "name: empty\n");
      try {
        const results = runStatic(tmp);
        const failing = results.filter(
          (r) => r.status === "fail",
        );
        expect(failing.length).toBeGreaterThan(0);
      } finally {
        fs.unlinkSync(tmp);
      }
    });
  });

  describe("run", () => {
    it("returns report with static-only", () => {
      const report = run("", TOOLSPEC);
      expect(report.total).toBe(12);
      expect(report.score).toBeGreaterThanOrEqual(1);
      expect(report.toolspec).toBe(TOOLSPEC);
    });
  });

  describe("formatReport", () => {
    const report = {
      binary: "test-bin",
      toolspec: "test.yaml",
      total: 12,
      score: 8,
      results: [
        {
          factor: Factor.SelfDescribing,
          name: "Self-Describing",
          status: "pass" as const,
          details: "ok",
        },
        {
          factor: Factor.StructuredIO,
          name: "Structured I/O",
          status: "fail" as const,
          details: "missing",
          suggestion: "Add output_schema",
        },
      ],
    };

    it("renders text format", () => {
      const out = formatReport(report, "text");
      expect(out).toContain("Self-Describing");
      expect(out).toContain("PASS");
      expect(out).toContain("FAIL");
      expect(out).toContain("8/12");
    });

    it("renders json format", () => {
      const out = formatReport(report, "json");
      const parsed = JSON.parse(out);
      expect(parsed.score).toBe(8);
      expect(parsed.total).toBe(12);
      expect(parsed.results).toHaveLength(2);
    });
  });

  describe("F13 Consenting Telemetry", () => {
    const wellFormed = `name: probe
schema_version: "1"
commands:
  - name: ping
  - name: telemetry
    children:
      - name: status
      - name: enable
      - name: disable
      - name: reset
      - name: inspect
telemetry:
  enabled: true
  categories: [invocation]
  sinks: [bus]
  consent_command: "probe telemetry"
  consent_subcommands: [status, enable, disable, reset, inspect]
  kill_switch_envs: [DO_NOT_TRACK, PROBE_TELEMETRY_MODE]
  prompt_version: "v1"
  redact_rules: kit-default
`;

    /** Run a toolspec body and return the whole report — both the F13
     *  row and the denominator are asserted per case. */
    function report(body: string) {
      const tmp = path.join(
        os.tmpdir(),
        `compliance-f13-${Date.now()}-${Math.random()
          .toString(36)
          .slice(2)}.yaml`,
      );
      fs.writeFileSync(tmp, body);
      try {
        return run("", tmp);
      } finally {
        fs.unlinkSync(tmp);
      }
    }

    function f13(r: ReturnType<typeof report>): CheckResult {
      const row = r.results.find(
        (x) => x.factor === Factor.ConsentingTelemetry,
      );
      expect(row, "F13 row must always be present").toBeDefined();
      return row!;
    }

    it("names the factor", () => {
      expect(factorName(Factor.ConsentingTelemetry))
        .toBe("Consenting Telemetry");
    });

    it("skips when not opted in", () => {
      const r = report(
        'name: probe\nschema_version: "1"\ncommands:\n  - name: ping\n',
      );
      expect(f13(r).status).toBe("skip");
      expect(r.total).toBe(12);
    });

    it("skips on a null telemetry key", () => {
      // A bare `telemetry:` key parses as null, not an object. It is
      // still not an opt-in, so it must skip rather than throw.
      const r = report(
        'name: probe\nschema_version: "1"\ncommands:\n  - name: ping\ntelemetry:\n',
      );
      expect(f13(r).status).toBe("skip");
      expect(r.total).toBe(12);
    });

    it("runs when opted in", () => {
      const r = report(wellFormed);
      expect(f13(r).status).toBe("pass");
      expect(r.total).toBe(13);
    });

    it("passes a well-formed block", () => {
      const r = report(wellFormed);
      const row = f13(r);
      expect(row.status).toBe("pass");
      expect(row.details).toContain("well-formed");
      expect(r.total).toBe(13);
    });

    it("fails on empty categories", () => {
      const r = report(
        wellFormed.replace(
          "categories: [invocation]",
          "categories: []",
        ),
      );
      const row = f13(r);
      expect(row.status).toBe("fail");
      expect(row.details).toContain("categories");
      expect(row.suggestion).toBeTruthy();
      expect(r.total).toBe(13);
    });

    it("fails on a missing canonical subcommand", () => {
      const r = report(
        wellFormed.replace(
          "consent_subcommands: [status, enable, disable, reset, inspect]",
          "consent_subcommands: [status, enable, disable]",
        ),
      );
      const row = f13(r);
      expect(row.status).toBe("fail");
      expect(row.details).toContain("reset");
      expect(row.details).toContain("inspect");
      expect(r.total).toBe(13);
    });

    it("fails when DO_NOT_TRACK is absent", () => {
      const r = report(
        wellFormed.replace(
          "kill_switch_envs: [DO_NOT_TRACK, PROBE_TELEMETRY_MODE]",
          "kill_switch_envs: [PROBE_TELEMETRY_MODE]",
        ),
      );
      const row = f13(r);
      expect(row.status).toBe("fail");
      expect(row.details).toContain("DO_NOT_TRACK");
      expect(r.total).toBe(13);
    });

    it("fails when no mode env is declared", () => {
      const r = report(
        wellFormed.replace(
          "kill_switch_envs: [DO_NOT_TRACK, PROBE_TELEMETRY_MODE]",
          "kill_switch_envs: [DO_NOT_TRACK]",
        ),
      );
      const row = f13(r);
      expect(row.status).toBe("fail");
      expect(row.details).toContain("TELEMETRY_MODE");
      expect(r.total).toBe(13);
    });

    it("accepts KIT_TELEMETRY_MODE", () => {
      // The regex covers the kit literal without a special-case branch.
      const r = report(
        wellFormed.replace("PROBE_TELEMETRY_MODE", "KIT_TELEMETRY_MODE"),
      );
      expect(f13(r).status).toBe("pass");
      expect(r.total).toBe(13);
    });

    it("accepts an app-prefixed mode env", () => {
      const r = report(
        wellFormed.replace("PROBE_TELEMETRY_MODE", "SPACED_TELEMETRY_MODE"),
      );
      expect(f13(r).status).toBe("pass");
      expect(r.total).toBe(13);
    });

    it("fails on an empty prompt_version", () => {
      const r = report(
        wellFormed.replace('prompt_version: "v1"', 'prompt_version: ""'),
      );
      const row = f13(r);
      expect(row.status).toBe("fail");
      expect(row.details).toContain("prompt_version");
      // The field name is locked; the failure names the rejected alias.
      expect(row.details).toContain("consent_version");
      expect(r.total).toBe(13);
    });

    it("fails on empty redact_rules", () => {
      const r = report(
        wellFormed.replace(
          "redact_rules: kit-default",
          'redact_rules: ""',
        ),
      );
      const row = f13(r);
      expect(row.status).toBe("fail");
      expect(row.details).toContain("redact_rules");
      expect(r.total).toBe(13);
    });

    it("fails when a subcommand is absent from the commands tree", () => {
      // `inspect` is declared but has no node under `telemetry`.
      const r = report(
        wellFormed.replace("      - name: inspect\n", ""),
      );
      const row = f13(r);
      expect(row.status).toBe("fail");
      expect(row.details).toContain("not in commands tree");
      expect(row.details).toContain("telemetry inspect");
      expect(r.total).toBe(13);
    });
  });
});
