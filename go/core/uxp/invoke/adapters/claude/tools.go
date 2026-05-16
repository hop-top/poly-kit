package claude

import "hop.top/kit/go/core/uxp/invoke"

// ToolCapabilities implements invoke.InvocationAdapter. The slice
// covers claude's documented built-in tools as of 2.1.118.
func (Adapter) ToolCapabilities() []invoke.ToolCapability {
	return []invoke.ToolCapability{
		{
			Universal: "shell.exec", NativeNames: []string{"Bash"},
			Support: invoke.MappingNative, Permission: invoke.ToolExec,
			Transcript: invoke.TranscriptNative, Controllable: true,
		},
		{
			Universal: "file.read", NativeNames: []string{"Read"},
			Support: invoke.MappingNative, Permission: invoke.ToolRead,
			Transcript: invoke.TranscriptNative, Controllable: true,
		},
		{
			Universal: "file.write", NativeNames: []string{"Write"},
			Support: invoke.MappingNative, Permission: invoke.ToolWrite,
			Transcript: invoke.TranscriptNative, Controllable: true,
		},
		{
			Universal: "file.edit", NativeNames: []string{"Edit", "MultiEdit"},
			Support: invoke.MappingNative, Permission: invoke.ToolWrite,
			Transcript: invoke.TranscriptNative, Controllable: true,
		},
		{
			Universal: "file.search", NativeNames: []string{"Glob", "Grep"},
			Support: invoke.MappingNative, Permission: invoke.ToolRead,
			Transcript: invoke.TranscriptNative, Controllable: true,
		},
		{
			Universal: "web.search", NativeNames: []string{"WebSearch"},
			Support: invoke.MappingNative, Permission: invoke.ToolNetwork,
			Transcript: invoke.TranscriptNative, Controllable: true,
		},
		{
			Universal: "web.fetch", NativeNames: []string{"WebFetch"},
			Support: invoke.MappingNative, Permission: invoke.ToolNetwork,
			Transcript: invoke.TranscriptNative, Controllable: true,
		},
		{
			Universal: "todo.write", NativeNames: []string{"TodoWrite"},
			Support: invoke.MappingNative, Permission: invoke.ToolTask,
			Transcript: invoke.TranscriptNative, Controllable: true,
		},
		{
			Universal: "task.spawn", NativeNames: []string{"Task"},
			Support: invoke.MappingNative, Permission: invoke.ToolTask,
			Transcript: invoke.TranscriptNative, Controllable: true,
		},
		{
			Universal: "plan.update", NativeNames: nil,
			Support: invoke.MappingUnsupported, Permission: invoke.ToolTask,
			Transcript: invoke.TranscriptUnavailable, Controllable: false,
			Notes: "claude uses --permission-mode plan for plan-mode runs; no in-conversation plan-update tool",
		},
		{
			Universal: "mcp.call", NativeNames: []string{"MCP"},
			Support: invoke.MappingNative, Permission: invoke.ToolExec,
			Transcript: invoke.TranscriptNative, Controllable: true,
			Notes: "MCP servers configured via --mcp-config and --strict-mcp-config",
		},
		{
			Universal: "image.read", NativeNames: nil,
			Support: invoke.MappingShim, Permission: invoke.ToolRead,
			Transcript: invoke.TranscriptPartial, Controllable: false,
			Notes: "TUI-only via stdin/clipboard; no headless image attach flag",
		},
		{
			Universal: "browser.operate", NativeNames: []string{"Claude in Chrome"},
			Support: invoke.MappingShim, Permission: invoke.ToolBrowser,
			Transcript: invoke.TranscriptPartial, Controllable: true,
			Notes: "Chrome integration via --chrome / --no-chrome flags; not a transcript-resident tool",
		},
		{
			Universal: "user.message", NativeNames: []string{"SendUserMessage"},
			Support: invoke.MappingNative, Permission: invoke.ToolTask,
			Transcript: invoke.TranscriptNative, Controllable: true,
			Notes: "enabled via --brief flag",
		},
	}
}
