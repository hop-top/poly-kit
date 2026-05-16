package qwen

import (
	"slices"
	"strings"
	"testing"

	"hop.top/kit/go/core/uxp"
	"hop.top/kit/go/core/uxp/invoke"
)

func TestAdapterCLI(t *testing.T) {
	t.Parallel()
	if got := New().CLI(); got != uxp.CLIQwen {
		t.Errorf("CLI() = %q, want %q", got, uxp.CLIQwen)
	}
}

func TestBuildModeRunPositional(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIQwen, Mode: invoke.ModeRun, Prompt: "hi",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	// Prompt is positional (last arg) — no -p flag.
	if slices.Contains(spec.Args, "-p") {
		t.Errorf("qwen prefers positional prompt; -p is deprecated. Got %v", spec.Args)
	}
	if spec.Args[len(spec.Args)-1] != "hi" {
		t.Errorf("expected prompt as last arg; got %v", spec.Args)
	}
}

func TestBuildResumeWithID(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIQwen, Mode: invoke.ModeResume, SessionID: "abc",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"-r", "abc"}) {
		t.Errorf("expected -r abc; got %v", spec.Args)
	}
}

func TestBuildContinue(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIQwen, Mode: invoke.ModeResume, Continue: true,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !slices.Contains(spec.Args, "-c") {
		t.Errorf("missing -c: %v", spec.Args)
	}
}

func TestBuildResumeNeedsIDOrContinue(t *testing.T) {
	t.Parallel()
	_, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIQwen, Mode: invoke.ModeResume,
	})
	if err == nil {
		t.Error("expected error for resume without id/continue")
	}
}

func TestBuildForkAndAgentRefused(t *testing.T) {
	t.Parallel()
	_, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIQwen, Mode: invoke.ModeRun, Fork: true,
	})
	if err == nil {
		t.Error("expected error: qwen has no fork")
	}
	_, _, err = New().Build(invoke.Invocation{
		CLI: uxp.CLIQwen, Mode: invoke.ModeRun, Agent: "x",
	})
	if err == nil {
		t.Error("expected error: qwen has no --agent")
	}
}

func TestBuildApprovalModes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		a    invoke.ApprovalMode
		mode string
	}{
		{invoke.ApprovalAutoEdit, "auto-edit"},
		{invoke.ApprovalPlan, "plan"},
	}
	for _, tc := range cases {
		t.Run(string(tc.a), func(t *testing.T) {
			spec, _, err := New().Build(invoke.Invocation{
				CLI: uxp.CLIQwen, Mode: invoke.ModeRun, Approval: tc.a,
			})
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if !containsSubslice(spec.Args, []string{"--approval-mode", tc.mode}) {
				t.Errorf("missing --approval-mode %s: %v", tc.mode, spec.Args)
			}
		})
	}
}

func TestBuildApprovalNeverRefused(t *testing.T) {
	t.Parallel()
	_, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIQwen, Mode: invoke.ModeRun, Approval: invoke.ApprovalNever,
	})
	if err == nil {
		t.Error("expected error: ApprovalNever unsupported on qwen")
	}
}

func TestBuildApprovalAutoAllOptIn(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIQwen, Mode: invoke.ModeRun,
		Approval: invoke.ApprovalAutoAll,
		Config:   map[string]string{"uxp.allow_dangerous": "true"},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"--approval-mode", "yolo"}) {
		t.Errorf("missing --approval-mode yolo: %v", spec.Args)
	}
}

func TestBuildSandboxBoolean(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIQwen, Mode: invoke.ModeRun, Sandbox: invoke.SandboxWorkspaceWrite,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !slices.Contains(spec.Args, "--sandbox") {
		t.Errorf("missing --sandbox: %v", spec.Args)
	}
}

func TestBuildSandboxDangerNeedsOptIn(t *testing.T) {
	t.Parallel()
	_, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIQwen, Mode: invoke.ModeRun, Sandbox: invoke.SandboxDangerFullAccess,
	})
	if err == nil {
		t.Error("expected error for SandboxDangerFullAccess without opt-in")
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
				CLI: uxp.CLIQwen, Mode: invoke.ModeRun, Output: tc.out,
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

func TestBuildAddDirsRepeatable(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIQwen, Mode: invoke.ModeRun, AddDirs: []string{"/a", "/b"},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	count := 0
	for i, a := range spec.Args {
		if a == "--include-directories" && i+1 < len(spec.Args) {
			count++
		}
	}
	if count != 2 {
		t.Errorf("expected 2 --include-directories flags; got %d in %v", count, spec.Args)
	}
}

func TestBuildFilesShim(t *testing.T) {
	t.Parallel()
	spec, ds, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIQwen, Mode: invoke.ModeRun,
		Files:  []string{"pkg/a/x.go", "pkg/b/y.go"},
		Prompt: "review",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{"pkg/a", "pkg/b"} {
		if !containsSubslice(spec.Args, []string{"--include-directories", want}) {
			t.Errorf("missing --include-directories %s: %v", want, spec.Args)
		}
	}
	last := spec.Args[len(spec.Args)-1]
	if !strings.Contains(last, "pkg/a/x.go") {
		t.Errorf("prompt block missing file: %q", last)
	}
	hasDiag := false
	for _, d := range ds {
		if d.Option == "Files" && d.Level == "warning" {
			hasDiag = true
		}
	}
	if !hasDiag {
		t.Errorf("expected Files warning diagnostic: %+v", ds)
	}
}

func TestBuildConfigKeys(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIQwen, Mode: invoke.ModeRun,
		Config: map[string]string{
			"qwen.system_prompt":     "be concise",
			"qwen.max_session_turns": "5",
			"qwen.allowed_tools":     "shell,read,edit",
			"qwen.session_id":        "fixed-id",
			"qwen.unknown":           "x",
		},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"--system-prompt", "be concise"}) {
		t.Errorf("missing --system-prompt: %v", spec.Args)
	}
	if !containsSubslice(spec.Args, []string{"--max-session-turns", "5"}) {
		t.Errorf("missing --max-session-turns: %v", spec.Args)
	}
	if !containsSubslice(spec.Args, []string{"--session-id", "fixed-id"}) {
		t.Errorf("missing --session-id: %v", spec.Args)
	}
	allowedCount := 0
	for i, a := range spec.Args {
		if a == "--allowed-tools" && i+1 < len(spec.Args) {
			allowedCount++
		}
	}
	if allowedCount != 3 {
		t.Errorf("expected 3 --allowed-tools; got %d in %v", allowedCount, spec.Args)
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
