package codex

import (
	"slices"
	"strings"
	"testing"

	"hop.top/kit/go/core/uxp"
	"hop.top/kit/go/core/uxp/invoke"
)

func TestAdapterCLI(t *testing.T) {
	t.Parallel()
	if got := New().CLI(); got != uxp.CLICodex {
		t.Errorf("CLI() = %q, want %q", got, uxp.CLICodex)
	}
}

func TestBuildModeRunSubcommand(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICodex, Mode: invoke.ModeRun, Prompt: "hi",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if spec.Args[0] != "exec" {
		t.Errorf("first arg should be exec; got %v", spec.Args)
	}
}

func TestBuildModeInteractiveNoSubcommand(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICodex, Mode: invoke.ModeInteractive, Prompt: "hi",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(spec.Args) > 0 && spec.Args[0] == "exec" {
		t.Errorf("ModeInteractive should not include exec subcommand: %v", spec.Args)
	}
}

func TestBuildModeResumeDefaultIsInteractive(t *testing.T) {
	t.Parallel()
	// Default ModeResume (no output format) is the interactive
	// `codex resume <id>` path — matches typical human resume and
	// cross-CLI handoff (usp resume).
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICodex, Mode: invoke.ModeResume, SessionID: "abc",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if spec.Args[0] != "resume" {
		t.Errorf("expected interactive resume subcommand; got %v", spec.Args)
	}
	if slices.Contains(spec.Args, "exec") {
		t.Errorf("default resume should not include exec subcommand: %v", spec.Args)
	}
	if !slices.Contains(spec.Args, "abc") {
		t.Errorf("session id missing: %v", spec.Args)
	}
}

func TestBuildModeResumeWithOutputUsesExecResume(t *testing.T) {
	t.Parallel()
	// When the caller asks for structured output, headless makes
	// sense — switch to the `codex exec resume <id>` form.
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICodex, Mode: invoke.ModeResume, SessionID: "abc",
		Output: invoke.OutputStreamJSON,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"exec", "resume"}) {
		t.Errorf("expected exec resume with structured output; got %v", spec.Args)
	}
}

func TestBuildContinueAddsLast(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICodex, Mode: invoke.ModeResume, Continue: true,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !slices.Contains(spec.Args, "--last") {
		t.Errorf("expected --last flag for Continue; got %v", spec.Args)
	}
}

func TestBuildForkUsesForkSubcommand(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICodex, Mode: invoke.ModeResume, SessionID: "x", Fork: true,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if spec.Args[0] != "fork" {
		t.Errorf("expected fork subcommand; got %v", spec.Args)
	}
	// fork is a top-level subcommand — not "exec resume".
	if containsSubslice(spec.Args, []string{"exec", "resume"}) {
		t.Errorf("fork should not be combined with exec resume: %v", spec.Args)
	}
}

func TestBuildForkRequiresResume(t *testing.T) {
	t.Parallel()
	_, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICodex, Mode: invoke.ModeRun, Fork: true,
	})
	if err == nil {
		t.Error("expected error: Fork requires ModeResume")
	}
}

func TestBuildResumeRequiresIDOrContinue(t *testing.T) {
	t.Parallel()
	_, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICodex, Mode: invoke.ModeResume,
	})
	if err == nil {
		t.Error("expected error for resume without id/continue")
	}
}

func TestBuildSandboxTiers(t *testing.T) {
	t.Parallel()
	cases := []struct {
		s    invoke.SandboxMode
		flag string
	}{
		{invoke.SandboxReadOnly, "read-only"},
		{invoke.SandboxWorkspaceWrite, "workspace-write"},
	}
	for _, tc := range cases {
		t.Run(string(tc.s), func(t *testing.T) {
			spec, _, err := New().Build(invoke.Invocation{
				CLI: uxp.CLICodex, Mode: invoke.ModeRun, Sandbox: tc.s,
			})
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if !containsSubslice(spec.Args, []string{"-s", tc.flag}) {
				t.Errorf("args missing -s %s: %v", tc.flag, spec.Args)
			}
		})
	}
}

func TestBuildSandboxDangerNeedsOptIn(t *testing.T) {
	t.Parallel()
	_, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICodex, Mode: invoke.ModeRun,
		Sandbox: invoke.SandboxDangerFullAccess,
	})
	if err == nil {
		t.Error("expected error for SandboxDangerFullAccess without opt-in")
	}
}

func TestBuildApprovalAutoEditRefused(t *testing.T) {
	t.Parallel()
	_, ds, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICodex, Mode: invoke.ModeRun, Approval: invoke.ApprovalAutoEdit,
	})
	if err == nil {
		t.Error("expected error: ApprovalAutoEdit unsupported on codex")
	}
	if errs := ds.Errors(); len(errs) != 1 || errs[0].Option != "Approval" {
		t.Errorf("expected Approval-level error diagnostic; got %+v", ds)
	}
}

func TestBuildApprovalPlanShimsViaSandboxAndNever(t *testing.T) {
	t.Parallel()
	spec, ds, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICodex, Mode: invoke.ModeRun, Approval: invoke.ApprovalPlan,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"-s", "read-only"}) {
		t.Errorf("S-6 shim missing -s read-only: %v", spec.Args)
	}
	if !containsSubslice(spec.Args, []string{"-a", "never"}) {
		t.Errorf("S-6 shim missing -a never: %v", spec.Args)
	}
	hasPlanDiag := false
	for _, d := range ds {
		if d.Option == "Approval" && d.Level == "warning" {
			hasPlanDiag = true
		}
	}
	if !hasPlanDiag {
		t.Errorf("expected S-6 warning diagnostic; got %+v", ds)
	}
}

func TestBuildApprovalPlanRespectsExistingSandbox(t *testing.T) {
	t.Parallel()
	// If the caller already set a sandbox, S-6 should not overwrite it.
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICodex, Mode: invoke.ModeRun,
		Sandbox: invoke.SandboxWorkspaceWrite, Approval: invoke.ApprovalPlan,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"-s", "workspace-write"}) {
		t.Errorf("explicit sandbox should be preserved: %v", spec.Args)
	}
	roCount := 0
	for i, a := range spec.Args {
		if a == "-s" && i+1 < len(spec.Args) && spec.Args[i+1] == "read-only" {
			roCount++
		}
	}
	if roCount != 0 {
		t.Errorf("S-6 should not overwrite explicit sandbox; got read-only %d time(s) in %v", roCount, spec.Args)
	}
}

func TestBuildOutputJSONRequiresPath(t *testing.T) {
	t.Parallel()
	_, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICodex, Mode: invoke.ModeRun, Output: invoke.OutputJSON,
	})
	if err == nil {
		t.Error("expected error: OutputJSON requires output_last_message_path")
	}
}

func TestBuildOutputJSONWithPath(t *testing.T) {
	t.Parallel()
	spec, ds, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICodex, Mode: invoke.ModeRun, Output: invoke.OutputJSON,
		Config: map[string]string{"codex.output_last_message_path": "/tmp/out.json"},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"-o", "/tmp/out.json"}) {
		t.Errorf("expected -o /tmp/out.json; got %v", spec.Args)
	}
	hasOutputDiag := false
	for _, d := range ds {
		if d.Option == "Output" && d.Level == "warning" {
			hasOutputDiag = true
		}
	}
	if !hasOutputDiag {
		t.Errorf("expected Output warning diagnostic about file output: %+v", ds)
	}
}

func TestBuildOutputStreamJSON(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICodex, Mode: invoke.ModeRun, Output: invoke.OutputStreamJSON,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !slices.Contains(spec.Args, "--json") {
		t.Errorf("missing --json: %v", spec.Args)
	}
}

func TestBuildCWDEmitsBothFlagAndDir(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICodex, Mode: invoke.ModeRun, CWD: "/repo",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"-C", "/repo"}) {
		t.Errorf("expected -C /repo: %v", spec.Args)
	}
	if spec.Dir != "/repo" {
		t.Errorf("Dir = %q, want /repo", spec.Dir)
	}
}

func TestBuildAddDirsRepeatable(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICodex, Mode: invoke.ModeRun,
		AddDirs: []string{"/a", "/b"},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	count := 0
	for i, a := range spec.Args {
		if a == "--add-dir" && i+1 < len(spec.Args) {
			count++
		}
	}
	if count != 2 {
		t.Errorf("expected 2 --add-dir flags; got %d in %v", count, spec.Args)
	}
}

func TestBuildFilesShim(t *testing.T) {
	t.Parallel()
	spec, ds, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICodex, Mode: invoke.ModeRun,
		Files:  []string{"pkg/a/x.go", "pkg/b/y.go"},
		Prompt: "review",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"--add-dir", "pkg/a"}) {
		t.Errorf("missing --add-dir pkg/a: %v", spec.Args)
	}
	if !containsSubslice(spec.Args, []string{"--add-dir", "pkg/b"}) {
		t.Errorf("missing --add-dir pkg/b: %v", spec.Args)
	}
	last := spec.Args[len(spec.Args)-1]
	if !strings.Contains(last, "pkg/a/x.go") {
		t.Errorf("prompt block missing file: %q", last)
	}
	hasFilesDiag := false
	for _, d := range ds {
		if d.Option == "Files" && d.Level == "warning" {
			hasFilesDiag = true
		}
	}
	if !hasFilesDiag {
		t.Errorf("expected Files warning diagnostic: %+v", ds)
	}
}

func TestBuildImagesNative(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICodex, Mode: invoke.ModeRun,
		Images: []string{"a.png", "b.png"},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"-i", "a.png", "b.png"}) {
		t.Errorf("expected -i a.png b.png variadic; got %v", spec.Args)
	}
}

func TestBuildAgentRefused(t *testing.T) {
	t.Parallel()
	_, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICodex, Mode: invoke.ModeRun, Agent: "x",
	})
	if err == nil {
		t.Error("expected error: codex has no --agent")
	}
}

func TestBuildConfigKeys(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICodex, Mode: invoke.ModeRun,
		Config: map[string]string{
			"codex.profile":   "team",
			"codex.config":    `model="o3",sandbox_permissions=["disk-full-read-access"]`,
			"codex.enable":    "search,quickstart",
			"codex.search":    "true",
			"codex.ephemeral": "true",
		},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"-p", "team"}) {
		t.Errorf("missing -p team: %v", spec.Args)
	}
	configCount := 0
	for i, a := range spec.Args {
		if a == "-c" && i+1 < len(spec.Args) {
			configCount++
		}
	}
	if configCount != 2 {
		t.Errorf("expected 2 -c flags; got %d in %v", configCount, spec.Args)
	}
	if !slices.Contains(spec.Args, "--search") {
		t.Errorf("missing --search: %v", spec.Args)
	}
	if !slices.Contains(spec.Args, "--ephemeral") {
		t.Errorf("missing --ephemeral: %v", spec.Args)
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

func TestToolCapabilitiesIncludePlan(t *testing.T) {
	t.Parallel()
	tc := New().ToolCapabilities()
	for _, c := range tc {
		if c.Universal == "plan.update" {
			if c.Support != invoke.MappingNative {
				t.Errorf("plan.update should be Native on codex; got %v", c.Support)
			}
			return
		}
	}
	t.Error("ToolCapabilities missing plan.update (codex's distinctive tool)")
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
