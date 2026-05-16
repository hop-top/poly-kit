package claude

import "hop.top/kit/go/core/uxp/invoke"

// Mappings implements invoke.InvocationAdapter. The slice covers
// every universal option in the order they appear in spec §15.4.
func (Adapter) Mappings() []invoke.OptionMapping {
	return []invoke.OptionMapping{
		{Universal: "ModeRun", Support: invoke.MappingNative, Native: []string{"-p"}},
		{Universal: "ModeInteractive", Support: invoke.MappingNative, Native: []string{"(default)"}},
		{Universal: "ModeResume", Support: invoke.MappingNative, Native: []string{"--resume <id>"}},
		{Universal: "Continue", Support: invoke.MappingNative, Native: []string{"--continue", "-c"}},
		{Universal: "Fork", Support: invoke.MappingNative, Native: []string{"--fork-session"}},
		{Universal: "CWD", Support: invoke.MappingNative, Native: []string{"CommandSpec.Dir"},
			Notes: "claude has no --cd flag; CWD applied via process working directory"},
		{Universal: "Model", Support: invoke.MappingNative, Native: []string{"--model"}},
		{Universal: "Agent", Support: invoke.MappingNative, Native: []string{"--agent"}},
		{Universal: "OutputText", Support: invoke.MappingNative, Native: []string{"(default)"}},
		{Universal: "OutputJSON", Support: invoke.MappingNative, Native: []string{"--output-format json"},
			Notes: "single result; requires --print"},
		{Universal: "OutputStreamJSON", Support: invoke.MappingNative, Native: []string{"--output-format stream-json"},
			Notes: "realtime streaming; requires --print"},
		{Universal: "SandboxReadOnly", Support: invoke.MappingShim, Native: []string{"--permission-mode plan"},
			Notes: "claude has no first-class sandbox tier; plan mode is read-only by convention"},
		{Universal: "SandboxWorkspaceWrite", Support: invoke.MappingShim, Native: []string{"(default)"},
			Notes: "claude default behavior is workspace-write via tool policy"},
		{Universal: "SandboxDangerFullAccess", Support: invoke.MappingDangerous, Native: []string{"--dangerously-skip-permissions"},
			Notes: "requires Config[\"uxp.allow_dangerous\"]=\"true\""},
		{Universal: "ApprovalPlan", Support: invoke.MappingNative, Native: []string{"--permission-mode plan"}},
		{Universal: "ApprovalAutoEdit", Support: invoke.MappingNative, Native: []string{"--permission-mode acceptEdits"}},
		{Universal: "ApprovalAutoAll", Support: invoke.MappingDangerous, Native: []string{"--permission-mode bypassPermissions"},
			Notes: "requires Config[\"uxp.allow_dangerous\"]=\"true\""},
		{Universal: "ApprovalAsk", Support: invoke.MappingNative, Native: []string{"(default)"}},
		{Universal: "ApprovalNever", Support: invoke.MappingShim, Native: []string{"--permission-mode dontAsk"},
			Notes: "closest peer to ApprovalNever; behavior is no-prompt"},
		{Universal: "AddDirs", Support: invoke.MappingNative, Native: []string{"--add-dir <directories...>"},
			Notes: "variadic; multiple paths after one flag"},
		{Universal: "Files", Support: invoke.MappingShim,
			Native: []string{"S-1+S-3 (parent-dir reduce → --add-dir + prompt-block)"},
			Notes:  "claude --file is file_id:path for downloaded resources, not local files"},
		{Universal: "Images", Support: invoke.MappingShim,
			Native: []string{"S-3 (prompt-block)"},
			Notes:  "no headless image flag; TUI-only via stdin/clipboard"},
	}
}
