package goose

import "hop.top/kit/go/core/uxp/invoke"

// ToolCapabilities for goose 1.33.1. goose's tool model is
// extension-driven: built-in extensions plus stdio/http extensions
// added per-run via --with-extension / --with-builtin /
// --with-streamable-http-extension. The entries below describe the
// canonical bundled set; per-run extension lists go through Config.
func (Adapter) ToolCapabilities() []invoke.ToolCapability {
	return []invoke.ToolCapability{
		{
			Universal: "shell.exec", NativeNames: []string{"developer__shell"},
			Support: invoke.MappingNative, Permission: invoke.ToolExec,
			Transcript: invoke.TranscriptNative, Controllable: true,
			Notes: "via developer extension; --container <id> for sandboxed execution",
		},
		{
			Universal: "file.read", NativeNames: []string{"developer__text_editor"},
			Support: invoke.MappingNative, Permission: invoke.ToolRead,
			Transcript: invoke.TranscriptNative, Controllable: true,
		},
		{
			Universal: "file.write", NativeNames: []string{"developer__text_editor"},
			Support: invoke.MappingNative, Permission: invoke.ToolWrite,
			Transcript: invoke.TranscriptNative, Controllable: true,
		},
		{
			Universal: "file.edit", NativeNames: []string{"developer__text_editor"},
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
			Notes: "via extensions",
		},
		{
			Universal: "web.fetch", NativeNames: nil,
			Support: invoke.MappingShim, Permission: invoke.ToolNetwork,
			Transcript: invoke.TranscriptUnavailable, Controllable: false,
			Notes: "via extensions",
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
			Notes: "sub-recipes are pre-orchestrated, not in-conversation spawn",
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
			Notes: "extensive MCP support; goose mcp subcommand and --with-extension / --with-streamable-http-extension flags",
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
