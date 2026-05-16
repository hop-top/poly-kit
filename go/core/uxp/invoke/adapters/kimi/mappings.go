package kimi

import "hop.top/kit/go/core/uxp/invoke"

func (Adapter) Mappings() []invoke.OptionMapping {
	return []invoke.OptionMapping{
		{Universal: "ModeRun", Support: invoke.MappingNative, Native: []string{"--print"}},
		{Universal: "ModeInteractive", Support: invoke.MappingNative, Native: []string{"(default)"}},
		{Universal: "ModeResume", Support: invoke.MappingNative, Native: []string{"-S/--session <id>"}},
		{Universal: "Continue", Support: invoke.MappingNative, Native: []string{"-C/--continue"}},
		{Universal: "Fork", Support: invoke.MappingUnsupported, Native: nil},
		{Universal: "CWD", Support: invoke.MappingNative, Native: []string{"-w/--work-dir <dir>"}},
		{Universal: "Model", Support: invoke.MappingNative, Native: []string{"-m/--model"}},
		{Universal: "Agent", Support: invoke.MappingNative,
			Native: []string{"--agent default|okabe", "--agent-file <FILE>"}},
		{Universal: "OutputText", Support: invoke.MappingNative, Native: []string{"(default)"}},
		{Universal: "OutputJSON", Support: invoke.MappingShim,
			Native: []string{"--print --output-format text --final-message-only (alias --quiet)"},
			Notes:  "kimi has no json choice; final-message-text shim"},
		{Universal: "OutputStreamJSON", Support: invoke.MappingNative,
			Native: []string{"--output-format stream-json"}},
		{Universal: "SandboxReadOnly", Support: invoke.MappingUnsupported, Native: nil},
		{Universal: "SandboxWorkspaceWrite", Support: invoke.MappingUnsupported, Native: nil},
		{Universal: "SandboxDangerFullAccess", Support: invoke.MappingUnsupported, Native: nil,
			Notes: "kimi has no per-invocation sandbox flag"},
		{Universal: "ApprovalAsk", Support: invoke.MappingNative, Native: []string{"(default)"}},
		{Universal: "ApprovalPlan", Support: invoke.MappingNative, Native: []string{"--plan"}},
		{Universal: "ApprovalAutoEdit", Support: invoke.MappingUnsupported, Native: nil,
			Notes: "--yolo and --afk are auto-all only; refused per anti-shim"},
		{Universal: "ApprovalAutoAll", Support: invoke.MappingDangerous,
			Native: []string{"--yolo", "--afk"}},
		{Universal: "ApprovalNever", Support: invoke.MappingUnsupported, Native: nil},
		{Universal: "AddDirs", Support: invoke.MappingNative,
			Native: []string{"--add-dir <dir> (repeatable)"}},
		{Universal: "Files", Support: invoke.MappingShim,
			Native: []string{"S-1+S-3 (parent-dir reduce → --add-dir + prompt-block)"}},
		{Universal: "Images", Support: invoke.MappingShim,
			Native: []string{"S-3 (prompt-block)"}},
	}
}
