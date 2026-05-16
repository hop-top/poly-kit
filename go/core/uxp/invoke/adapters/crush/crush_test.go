package crush

import (
	"slices"
	"testing"

	"hop.top/kit/go/core/uxp"
	"hop.top/kit/go/core/uxp/invoke"
)

func TestAdapterCLI(t *testing.T) {
	t.Parallel()
	if got := New().CLI(); got != uxp.CLICrush {
		t.Errorf("CLI() = %q, want %q", got, uxp.CLICrush)
	}
}

func TestBuildModeRunSubcommand(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICrush, Mode: invoke.ModeRun, Prompt: "hi",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if spec.Args[0] != "run" {
		t.Errorf("expected run subcommand; got %v", spec.Args)
	}
	if spec.Args[len(spec.Args)-1] != "hi" {
		t.Errorf("prompt should be last arg; got %v", spec.Args)
	}
}

func TestBuildResume(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICrush, Mode: invoke.ModeResume, SessionID: "abc",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"--session", "abc"}) {
		t.Errorf("missing --session abc: %v", spec.Args)
	}
}

func TestBuildContinue(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICrush, Mode: invoke.ModeResume, Continue: true,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !slices.Contains(spec.Args, "--continue") {
		t.Errorf("missing --continue: %v", spec.Args)
	}
}

func TestBuildOutputJSONRefused(t *testing.T) {
	t.Parallel()
	for _, out := range []invoke.OutputFormat{invoke.OutputJSON, invoke.OutputStreamJSON} {
		_, _, err := New().Build(invoke.Invocation{
			CLI: uxp.CLICrush, Mode: invoke.ModeRun, Output: out,
		})
		if err == nil {
			t.Errorf("expected error for Output=%s on crush (no --format flag)", out)
		}
	}
}

func TestBuildForkAndAgentRefused(t *testing.T) {
	t.Parallel()
	_, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICrush, Mode: invoke.ModeRun, Fork: true,
	})
	if err == nil {
		t.Error("expected error: crush has no fork")
	}
	_, _, err = New().Build(invoke.Invocation{
		CLI: uxp.CLICrush, Mode: invoke.ModeRun, Agent: "x",
	})
	if err == nil {
		t.Error("expected error: crush has no --agent")
	}
}

func TestBuildApprovalAutoEditRefused(t *testing.T) {
	t.Parallel()
	_, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICrush, Mode: invoke.ModeRun, Approval: invoke.ApprovalAutoEdit,
	})
	if err == nil {
		t.Error("expected error: ApprovalAutoEdit refused on crush")
	}
}

func TestBuildApprovalAutoAllOptIn(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICrush, Mode: invoke.ModeRun,
		Approval: invoke.ApprovalAutoAll,
		Config:   map[string]string{"uxp.allow_dangerous": "true"},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !slices.Contains(spec.Args, "--yolo") {
		t.Errorf("missing --yolo: %v", spec.Args)
	}
}

func TestBuildSandboxAndApprovalDontDoublyEmitYolo(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICrush, Mode: invoke.ModeRun,
		Sandbox:  invoke.SandboxDangerFullAccess,
		Approval: invoke.ApprovalAutoAll,
		Config:   map[string]string{"uxp.allow_dangerous": "true"},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	count := 0
	for _, a := range spec.Args {
		if a == "--yolo" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected --yolo exactly once; got %d in %v", count, spec.Args)
	}
}

func TestBuildCWDViaCwd(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICrush, Mode: invoke.ModeRun, CWD: "/repo",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"--cwd", "/repo"}) {
		t.Errorf("missing --cwd /repo: %v", spec.Args)
	}
}

func TestBuildConfigKeys(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICrush, Mode: invoke.ModeRun,
		Config: map[string]string{
			"crush.small_model": "haiku",
			"crush.data_dir":    "/var/crush",
			"crush.quiet":       "true",
		},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"--small-model", "haiku"}) {
		t.Errorf("missing --small-model: %v", spec.Args)
	}
	if !containsSubslice(spec.Args, []string{"--data-dir", "/var/crush"}) {
		t.Errorf("missing --data-dir: %v", spec.Args)
	}
	if !slices.Contains(spec.Args, "--quiet") {
		t.Errorf("missing --quiet: %v", spec.Args)
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
