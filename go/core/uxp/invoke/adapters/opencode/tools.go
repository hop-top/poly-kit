package opencode

import "hop.top/kit/go/core/uxp/invoke"

// ToolCapabilities lists opencode's built-in agent tools as of
// opencode 1.14.30. opencode publishes a tool catalog through its
// agent + plugin systems; this adapter records the subset documented
// at https://opencode.ai/docs (best-effort).
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
			Universal: "file.search", NativeNames: []string{"grep", "glob", "list"},
			Support: invoke.MappingNative, Permission: invoke.ToolRead,
			Transcript: invoke.TranscriptNative, Controllable: true,
		},
		{
			Universal: "web.search", NativeNames: nil,
			Support: invoke.MappingShim, Permission: invoke.ToolNetwork,
			Transcript: invoke.TranscriptUnavailable, Controllable: false,
			Notes: "available via plugins/MCP, not built-in",
		},
		{
			Universal: "web.fetch", NativeNames: []string{"webfetch"},
			Support: invoke.MappingNative, Permission: invoke.ToolNetwork,
			Transcript: invoke.TranscriptNative, Controllable: true,
		},
		{
			Universal: "todo.write", NativeNames: []string{"todowrite"},
			Support: invoke.MappingNative, Permission: invoke.ToolTask,
			Transcript: invoke.TranscriptNative, Controllable: true,
		},
		{
			Universal: "task.spawn", NativeNames: []string{"task"},
			Support: invoke.MappingNative, Permission: invoke.ToolTask,
			Transcript: invoke.TranscriptNative, Controllable: true,
			Notes: "subagent invocation via opencode agents subcommand",
		},
		{
			Universal: "plan.update", NativeNames: nil,
			Support: invoke.MappingUnsupported, Permission: invoke.ToolTask,
			Transcript: invoke.TranscriptUnavailable, Controllable: false,
		},
		{
			Universal: "mcp.call", NativeNames: []string{"MCP"},
			Support: invoke.MappingNative, Permission: invoke.ToolExec,
			Transcript: invoke.TranscriptNative, Controllable: true,
			Notes: "MCP servers via opencode mcp subcommand",
		},
		{
			Universal: "image.read", NativeNames: nil,
			Support: invoke.MappingShim, Permission: invoke.ToolRead,
			Transcript: invoke.TranscriptPartial, Controllable: false,
			Notes: "via -f/--file (no distinct image flag)",
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
