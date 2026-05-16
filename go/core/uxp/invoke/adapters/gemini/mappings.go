package gemini

import "hop.top/kit/go/core/uxp/invoke"

func (Adapter) Mappings() []invoke.OptionMapping {
	return []invoke.OptionMapping{
		{Universal: "ModeRun", Support: invoke.MappingNative, Native: []string{"-p"}},
		{Universal: "ModeInteractive", Support: invoke.MappingNative, Native: []string{"(default)"}},
		{Universal: "ModeResume", Support: invoke.MappingNative, Native: []string{"--resume <id>"}},
		{Universal: "Continue", Support: invoke.MappingNative, Native: []string{"--resume latest"}},
		{Universal: "Fork", Support: invoke.MappingUnsupported, Native: nil,
			Notes: "no native fork; resume + fresh session would lose lineage"},
		{Universal: "CWD", Support: invoke.MappingNative, Native: []string{"CommandSpec.Dir"},
			Notes: "no --cd flag; CWD applied via process working directory"},
		{Universal: "Model", Support: invoke.MappingNative, Native: []string{"-m/--model"}},
		{Universal: "Agent", Support: invoke.MappingUnsupported, Native: nil,
			Notes: "no --agent flag; gemini personas configured globally"},
		{Universal: "OutputText", Support: invoke.MappingNative, Native: []string{"(default)"}},
		{Universal: "OutputJSON", Support: invoke.MappingNative, Native: []string{"--output-format json"}},
		{Universal: "OutputStreamJSON", Support: invoke.MappingNative, Native: []string{"--output-format stream-json"}},
		{Universal: "SandboxReadOnly", Support: invoke.MappingShim, Native: []string{"--approval-mode plan"},
			Notes: "no first-class read-only sandbox; plan mode is read-only by convention"},
		{Universal: "SandboxWorkspaceWrite", Support: invoke.MappingShim, Native: []string{"--sandbox"},
			Notes: "boolean --sandbox flag; not a tier"},
		{Universal: "SandboxDangerFullAccess", Support: invoke.MappingDangerous, Native: []string{"--yolo"},
			Notes: "requires Config[\"uxp.allow_dangerous\"]=\"true\""},
		{Universal: "ApprovalAsk", Support: invoke.MappingNative, Native: []string{"(default)"}},
		{Universal: "ApprovalPlan", Support: invoke.MappingNative, Native: []string{"--approval-mode plan"}},
		{Universal: "ApprovalAutoEdit", Support: invoke.MappingNative, Native: []string{"--approval-mode auto_edit"}},
		{Universal: "ApprovalAutoAll", Support: invoke.MappingDangerous, Native: []string{"--approval-mode yolo"},
			Notes: "requires Config[\"uxp.allow_dangerous\"]=\"true\""},
		{Universal: "ApprovalNever", Support: invoke.MappingUnsupported, Native: nil,
			Notes: "no equivalent to claude's dontAsk; refuse rather than shim ambiguously"},
		{Universal: "AddDirs", Support: invoke.MappingNative, Native: []string{"--include-directories <dir> (repeatable)"}},
		{Universal: "Files", Support: invoke.MappingShim,
			Native: []string{"S-1+S-3 (parent-dir reduce → --include-directories + prompt-block)"},
			Notes:  "no per-file flag; gemini accepts directory scope only"},
		{Universal: "Images", Support: invoke.MappingShim,
			Native: []string{"S-3 (prompt-block)"},
			Notes:  "no headless image flag; --prompt is text-only"},
	}
}
