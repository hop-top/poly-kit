package vibe

import "hop.top/kit/go/core/uxp/invoke"

// ToolCapabilities for vibe (Mistral). Tool inventory is partly
// agent-defined (default vs plan vs accept-edits vs auto-approve)
// and partly --enabled-tools-driven.
func (Adapter) ToolCapabilities() []invoke.ToolCapability {
	return []invoke.ToolCapability{
		{
			Universal: "shell.exec", NativeNames: []string{"bash"},
			Support: invoke.MappingNative, Permission: invoke.ToolExec,
			Transcript: invoke.TranscriptNative, Controllable: true,
			Notes: "controllable via --enabled-tools (glob/regex)",
		},
		{
			Universal: "file.read", NativeNames: nil,
			Support: invoke.MappingNative, Permission: invoke.ToolRead,
			Transcript: invoke.TranscriptNative, Controllable: true,
		},
		{
			Universal: "file.write", NativeNames: nil,
			Support: invoke.MappingNative, Permission: invoke.ToolWrite,
			Transcript: invoke.TranscriptNative, Controllable: true,
		},
		{
			Universal: "file.edit", NativeNames: nil,
			Support: invoke.MappingNative, Permission: invoke.ToolWrite,
			Transcript: invoke.TranscriptNative, Controllable: true,
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
			Support: invoke.MappingShim, Permission: invoke.ToolTask,
			Transcript: invoke.TranscriptPartial, Controllable: false,
			Notes: "use --agent plan for plan-mode runs",
		},
		{
			Universal: "mcp.call", NativeNames: nil,
			Support: invoke.MappingUnsupported, Permission: invoke.ToolExec,
			Transcript: invoke.TranscriptUnavailable, Controllable: false,
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
