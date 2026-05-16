package qwen

import "hop.top/kit/go/core/uxp/invoke"

func (Adapter) Mappings() []invoke.OptionMapping {
	return []invoke.OptionMapping{
		{Universal: "ModeRun", Support: invoke.MappingNative,
			Native: []string{"qwen [query..]"},
			Notes:  "positional; -p/--prompt is documented as deprecated"},
		{Universal: "ModeInteractive", Support: invoke.MappingNative,
			Native: []string{"qwen [query..]", "-i/--prompt-interactive"}},
		{Universal: "ModeResume", Support: invoke.MappingNative, Native: []string{"-r/--resume <id>"}},
		{Universal: "Continue", Support: invoke.MappingNative, Native: []string{"-c/--continue"}},
		{Universal: "Fork", Support: invoke.MappingUnsupported, Native: nil},
		{Universal: "CWD", Support: invoke.MappingNative, Native: []string{"CommandSpec.Dir"}},
		{Universal: "Model", Support: invoke.MappingNative, Native: []string{"-m/--model"}},
		{Universal: "Agent", Support: invoke.MappingUnsupported, Native: nil},
		{Universal: "OutputText", Support: invoke.MappingNative, Native: []string{"(default)"}},
		{Universal: "OutputJSON", Support: invoke.MappingNative, Native: []string{"-o/--output-format json"}},
		{Universal: "OutputStreamJSON", Support: invoke.MappingNative,
			Native: []string{"-o/--output-format stream-json"}},
		{Universal: "SandboxReadOnly", Support: invoke.MappingShim,
			Native: []string{"--approval-mode plan"}},
		{Universal: "SandboxWorkspaceWrite", Support: invoke.MappingShim,
			Native: []string{"-s/--sandbox (boolean)"}},
		{Universal: "SandboxDangerFullAccess", Support: invoke.MappingDangerous,
			Native: []string{"-y/--yolo"}},
		{Universal: "ApprovalAsk", Support: invoke.MappingNative, Native: []string{"(default)"}},
		{Universal: "ApprovalPlan", Support: invoke.MappingNative,
			Native: []string{"--approval-mode plan"}},
		{Universal: "ApprovalAutoEdit", Support: invoke.MappingNative,
			Native: []string{"--approval-mode auto-edit"}},
		{Universal: "ApprovalAutoAll", Support: invoke.MappingDangerous,
			Native: []string{"--approval-mode yolo", "-y/--yolo"}},
		{Universal: "ApprovalNever", Support: invoke.MappingUnsupported, Native: nil},
		{Universal: "AddDirs", Support: invoke.MappingNative,
			Native: []string{"--include-directories <dir> (alias --add-dir; repeatable or comma-list)"}},
		{Universal: "Files", Support: invoke.MappingShim,
			Native: []string{"S-1+S-3 (parent-dir reduce → --include-directories + prompt-block)"}},
		{Universal: "Images", Support: invoke.MappingShim,
			Native: []string{"S-3 (prompt-block)"}},
	}
}
