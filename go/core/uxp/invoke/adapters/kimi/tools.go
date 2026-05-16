package kimi

import "hop.top/kit/go/core/uxp/invoke"

// ToolCapabilities for kimi (Moonshot AI). Tool surface is partly
// agent-defined (built-in agents `default` and `okabe` differ) and
// partly skills-driven via --skills-dir. Conservative entries below.
func (Adapter) ToolCapabilities() []invoke.ToolCapability {
	return []invoke.ToolCapability{
		{
			Universal: "shell.exec", NativeNames: []string{"bash"},
			Support: invoke.MappingNative, Permission: invoke.ToolExec,
			Transcript: invoke.TranscriptNative, Controllable: true,
		},
		{
			Universal: "file.read", NativeNames: []string{"read"},
			Support: invoke.MappingNative, Permission: invoke.ToolRead,
			Transcript: invoke.TranscriptNative, Controllable: true,
		},
		{
			Universal: "file.write", NativeNames: []string{"write"},
			Support: invoke.MappingNative, Permission: invoke.ToolWrite,
			Transcript: invoke.TranscriptNative, Controllable: true,
		},
		{
			Universal: "file.edit", NativeNames: []string{"edit"},
			Support: invoke.MappingNative, Permission: invoke.ToolWrite,
			Transcript: invoke.TranscriptNative, Controllable: true,
		},
		{
			Universal: "file.search", NativeNames: nil,
			Support: invoke.MappingShim, Permission: invoke.ToolRead,
			Transcript: invoke.TranscriptUnavailable, Controllable: false,
			Notes: "via shell rg/find/grep",
		},
		{
			Universal: "web.search", NativeNames: nil,
			Support: invoke.MappingShim, Permission: invoke.ToolNetwork,
			Transcript: invoke.TranscriptUnavailable, Controllable: false,
			Notes: "via skills/MCP",
		},
		{
			Universal: "web.fetch", NativeNames: nil,
			Support: invoke.MappingShim, Permission: invoke.ToolNetwork,
			Transcript: invoke.TranscriptUnavailable, Controllable: false,
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
			Universal: "plan.update", NativeNames: nil,
			Support: invoke.MappingUnsupported, Permission: invoke.ToolTask,
			Transcript: invoke.TranscriptUnavailable, Controllable: false,
			Notes: "use --plan flag for plan-mode runs",
		},
		{
			Universal: "mcp.call", NativeNames: []string{"MCP"},
			Support: invoke.MappingNative, Permission: invoke.ToolExec,
			Transcript: invoke.TranscriptNative, Controllable: true,
			Notes: "via --mcp-config / --mcp-config-file (repeatable)",
		},
		{
			Universal: "image.read", NativeNames: nil,
			Support: invoke.MappingUnsupported, Permission: invoke.ToolRead,
			Transcript: invoke.TranscriptUnavailable, Controllable: false,
		},
		{
			Universal: "browser.operate", NativeNames: nil,
			Support: invoke.MappingUnsupported, Permission: invoke.ToolBrowser,
			Transcript: invoke.TranscriptUnavailable, Controllable: false,
		},
		{
			Universal: "user.message", NativeNames: []string{"AskUserQuestion"},
			Support: invoke.MappingNative, Permission: invoke.ToolTask,
			Transcript: invoke.TranscriptNative, Controllable: false,
			Notes: "auto-dismissed under --print or --afk modes",
		},
	}
}
