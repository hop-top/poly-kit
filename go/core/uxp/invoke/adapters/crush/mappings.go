package crush

import "hop.top/kit/go/core/uxp/invoke"

func (Adapter) Mappings() []invoke.OptionMapping {
	return []invoke.OptionMapping{
		{Universal: "ModeRun", Support: invoke.MappingNative, Native: []string{"crush run [prompt...]"}},
		{Universal: "ModeInteractive", Support: invoke.MappingNative, Native: []string{"crush"}},
		{Universal: "ModeResume", Support: invoke.MappingNative,
			Native: []string{"crush run --session <id>"}},
		{Universal: "Continue", Support: invoke.MappingNative, Native: []string{"crush run --continue"}},
		{Universal: "Fork", Support: invoke.MappingUnsupported, Native: nil},
		{Universal: "CWD", Support: invoke.MappingNative, Native: []string{"--cwd / -c"}},
		{Universal: "Model", Support: invoke.MappingNative, Native: []string{"--model"}},
		{Universal: "Agent", Support: invoke.MappingUnsupported, Native: nil},
		{Universal: "OutputText", Support: invoke.MappingNative, Native: []string{"(default)"}},
		{Universal: "OutputJSON", Support: invoke.MappingUnsupported, Native: nil,
			Notes: "no --format flag exists"},
		{Universal: "OutputStreamJSON", Support: invoke.MappingUnsupported, Native: nil},
		{Universal: "SandboxReadOnly", Support: invoke.MappingUnsupported, Native: nil},
		{Universal: "SandboxWorkspaceWrite", Support: invoke.MappingUnsupported, Native: nil},
		{Universal: "SandboxDangerFullAccess", Support: invoke.MappingDangerous,
			Native: []string{"--yolo / -y"}},
		{Universal: "ApprovalAsk", Support: invoke.MappingNative, Native: []string{"(default)"}},
		{Universal: "ApprovalPlan", Support: invoke.MappingUnsupported, Native: nil},
		{Universal: "ApprovalAutoEdit", Support: invoke.MappingUnsupported, Native: nil,
			Notes: "--yolo is auto-all only; refused per anti-shim"},
		{Universal: "ApprovalAutoAll", Support: invoke.MappingDangerous,
			Native: []string{"--yolo / -y"}},
		{Universal: "ApprovalNever", Support: invoke.MappingUnsupported, Native: nil},
		{Universal: "AddDirs", Support: invoke.MappingShim, Native: []string{"S-3 (prompt-block)"}},
		{Universal: "Files", Support: invoke.MappingShim, Native: []string{"S-3 (prompt-block)"}},
		{Universal: "Images", Support: invoke.MappingShim, Native: []string{"S-3 (prompt-block)"}},
	}
}
