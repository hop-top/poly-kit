package goose

import "hop.top/kit/go/core/uxp/invoke"

func (Adapter) Mappings() []invoke.OptionMapping {
	return []invoke.OptionMapping{
		{Universal: "ModeRun", Support: invoke.MappingNative, Native: []string{"goose run -t <text>"}},
		{Universal: "ModeInteractive", Support: invoke.MappingNative, Native: []string{"goose session"}},
		{Universal: "ModeResume", Support: invoke.MappingNative,
			Native: []string{"goose run --resume --session-id <id>"}},
		{Universal: "Continue", Support: invoke.MappingNative,
			Native: []string{"goose run --resume"},
			Notes:  "without --session-id resumes most recent"},
		{Universal: "Fork", Support: invoke.MappingNative,
			Native: []string{"goose session --resume --fork"},
			Notes:  "fork uses session subcommand, not run"},
		{Universal: "CWD", Support: invoke.MappingNative, Native: []string{"CommandSpec.Dir"},
			Notes: "no --cd flag"},
		{Universal: "Model", Support: invoke.MappingNative, Native: []string{"--model <name>"}},
		{Universal: "Agent", Support: invoke.MappingShim,
			Native: []string{"--recipe <name> (S-5)"},
			Notes:  "goose recipes are richer than agents; use Config[goose.recipe_params] / [goose.sub_recipe]"},
		{Universal: "OutputText", Support: invoke.MappingNative, Native: []string{"(default)"}},
		{Universal: "OutputJSON", Support: invoke.MappingNative,
			Native: []string{"--output-format json"}},
		{Universal: "OutputStreamJSON", Support: invoke.MappingNative,
			Native: []string{"--output-format stream-json"}},
		{Universal: "SandboxReadOnly", Support: invoke.MappingUnsupported, Native: nil,
			Notes: "configured globally via goose configure"},
		{Universal: "SandboxWorkspaceWrite", Support: invoke.MappingUnsupported, Native: nil},
		{Universal: "SandboxDangerFullAccess", Support: invoke.MappingUnsupported, Native: nil},
		{Universal: "ApprovalAsk", Support: invoke.MappingNative, Native: []string{"(default)"}},
		{Universal: "ApprovalPlan", Support: invoke.MappingUnsupported, Native: nil},
		{Universal: "ApprovalAutoEdit", Support: invoke.MappingUnsupported, Native: nil},
		{Universal: "ApprovalAutoAll", Support: invoke.MappingUnsupported, Native: nil},
		{Universal: "ApprovalNever", Support: invoke.MappingUnsupported, Native: nil},
		{Universal: "AddDirs", Support: invoke.MappingShim,
			Native: []string{"S-3 (prompt-block)"}},
		{Universal: "Files", Support: invoke.MappingShim,
			Native: []string{"S-3 (prompt-block)"}},
		{Universal: "Images", Support: invoke.MappingShim,
			Native: []string{"S-3 (prompt-block)"}},
	}
}
