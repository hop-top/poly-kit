/**
 * commands/compliance.ts — 12-factor AI CLI compliance checks
 */

import { Command } from "commander";
import * as path from "path";
import { run, formatReport } from "../../../../sdk/ts/src/compliance";

export function complianceCommand(): Command {
  return new Command("compliance")
    .description("Run 12-factor AI CLI compliance checks")
    .option("--static", "Run static checks only")
    .option("--format <fmt>", "Output format (text, json)", "text")
    .option("--spec <path>", "Path to toolspec YAML")
    .action((opts) => {
      const specPath =
        opts.spec ??
        path.resolve(__dirname, "../../spaced.toolspec.yaml");

      const binaryPath = opts.static ? "" : process.argv[0];
      const report = run(
        opts.static ? "" : binaryPath,
        specPath,
      );
      process.stdout.write(formatReport(report, opts.format));
    });
}
