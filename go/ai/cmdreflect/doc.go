// Package cmdreflect is the single place kit reflects a cobra command
// tree into structured metadata.
//
// # The one-descriptor rule
//
// Every consumer that needs to know what a command IS reads a
// [Descriptor]. No consumer walks the cobra tree itself. Before this
// package existed, three independent walkers derived overlapping
// facts from the same tree — the ToolSpec walker, the Manifest
// builder, and the cmdsurface leaf discovery — and each one dropped a
// different subset of commands on the floor with no record of why.
// Divergence between them was invisible until a command showed up on
// one surface and not another.
//
// A [Tree] reflects EVERY command in the source tree. Nothing is
// silently dropped. A command a consumer must not invoke is still
// described, with [Descriptor.Invocable] false and a
// [NonInvocableReason] naming the rule that excluded it. Filtering is
// therefore a consumer decision applied to a complete reflection,
// never a walker decision applied to an incomplete one.
//
// # Consumers
//
//   - Spec generation — [hop.top/kit/go/ai/toolspec/cli] projects a
//     Tree into toolspec.ToolSpec and toolspec.Manifest for the
//     `<tool> spec` subcommand.
//   - MCP — [hop.top/kit/go/ai/toolspec/adapters] renders the MCP
//     tool envelope from the ToolSpec that the Tree produced, and
//     [hop.top/kit/go/transport/cmdsurface] gates the MCP tools/call
//     channel on the same descriptors.
//   - OpenAPI — REST projection reads Path, Args, Flags, and
//     OutputSchema off the descriptor to synthesize operations.
//   - Command transports — [hop.top/kit/go/transport/cmdsurface]
//     builds its Leaf set from Tree.Invocable and enforces
//     [Safety] through its Policy gate.
//
// # Shape
//
// [Reflect] takes a completed cobra root — every command registered,
// every flag declared — and returns a [Tree]. Reflecting a tree that
// is still being assembled produces a descriptor set that does not
// match what the binary actually exposes.
//
// The descriptor carries three groups of facts:
//
//   - Identity and documentation: Path, Use, Short, Long, Aliases.
//   - Interface: Flags (with types, defaults, and required-ness),
//     Args, and OutputSchema.
//   - Governance: Safety (tier ladder, permissions vocabulary,
//     confirmation), Surface metadata (hidden, deprecated, reserved,
//     transport annotations), and the Invocable / Reason pair.
//
// Safety reuses the vocabulary [hop.top/kit/go/ai/toolspec] already
// defines: the six-tier side-effect ladder and the kit: permission
// tokens. This package resolves annotations into that vocabulary; it
// does not define a competing one.
package cmdreflect
