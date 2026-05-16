package codex

import "hop.top/kit/go/core/uxp/invoke"

// ToolCapabilities lists codex's documented built-in agent tools as
// of codex-cli 0.130.0. Codex's tool model is more sandbox-centric:
// most file/exec actions go through the configured sandbox tier
// (`-s`), and shell commands run via `exec_command` rather than a
// distinct `Bash` tool.
func (Adapter) ToolCapabilities() []invoke.ToolCapability {
	return []invoke.ToolCapability{
		{
			Universal: "shell.exec", NativeNames: []string{"exec_command"},
			Support: invoke.MappingNative, Permission: invoke.ToolExec,
			Transcript: invoke.TranscriptNative, Controllable: true,
			Notes: "subject to -s sandbox tier and -a approval policy",
		},
		{
			Universal: "file.read", NativeNames: []string{"exec_command"},
			Support: invoke.MappingShim, Permission: invoke.ToolRead,
			Transcript: invoke.TranscriptNative, Controllable: true,
			Notes: "codex reads files via shell; no distinct read tool",
		},
		{
			Universal: "file.write", NativeNames: []string{"exec_command"},
			Support: invoke.MappingShim, Permission: invoke.ToolWrite,
			Transcript: invoke.TranscriptNative, Controllable: true,
			Notes: "codex writes new files via shell (heredoc, tee, cat); apply_patch is file.edit",
		},
		{
			Universal: "file.edit", NativeNames: []string{"apply_patch"},
			Support: invoke.MappingNative, Permission: invoke.ToolWrite,
			Transcript: invoke.TranscriptNative, Controllable: true,
			Notes: "codex emits unified diffs; the host applies them via codex apply",
		},
		{
			Universal: "file.search", NativeNames: []string{"exec_command"},
			Support: invoke.MappingShim, Permission: invoke.ToolRead,
			Transcript: invoke.TranscriptNative, Controllable: true,
			Notes: "no distinct glob/grep tool; uses shell rg/find/grep",
		},
		{
			Universal: "web.search", NativeNames: []string{"web_search"},
			Support: invoke.MappingNative, Permission: invoke.ToolNetwork,
			Transcript: invoke.TranscriptNative, Controllable: true,
			Notes: "enabled via --search; native Responses tool",
		},
		{
			Universal: "web.fetch", NativeNames: nil,
			Support: invoke.MappingShim, Permission: invoke.ToolNetwork,
			Transcript: invoke.TranscriptUnavailable, Controllable: false,
			Notes: "no built-in fetch tool; available via MCP or shell curl",
		},
		{
			Universal: "todo.write", NativeNames: nil,
			Support: invoke.MappingUnsupported, Permission: invoke.ToolTask,
			Transcript: invoke.TranscriptUnavailable, Controllable: false,
		},
		{
			Universal: "task.spawn", NativeNames: nil,
			Support: invoke.MappingUnsupported, Permission: invoke.ToolTask,
			Transcript: invoke.TranscriptUnavailable, Controllable: false,
		},
		{
			Universal: "plan.update", NativeNames: []string{"update_plan"},
			Support: invoke.MappingNative, Permission: invoke.ToolTask,
			Transcript: invoke.TranscriptNative, Controllable: true,
			Notes: "codex's distinctive plan-update tool; emitted in transcripts",
		},
		{
			Universal: "mcp.call", NativeNames: []string{"MCP"},
			Support: invoke.MappingNative, Permission: invoke.ToolExec,
			Transcript: invoke.TranscriptNative, Controllable: true,
			Notes: "MCP servers via codex mcp subcommand",
		},
		{
			Universal: "image.read", NativeNames: nil,
			Support: invoke.MappingNative, Permission: invoke.ToolRead,
			Transcript: invoke.TranscriptPartial, Controllable: false,
			Notes: "via -i/--image flag; attached to initial prompt",
		},
		{
			Universal: "browser.operate", NativeNames: nil,
			Support: invoke.MappingUnsupported, Permission: invoke.ToolBrowser,
			Transcript: invoke.TranscriptUnavailable, Controllable: false,
		},
		{
			Universal: "user.message", NativeNames: nil,
			Support: invoke.MappingUnsupported, Permission: invoke.ToolTask,
			Transcript: invoke.TranscriptUnavailable, Controllable: false,
		},
	}
}
