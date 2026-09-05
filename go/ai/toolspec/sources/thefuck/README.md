# thefuck

## What it answers

Error-to-fix mappings extracted statically from thefuck Python rule files.
Wrong package for workflows (`hop.top/kit/go/ai/toolspec/sources/tldr`) or
for generated patterns when no rule exists (`hop.top/kit/go/ai/toolspec/sources/llm`).

## Use it when

- you have one rule's source: `thefuck.ParseRule(name, pythonSource)` returns an `ErrorPattern` or `nil, nil`
- you have a rule set: `thefuck.ParseRules(toolName, map[ruleName]source)` returns a spec with every extractable pattern

## Contract

- Reads: Python source text only. Rules that need runtime evaluation (`import re`, `re.search`, loops, `any(`, `get_close_matches`) are skipped, never executed.
- Extracts `'literal' in command.output` or `command.script` matches and the first `return` inside `get_new_command`; no match or no fix means the rule is dropped.
- Trust: medium. `Confidence` is 0.9 for one pattern, 0.8 for several; `Cause` is classified into `permission`, `missing_dep`, `conflict` or `bad_input` from rule name and pattern keywords; `Source` is `thefuck:<rule>`.
- No snippet here: the input is third-party rule source the caller vendors.

## Neighbours

- `hop.top/kit/go/ai/toolspec/cli`: curated `WithErrorPatterns` for tools you own; this package is for tools you do not.
- `hop.top/kit/go/ai/toolspec`: `ErrorPattern`, `Provenance`.

## See also

- [ToolSpec API reference](../../../../../docs/adopters/reference/toolspec-api.md)
