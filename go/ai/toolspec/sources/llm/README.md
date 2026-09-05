# llm

## What it answers

A last-resort `toolspec.Source` that asks an LLM for commands, error
patterns and workflows when no deterministic source knows the tool. Wrong
package when any static source applies; place it last in the chain.

## Use it when

- you wire it into a registry: `llm.NewLLMSource(llm.Config{Client: completer, Model: "...", Enabled: true})`
- `Enabled` is false: `Resolve` returns `nil, nil` and the chain moves on

## Contract

- Reads: nothing local. Sends the tool name in a fixed JSON-only prompt to `Config.Client` (`hop.top/kit/go/ai/llm.Completer`) and parses the reply, stripping markdown fences.
- Trust: low. Output is generated; `Provenance.Source` is `llm` and `Confidence` is 0.6 on every pattern and workflow. Harnesses must treat it as a hint.
- `Enabled` must be explicit; a nil `Client` with `Enabled` true is an error.
- Network egress and cost depend on the wired client.
- No snippet here: the source calls an LLM.

## Neighbours

- `hop.top/kit/go/ai/llm`: the provider-agnostic `Completer` this source talks to.
- `hop.top/kit/go/ai/toolspec/sources/thefuck`, `tldr`: deterministic sources to place before this one.

## See also

- [ToolSpec API reference](../../../../../docs/adopters/reference/toolspec-api.md)
- [llm package](../../../llm/README.md)
