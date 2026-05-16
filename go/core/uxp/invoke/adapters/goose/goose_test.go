package goose

import (
	"slices"
	"testing"

	"hop.top/kit/go/core/uxp"
	"hop.top/kit/go/core/uxp/invoke"
)

func TestAdapterCLI(t *testing.T) {
	t.Parallel()
	if got := New().CLI(); got != uxp.CLIGoose {
		t.Errorf("CLI() = %q, want %q", got, uxp.CLIGoose)
	}
}

func TestBuildModeRunSubcommand(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIGoose, Mode: invoke.ModeRun, Prompt: "hi",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if spec.Args[0] != "run" {
		t.Errorf("expected run subcommand; got %v", spec.Args)
	}
	if !containsSubslice(spec.Args, []string{"-t", "hi"}) {
		t.Errorf("missing -t hi: %v", spec.Args)
	}
}

func TestBuildModeInteractiveUsesSession(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIGoose, Mode: invoke.ModeInteractive,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if spec.Args[0] != "session" {
		t.Errorf("expected session subcommand; got %v", spec.Args)
	}
}

func TestBuildResumeWithIDUsesRunResume(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIGoose, Mode: invoke.ModeResume, SessionID: "abc",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"run", "--resume", "--session-id", "abc"}) {
		t.Errorf("expected run --resume --session-id abc; got %v", spec.Args)
	}
}

func TestBuildContinue(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIGoose, Mode: invoke.ModeResume, Continue: true,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"run", "--resume"}) {
		t.Errorf("expected run --resume for Continue; got %v", spec.Args)
	}
	if slices.Contains(spec.Args, "--session-id") {
		t.Errorf("Continue should not include --session-id: %v", spec.Args)
	}
}

func TestBuildForkUsesSessionResumeFork(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIGoose, Mode: invoke.ModeResume, SessionID: "abc", Fork: true,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"session", "--resume", "--fork"}) {
		t.Errorf("expected session --resume --fork; got %v", spec.Args)
	}
}

func TestBuildForkRequiresResume(t *testing.T) {
	t.Parallel()
	_, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIGoose, Mode: invoke.ModeRun, Fork: true,
	})
	if err == nil {
		t.Error("expected error: Fork requires Resume")
	}
}

func TestBuildAgentShimsToRecipe(t *testing.T) {
	t.Parallel()
	spec, ds, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIGoose, Mode: invoke.ModeRun, Agent: "code-review",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"--recipe", "code-review"}) {
		t.Errorf("missing --recipe: %v", spec.Args)
	}
	hasS5Diag := false
	for _, d := range ds {
		if d.Option == "Agent" && d.Level == "warning" {
			hasS5Diag = true
		}
	}
	if !hasS5Diag {
		t.Errorf("expected S-5 Agent warning: %+v", ds)
	}
}

func TestBuildSandboxAndApprovalRefused(t *testing.T) {
	t.Parallel()
	_, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIGoose, Mode: invoke.ModeRun, Sandbox: invoke.SandboxReadOnly,
	})
	if err == nil {
		t.Error("expected error: goose has no per-invocation sandbox")
	}
	_, _, err = New().Build(invoke.Invocation{
		CLI: uxp.CLIGoose, Mode: invoke.ModeRun, Approval: invoke.ApprovalAutoEdit,
	})
	if err == nil {
		t.Error("expected error: goose has no per-invocation approval")
	}
}

func TestBuildOutputFormats(t *testing.T) {
	t.Parallel()
	cases := []struct {
		out  invoke.OutputFormat
		flag string
	}{
		{invoke.OutputJSON, "json"},
		{invoke.OutputStreamJSON, "stream-json"},
	}
	for _, tc := range cases {
		t.Run(string(tc.out), func(t *testing.T) {
			spec, _, err := New().Build(invoke.Invocation{
				CLI: uxp.CLIGoose, Mode: invoke.ModeRun, Output: tc.out,
			})
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if !containsSubslice(spec.Args, []string{"--output-format", tc.flag}) {
				t.Errorf("missing --output-format %s: %v", tc.flag, spec.Args)
			}
		})
	}
}

func TestBuildConfigKeys(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIGoose, Mode: invoke.ModeRun,
		Config: map[string]string{
			"goose.provider":       "anthropic",
			"goose.with_builtin":   "developer,memory",
			"goose.with_extension": `ENV1=v command1,COMMAND2 args`,
			"goose.no_session":     "true",
			"goose.recipe_params":  "key1=v1,key2=v2",
		},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"--provider", "anthropic"}) {
		t.Errorf("missing --provider anthropic: %v", spec.Args)
	}
	if !containsSubslice(spec.Args, []string{"--with-builtin", "developer,memory"}) {
		t.Errorf("missing --with-builtin: %v", spec.Args)
	}
	extCount := 0
	for i, a := range spec.Args {
		if a == "--with-extension" && i+1 < len(spec.Args) {
			extCount++
		}
	}
	if extCount != 2 {
		t.Errorf("expected 2 --with-extension; got %d in %v", extCount, spec.Args)
	}
	if !slices.Contains(spec.Args, "--no-session") {
		t.Errorf("missing --no-session: %v", spec.Args)
	}
	paramCount := 0
	for i, a := range spec.Args {
		if a == "--params" && i+1 < len(spec.Args) {
			paramCount++
		}
	}
	if paramCount != 2 {
		t.Errorf("expected 2 --params; got %d in %v", paramCount, spec.Args)
	}
}

func TestMappingsCovered(t *testing.T) {
	t.Parallel()
	want := []string{
		"ModeRun", "ModeInteractive", "ModeResume", "Continue", "Fork",
		"CWD", "Model", "Agent",
		"OutputText", "OutputJSON", "OutputStreamJSON",
		"SandboxReadOnly", "SandboxWorkspaceWrite", "SandboxDangerFullAccess",
		"ApprovalAsk", "ApprovalPlan", "ApprovalAutoEdit", "ApprovalAutoAll", "ApprovalNever",
		"AddDirs", "Files", "Images",
	}
	have := map[string]bool{}
	for _, m := range New().Mappings() {
		have[m.Universal] = true
	}
	for _, w := range want {
		if !have[w] {
			t.Errorf("Mappings missing %q", w)
		}
	}
}

func containsSubslice(haystack, needle []string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
