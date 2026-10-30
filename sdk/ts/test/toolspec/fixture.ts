/**
 * Builds the shared cross-language toolspec fixture tree
 * (sdk/tests/cross-lang/fixtures/toolspec-manifest.tree.json) as a
 * commander program, mirroring the Go cobra builder in
 * go/ai/toolspec/cli/manifest_crosslang_test.go.
 */
import * as fs from "fs";
import * as path from "path";
import { Command, Option } from "commander";

export const FIXTURES = path.resolve(__dirname, "../../../tests/cross-lang/fixtures");
export const CONTRACTS = path.resolve(__dirname, "../../../../contracts");

interface TreeFlag {
  name: string;
  short?: string;
  type: "bool" | "string" | "stringSlice";
  default?: string | string[];
  description?: string;
  hidden?: boolean;
  /** cobra MarkFlagRequired / commander makeOptionMandatory. */
  required?: boolean;
  /** kit flag enum (cli.FlagEnumAnnotation) / commander .choices(). */
  enum?: string[];
}

interface TreeCommand {
  name: string;
  short?: string;
  long?: string;
  hidden?: boolean;
  run?: boolean;
  annotations?: Record<string, string>;
  flags?: TreeFlag[];
  commands?: TreeCommand[];
}

export interface Tree {
  tool: string;
  version: string;
  reserved: string[];
  commands: TreeCommand[];
}

function addFixtureCommand(parent: Command, tc: TreeCommand): void {
  const cmd = parent.command(tc.name, { hidden: tc.hidden ?? false });
  if (tc.short && tc.long) cmd.summary(tc.short).description(tc.long);
  else if (tc.short) cmd.description(tc.short);
  if (tc.run) cmd.action(() => {});
  if (tc.annotations) {
    (cmd as Command & { annotations?: Record<string, string> }).annotations = {
      ...tc.annotations,
    };
  }
  for (const f of tc.flags ?? []) {
    const short = f.short ? `-${f.short}, ` : "";
    const arg =
      f.type === "bool" ? "" : f.type === "string" ? " <value>" : " <values...>";
    const opt = new Option(`${short}--${f.name}${arg}`, f.description);
    if (f.default !== undefined) opt.default(f.default);
    if (f.hidden) opt.hideHelp();
    if (f.required) opt.makeOptionMandatory();
    if (f.enum) opt.choices(f.enum);
    cmd.addOption(opt);
  }
  for (const child of tc.commands ?? []) addFixtureCommand(cmd, child);
}

/**
 * The fixture program, with createCLI's `-v, --version` on the root.
 */
export function fixtureProgram(): { program: Command; tree: Tree } {
  const tree = JSON.parse(
    fs.readFileSync(path.join(FIXTURES, "toolspec-manifest.tree.json"), "utf-8"),
  ) as Tree;
  const program = new Command(tree.tool).version(tree.version, "-v, --version");
  for (const c of tree.commands) addFixtureCommand(program, c);
  return { program, tree };
}
