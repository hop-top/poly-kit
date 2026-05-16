package vibe

import "hop.top/kit/go/core/uxp/invoke"

func (Adapter) Mappings() []invoke.OptionMapping {
	return []invoke.OptionMapping{
		{Universal: "ModeRun", Support: invoke.MappingNative, Native: []string{"-p/--prompt"}},
		{Universal: "ModeInteractive", Support: invoke.MappingNative, Native: []string{"(default)"}},
		{Universal: "ModeResume", Support: invoke.MappingNative, Native: []string{"--resume <id>"}},
		{Universal: "Continue", Support: invoke.MappingNative, Native: []string{"-c/--continue"}},
		{Universal: "Fork", Support: invoke.MappingUnsupported, Native: nil},
		{Universal: "CWD", Support: invoke.MappingNative, Native: []string{"--workdir <dir>"}},
		{Universal: "Model", Support: invoke.MappingUnsupported, Native: nil,
			Notes: "vibe selects model via config; use --agent"},
		{Universal: "Agent", Support: invoke.MappingNative,
			Native: []string{"--agent default|plan|accept-edits|auto-approve|<custom>"}},
		{Universal: "OutputText", Support: invoke.MappingNative, Native: []string{"--output text", "(default)"}},
		{Universal: "OutputJSON", Support: invoke.MappingNative, Native: []string{"--output json"}},
		{Universal: "OutputStreamJSON", Support: invoke.MappingNative,
			Native: []string{"--output streaming"}},
		{Universal: "SandboxReadOnly", Support: invoke.MappingUnsupported, Native: nil},
		{Universal: "SandboxWorkspaceWrite", Support: invoke.MappingUnsupported, Native: nil},
		{Universal: "SandboxDangerFullAccess", Support: invoke.MappingUnsupported, Native: nil,
			Notes: "no sandbox flag; --agent auto-approve handles approval, not isolation"},
		{Universal: "ApprovalAsk", Support: invoke.MappingNative, Native: []string{"(default)"}},
		{Universal: "ApprovalPlan", Support: invoke.MappingShim,
			Native: []string{"--agent plan (S-4)"},
			Notes:  "consumes --agent slot; mutually exclusive with caller-set Agent"},
		{Universal: "ApprovalAutoEdit", Support: invoke.MappingShim,
			Native: []string{"--agent accept-edits (S-4)"},
			Notes:  "consumes --agent slot"},
		{Universal: "ApprovalAutoAll", Support: invoke.MappingDangerous,
			Native: []string{"--agent auto-approve (S-4)"},
			Notes:  "requires Config[\"uxp.allow_dangerous\"]=\"true\""},
		{Universal: "ApprovalNever", Support: invoke.MappingUnsupported, Native: nil},
		{Universal: "AddDirs", Support: invoke.MappingShim,
			Native: []string{"S-3 (prompt-block)"}},
		{Universal: "Files", Support: invoke.MappingShim,
			Native: []string{"S-3 (prompt-block)"}},
		{Universal: "Images", Support: invoke.MappingShim,
			Native: []string{"S-3 (prompt-block)"}},
	}
}
