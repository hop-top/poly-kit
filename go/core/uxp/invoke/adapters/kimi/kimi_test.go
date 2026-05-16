package kimi

import (
	"slices"
	"testing"

	"hop.top/kit/go/core/uxp"
	"hop.top/kit/go/core/uxp/invoke"
)

func TestAdapterCLI(t *testing.T) {
	t.Parallel()
	if got := New().CLI(); got != uxp.CLIKimi {
		t.Errorf("CLI() = %q, want %q", got, uxp.CLIKimi)
	}
}

func TestBuildModeRunUsesPrint(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIKimi, Mode: invoke.ModeRun, Prompt: "hi",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !slices.Contains(spec.Args, "--print") {
		t.Errorf("missing --print: %v", spec.Args)
	}
	if !containsSubslice(spec.Args, []string{"--prompt", "hi"}) {
		t.Errorf("missing --prompt hi: %v", spec.Args)
	}
}

func TestBuildResumeWithID(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIKimi, Mode: invoke.ModeResume, SessionID: "abc",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"-S", "abc"}) {
		t.Errorf("missing -S abc: %v", spec.Args)
	}
}

func TestBuildContinue(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIKimi, Mode: invoke.ModeResume, Continue: true,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !slices.Contains(spec.Args, "-C") {
		t.Errorf("missing -C: %v", spec.Args)
	}
}

func TestBuildForkRefused(t *testing.T) {
	t.Parallel()
	_, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIKimi, Mode: invoke.ModeRun, Fork: true,
	})
	if err == nil {
		t.Error("expected error: kimi has no fork")
	}
}

func TestBuildApprovalPlanNative(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIKimi, Mode: invoke.ModeRun, Approval: invoke.ApprovalPlan,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !slices.Contains(spec.Args, "--plan") {
		t.Errorf("missing --plan: %v", spec.Args)
	}
}

func TestBuildApprovalAutoEditRefused(t *testing.T) {
	t.Parallel()
	_, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIKimi, Mode: invoke.ModeRun, Approval: invoke.ApprovalAutoEdit,
	})
	if err == nil {
		t.Error("expected error: ApprovalAutoEdit refused on kimi")
	}
}

func TestBuildApprovalAutoAllOptIn(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIKimi, Mode: invoke.ModeRun, Approval: invoke.ApprovalAutoAll,
		Config: map[string]string{"uxp.allow_dangerous": "true"},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !slices.Contains(spec.Args, "--yolo") {
		t.Errorf("missing --yolo: %v", spec.Args)
	}
}

func TestBuildSandboxAllRefused(t *testing.T) {
	t.Parallel()
	for _, s := range []invoke.SandboxMode{invoke.SandboxReadOnly, invoke.SandboxWorkspaceWrite, invoke.SandboxDangerFullAccess} {
		_, _, err := New().Build(invoke.Invocation{
			CLI: uxp.CLIKimi, Mode: invoke.ModeRun, Sandbox: s,
		})
		if err == nil {
			t.Errorf("expected error for Sandbox=%s on kimi", s)
		}
	}
}

func TestBuildOutputJSONShim(t *testing.T) {
	t.Parallel()
	spec, ds, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIKimi, Mode: invoke.ModeRun, Output: invoke.OutputJSON,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"--output-format", "text", "--final-message-only"}) {
		t.Errorf("missing OutputJSON shim flags: %v", spec.Args)
	}
	hasShimDiag := false
	for _, d := range ds {
		if d.Option == "Output" && d.Level == "warning" {
			hasShimDiag = true
		}
	}
	if !hasShimDiag {
		t.Errorf("expected Output shim warning: %+v", ds)
	}
}

func TestBuildOutputStreamJSONNative(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIKimi, Mode: invoke.ModeRun, Output: invoke.OutputStreamJSON,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"--output-format", "stream-json"}) {
		t.Errorf("missing --output-format stream-json: %v", spec.Args)
	}
}

func TestBuildAgent(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIKimi, Mode: invoke.ModeRun, Agent: "okabe",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"--agent", "okabe"}) {
		t.Errorf("missing --agent okabe: %v", spec.Args)
	}
}

func TestBuildCWDUsesWorkDir(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIKimi, Mode: invoke.ModeRun, CWD: "/repo",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"--work-dir", "/repo"}) {
		t.Errorf("missing --work-dir /repo: %v", spec.Args)
	}
}

func TestBuildAddDirsRepeatable(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIKimi, Mode: invoke.ModeRun,
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
		t.Errorf("expected 2 --add-dir; got %d in %v", count, spec.Args)
	}
}

func TestBuildConfigKeys(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIKimi, Mode: invoke.ModeRun,
		Config: map[string]string{
			"kimi.thinking":           "true",
			"kimi.afk":                "true",
			"kimi.agent_file":         "/agents/custom.md",
			"kimi.skills_dir":         "/skills/a,/skills/b",
			"kimi.max_steps_per_turn": "10",
		},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !slices.Contains(spec.Args, "--thinking") {
		t.Errorf("missing --thinking: %v", spec.Args)
	}
	if !slices.Contains(spec.Args, "--afk") {
		t.Errorf("missing --afk: %v", spec.Args)
	}
	if !containsSubslice(spec.Args, []string{"--agent-file", "/agents/custom.md"}) {
		t.Errorf("missing --agent-file: %v", spec.Args)
	}
	skillsCount := 0
	for i, a := range spec.Args {
		if a == "--skills-dir" && i+1 < len(spec.Args) {
			skillsCount++
		}
	}
	if skillsCount != 2 {
		t.Errorf("expected 2 --skills-dir; got %d in %v", skillsCount, spec.Args)
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
