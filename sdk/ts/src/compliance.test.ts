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
    it("returns name for all 12 factors", () => {
      for (
        let f = Factor.SelfDescribing;
        f <= Factor.AuthLifecycle;
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
});
