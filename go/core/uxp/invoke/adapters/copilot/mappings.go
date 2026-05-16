package copilot

import "hop.top/kit/go/core/uxp/invoke"

func (Adapter) Mappings() []invoke.OptionMapping {
	return []invoke.OptionMapping{
		{Universal: "ModeRun", Support: invoke.MappingNative, Native: []string{"-p/--prompt <text>"}},
		{Universal: "ModeInteractive", Support: invoke.MappingNative, Native: []string{"(default)", "-i/--interactive <prompt>"}},
		{Universal: "ModeResume", Support: invoke.MappingNative, Native: []string{"--resume=<id>"}},
		{Universal: "Continue", Support: invoke.MappingNative, Native: []string{"--continue"}},
		{Universal: "Fork", Support: invoke.MappingUnsupported, Native: nil},
		{Universal: "CWD", Support: invoke.MappingNative, Native: []string{"CommandSpec.Dir"},
			Notes: "no --cd / --dir flag"},
		{Universal: "Model", Support: invoke.MappingNative, Native: []string{"--model"}},
		{Universal: "Agent", Support: invoke.MappingNative, Native: []string{"--agent"}},
		{Universal: "OutputText", Support: invoke.MappingNative, Native: []string{"(default)"}},
		{Universal: "OutputJSON", Support: invoke.MappingShim, Native: []string{"--output-format json"},
			Notes: "JSONL one-object-per-line; caller must reduce"},
		{Universal: "OutputStreamJSON", Support: invoke.MappingNative, Native: []string{"--output-format json"}},
		{Universal: "SandboxReadOnly", Support: invoke.MappingUnsupported, Native: nil,
			Notes: "no per-tier sandbox; use tool/url policy via Config"},
		{Universal: "SandboxWorkspaceWrite", Support: invoke.MappingUnsupported, Native: nil},
		{Universal: "SandboxDangerFullAccess", Support: invoke.MappingDangerous,
			Native: []string{"--yolo"},
			Notes:  "requires Config[\"uxp.allow_dangerous\"]=\"true\""},
		{Universal: "ApprovalAsk", Support: invoke.MappingNative, Native: []string{"(default)"}},
		{Universal: "ApprovalPlan", Support: invoke.MappingUnsupported, Native: nil},
		{Universal: "ApprovalAutoEdit", Support: invoke.MappingUnsupported, Native: nil,
			Notes: "refused: no native auto-edit; would degrade to dangerous bypass"},
		{Universal: "ApprovalAutoAll", Support: invoke.MappingDangerous,
			Native: []string{"--yolo", "--allow-all", "--allow-all-tools"},
			Notes:  "requires Config[\"uxp.allow_dangerous\"]=\"true\""},
		{Universal: "ApprovalNever", Support: invoke.MappingUnsupported, Native: nil},
		{Universal: "AddDirs", Support: invoke.MappingNative, Native: []string{"--add-dir <directory> (repeatable)"}},
		{Universal: "Files", Support: invoke.MappingShim,
			Native: []string{"S-1+S-3 (parent-dir reduce → --add-dir + prompt-block)"}},
		{Universal: "Images", Support: invoke.MappingShim,
			Native: []string{"S-3 (prompt-block)"},
			Notes:  "no headless image flag"},
	}
}
