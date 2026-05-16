// Package invoke builds native argv for agent CLIs (Claude, Codex,
// Gemini, OpenCode, …) from one normalized Invocation. Adapters
// live at hop.top/kit/go/core/uxp/invoke/adapters/<cli>/.
//
// Build is pure: it returns a CommandSpec plus a Diagnostics slice
// describing every shim or unsupported option encountered. Execution
// (Runner) is optional and side-effecting; callers wire it explicitly.
//
// See docs/specs/uxp-agent-cli-facade.md for the design.
package invoke
