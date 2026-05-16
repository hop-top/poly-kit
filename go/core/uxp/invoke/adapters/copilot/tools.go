package copilot

import "hop.top/kit/go/core/uxp/invoke"

// ToolCapabilities lists copilot's documented built-in agent tools as
// of GitHub Copilot CLI 1.0.15. Copilot has rich tool-policy
// surfaces (--allow-tool, --deny-tool, --allow-url, --deny-url) that
// callers can drive via Config keys; the entries below describe the
// underlying tool capabilities, not the policy DSL.
func (Adapter) ToolCapabilities() []invoke.ToolCapability {
	return []invoke.ToolCapability{
		{
			Universal: "shell.exec", NativeNames: []string{"shell"},
			Support: invoke.MappingNative, Permission: invoke.ToolExec,
			Transcript: invoke.TranscriptNative, Controllable: true,
			Notes: "subject to --allow-tool='shell(...)' and --deny-tool='shell(...)' policy",
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
			Universal: "file.search", NativeNames: []string{"search"},
			Support: invoke.MappingNative, Permission: invoke.ToolRead,
			Transcript: invoke.TranscriptNative, Controllable: true,
		},
		{
			Universal: "web.search", NativeNames: nil,
			Support: invoke.MappingShim, Permission: invoke.ToolNetwork,
			Transcript: invoke.TranscriptUnavailable, Controllable: false,
			Notes: "via plugins/MCP, not built-in",
		},
		{
			Universal: "web.fetch", NativeNames: []string{"fetch"},
			Support: invoke.MappingNative, Permission: invoke.ToolNetwork,
			Transcript: invoke.TranscriptNative, Controllable: true,
			Notes: "subject to --allow-url / --deny-url policy",
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
			Universal: "mcp.call", NativeNames: []string{"MCP", "github-mcp-server"},
			Support: invoke.MappingNative, Permission: invoke.ToolExec,
			Transcript: invoke.TranscriptNative, Controllable: true,
			Notes: "built-in github-mcp-server; --add-github-mcp-tool / --add-github-mcp-toolset / --additional-mcp-config to extend",
		},
		{
			Universal: "image.read", NativeNames: nil,
			Support: invoke.MappingUnsupported, Permission: invoke.ToolRead,
			Transcript: invoke.TranscriptUnavailable, Controllable: false,
			Notes: "no headless image flag",
		},
		{
			Universal: "browser.operate", NativeNames: nil,
			Support: invoke.MappingUnsupported, Permission: invoke.ToolBrowser,
			Transcript: invoke.TranscriptUnavailable, Controllable: false,
		},
		{
			Universal: "user.message", NativeNames: []string{"ask_user"},
			Support: invoke.MappingNative, Permission: invoke.ToolTask,
			Transcript: invoke.TranscriptNative, Controllable: true,
			Notes: "disable via --no-ask-user for fully-autonomous runs",
		},
	}
}
