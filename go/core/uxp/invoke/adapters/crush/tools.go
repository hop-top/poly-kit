package crush

import "hop.top/kit/go/core/uxp/invoke"

// ToolCapabilities for crush v0.65.2 (Charmbracelet). Tool inventory
// is conservative — crush's --help does not enumerate built-in tools
// directly; entries follow the standard agent toolset assumption.
func (Adapter) ToolCapabilities() []invoke.ToolCapability {
	return []invoke.ToolCapability{
		{
			Universal: "shell.exec", NativeNames: []string{"bash"},
			Support: invoke.MappingNative, Permission: invoke.ToolExec,
			Transcript: invoke.TranscriptNative, Controllable: false,
		},
		{
			Universal: "file.read", NativeNames: nil,
			Support: invoke.MappingNative, Permission: invoke.ToolRead,
			Transcript: invoke.TranscriptNative, Controllable: false,
		},
		{
			Universal: "file.write", NativeNames: nil,
			Support: invoke.MappingNative, Permission: invoke.ToolWrite,
			Transcript: invoke.TranscriptNative, Controllable: false,
		},
		{
			Universal: "file.edit", NativeNames: nil,
			Support: invoke.MappingNative, Permission: invoke.ToolWrite,
			Transcript: invoke.TranscriptNative, Controllable: false,
		},
		{
			Universal: "file.search", NativeNames: nil,
			Support: invoke.MappingShim, Permission: invoke.ToolRead,
			Transcript: invoke.TranscriptUnavailable, Controllable: false,
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
			Universal: "mcp.call", NativeNames: nil,
			Support: invoke.MappingShim, Permission: invoke.ToolExec,
			Transcript: invoke.TranscriptUnavailable, Controllable: false,
			Notes: "MCP support via crush config",
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
