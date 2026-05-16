package gemini

import "hop.top/kit/go/core/uxp/invoke"

// ToolCapabilities lists gemini's documented built-in agent tools as
// of 0.40.1. Source: gemini help output + gemini skills/extensions
// surface. Conservative on transcript fidelity — gemini's transcript
// emits tool calls as JSON events under --output-format=stream-json.
func (Adapter) ToolCapabilities() []invoke.ToolCapability {
	return []invoke.ToolCapability{
		{
			Universal: "shell.exec", NativeNames: []string{"shell"},
			Support: invoke.MappingNative, Permission: invoke.ToolExec,
			Transcript: invoke.TranscriptNative, Controllable: true,
			Notes: "subject to Policy Engine via --policy / --admin-policy",
		},
		{
			Universal: "file.read", NativeNames: []string{"read_file"},
			Support: invoke.MappingNative, Permission: invoke.ToolRead,
			Transcript: invoke.TranscriptNative, Controllable: true,
		},
		{
			Universal: "file.write", NativeNames: []string{"write_file"},
			Support: invoke.MappingNative, Permission: invoke.ToolWrite,
			Transcript: invoke.TranscriptNative, Controllable: true,
		},
		{
			Universal: "file.edit", NativeNames: []string{"edit_file"},
			Support: invoke.MappingNative, Permission: invoke.ToolWrite,
			Transcript: invoke.TranscriptNative, Controllable: true,
		},
		{
			Universal: "file.search", NativeNames: []string{"search_file_content", "glob"},
			Support: invoke.MappingNative, Permission: invoke.ToolRead,
			Transcript: invoke.TranscriptNative, Controllable: true,
		},
		{
			Universal: "web.search", NativeNames: []string{"google_web_search"},
			Support: invoke.MappingNative, Permission: invoke.ToolNetwork,
			Transcript: invoke.TranscriptNative, Controllable: true,
		},
		{
			Universal: "web.fetch", NativeNames: []string{"web_fetch"},
			Support: invoke.MappingNative, Permission: invoke.ToolNetwork,
			Transcript: invoke.TranscriptNative, Controllable: true,
		},
		{
			Universal: "todo.write", NativeNames: nil,
			Support: invoke.MappingShim, Permission: invoke.ToolTask,
			Transcript: invoke.TranscriptUnavailable, Controllable: false,
			Notes: "no built-in TodoWrite analog; available via skills",
		},
		{
			Universal: "task.spawn", NativeNames: nil,
			Support: invoke.MappingShim, Permission: invoke.ToolTask,
			Transcript: invoke.TranscriptUnavailable, Controllable: false,
			Notes: "subagents available via skills; not a built-in tool",
		},
		{
			Universal: "plan.update", NativeNames: nil,
			Support: invoke.MappingUnsupported, Permission: invoke.ToolTask,
			Transcript: invoke.TranscriptUnavailable, Controllable: false,
			Notes: "no plan-mode update tool; use --approval-mode plan for plan-mode runs",
		},
		{
			Universal: "mcp.call", NativeNames: []string{"MCP"},
			Support: invoke.MappingNative, Permission: invoke.ToolExec,
			Transcript: invoke.TranscriptNative, Controllable: true,
			Notes: "MCP servers via gemini mcp subcommand and --allowed-mcp-server-names",
		},
		{
			Universal: "image.read", NativeNames: nil,
			Support: invoke.MappingNative, Permission: invoke.ToolRead,
			Transcript: invoke.TranscriptPartial, Controllable: false,
			Notes: "Gemini models accept image input via prompt media; no headless flag",
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
