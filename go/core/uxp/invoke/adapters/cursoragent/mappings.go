package cursoragent

import "hop.top/kit/go/core/uxp/invoke"

func (Adapter) Mappings() []invoke.OptionMapping {
	return []invoke.OptionMapping{
		{Universal: "ModeRun", Support: invoke.MappingNative, Native: []string{"-p/--print"}},
		{Universal: "ModeInteractive", Support: invoke.MappingNative, Native: []string{"(default)"}},
		{Universal: "ModeResume", Support: invoke.MappingNative, Native: []string{"--resume <id>"}},
		{Universal: "Continue", Support: invoke.MappingNative, Native: []string{"resume (subcommand)"}},
		{Universal: "Fork", Support: invoke.MappingUnsupported, Native: nil},
		{Universal: "CWD", Support: invoke.MappingNative, Native: []string{"CommandSpec.Dir"}},
		{Universal: "Model", Support: invoke.MappingNative, Native: []string{"--model"}},
		{Universal: "Agent", Support: invoke.MappingUnsupported, Native: nil},
		{Universal: "OutputText", Support: invoke.MappingNative, Native: []string{"(default)"}},
		{Universal: "OutputJSON", Support: invoke.MappingNative, Native: []string{"--output-format json"},
			Notes: "requires --print"},
		{Universal: "OutputStreamJSON", Support: invoke.MappingNative, Native: []string{"--output-format stream-json"},
			Notes: "requires --print; --stream-partial-output for deltas"},
		{Universal: "SandboxReadOnly", Support: invoke.MappingUnsupported, Native: nil,
			Notes: "sandbox is configured via cursor-agent sandbox subcommand, not per-invocation"},
		{Universal: "SandboxWorkspaceWrite", Support: invoke.MappingUnsupported, Native: nil},
		{Universal: "SandboxDangerFullAccess", Support: invoke.MappingDangerous,
			Native: []string{"-f/--force"},
			Notes:  "requires Config[\"uxp.allow_dangerous\"]=\"true\""},
		{Universal: "ApprovalAsk", Support: invoke.MappingNative, Native: []string{"(default)"}},
		{Universal: "ApprovalPlan", Support: invoke.MappingUnsupported, Native: nil},
		{Universal: "ApprovalAutoEdit", Support: invoke.MappingUnsupported, Native: nil,
			Notes: "-f/--force is auto-all only; refused per anti-shim"},
		{Universal: "ApprovalAutoAll", Support: invoke.MappingDangerous,
			Native: []string{"-f/--force"}},
		{Universal: "ApprovalNever", Support: invoke.MappingUnsupported, Native: nil},
		{Universal: "AddDirs", Support: invoke.MappingShim,
			Native: []string{"S-3 (prompt-block listing)"},
			Notes:  "no --add-dir flag"},
		{Universal: "Files", Support: invoke.MappingShim,
			Native: []string{"S-3 (prompt-block)"},
			Notes:  "no per-file flag"},
		{Universal: "Images", Support: invoke.MappingShim,
			Native: []string{"S-3 (prompt-block)"}},
	}
}
