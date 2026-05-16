package cursoragent

import "hop.top/kit/go/core/uxp/invoke"

// ToolCapabilities lists cursor-agent's documented built-in agent
// tools. Cursor-agent's --print mode states "has access to all tools,
// including write and bash". The exact native tool name set is not
// publicly enumerated; entries below are conservative.
func (Adapter) ToolCapabilities() []invoke.ToolCapability {
	return []invoke.ToolCapability{
		{
			Universal: "shell.exec", NativeNames: []string{"bash"},
			Support: invoke.MappingNative, Permission: invoke.ToolExec,
			Transcript: invoke.TranscriptNative, Controllable: true,
			Notes: "subject to sandbox subcommand allowlist",
		},
		{
			Universal: "file.read", NativeNames: []string{"read"},
			Support: invoke.MappingNative, Permission: invoke.ToolRead,
			Transcript: invoke.TranscriptNative, Controllable: false,
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
			Notes: "uses shell rg/find/grep",
		},
		{
			Universal: "web.search", NativeNames: nil,
			Support: invoke.MappingUnsupported, Permission: invoke.ToolNetwork,
			Transcript: invoke.TranscriptUnavailable, Controllable: false,
		},
		{
			Universal: "web.fetch", NativeNames: nil,
			Support: invoke.MappingUnsupported, Permission: invoke.ToolNetwork,
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
		},
		{
			Universal: "mcp.call", NativeNames: []string{"MCP"},
			Support: invoke.MappingNative, Permission: invoke.ToolExec,
			Transcript: invoke.TranscriptNative, Controllable: true,
			Notes: "managed via cursor-agent mcp subcommand",
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
			Universal: "user.message", NativeNames: nil,
			Support: invoke.MappingUnsupported, Permission: invoke.ToolTask,
			Transcript: invoke.TranscriptUnavailable, Controllable: false,
		},
	}
}
