# tldr

## What it answers

Example invocations from a tldr-pages markdown page, turned into
`toolspec.Workflow` entries. Wrong package for commands and flags
(`hop.top/kit/go/ai/toolspec/sources/help`) or error patterns
(`hop.top/kit/go/ai/toolspec/sources/thefuck`).

## Use it when

- you fetched or vendored a tldr page: `tldr.ParseTldrPage(name, markdown)`; each `- Step` line followed by a backticked command becomes one single-step workflow

## Contract

- Reads: the markdown text only; fetching the page is the caller's job. No I/O, no subprocess.
- Trust: medium. Content is community-maintained and may lag the tool's current flags; steps are examples, not a contract.
- A command line without a preceding `- Step` line uses the command itself as the workflow name.
- No snippet here: the input is third-party content the caller fetches.

## Neighbours

- `hop.top/kit/go/ai/toolspec/sources/usp`: workflows learned from local sessions instead of published examples.
- `hop.top/kit/go/ai/toolspec`: `Merge` keeps the first non-empty `Workflows` slice when chaining.

## See also

- [ToolSpec API reference](../../../../../docs/adopters/reference/toolspec-api.md)
