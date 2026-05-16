package copilot

import (
	"slices"
	"strings"
	"testing"

	"hop.top/kit/go/core/uxp"
	"hop.top/kit/go/core/uxp/invoke"
)

func TestAdapterCLI(t *testing.T) {
	t.Parallel()
	if got := New().CLI(); got != uxp.CLICopilot {
		t.Errorf("CLI() = %q, want %q", got, uxp.CLICopilot)
	}
}

func TestBuildModeRunUsesDashP(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICopilot, Mode: invoke.ModeRun, Prompt: "hi",
		Approval: invoke.ApprovalAutoAll,
		Config:   map[string]string{"uxp.allow_dangerous": "true"},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	// -p should be present with the prompt as its value.
	for i, a := range spec.Args {
		if a == "-p" && i+1 < len(spec.Args) && spec.Args[i+1] == "hi" {
			return
		}
	}
	t.Errorf("expected -p hi; got %v", spec.Args)
}

func TestBuildModeInteractiveUsesDashI(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICopilot, Mode: invoke.ModeInteractive, Prompt: "hi",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"-i", "hi"}) {
		t.Errorf("expected -i hi for interactive with prompt; got %v", spec.Args)
	}
}

func TestBuildResumeWithIDEqualsForm(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICopilot, Mode: invoke.ModeResume, SessionID: "abc",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !slices.Contains(spec.Args, "--resume=abc") {
		t.Errorf("expected --resume=abc; got %v", spec.Args)
	}
}

func TestBuildContinue(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICopilot, Mode: invoke.ModeResume, Continue: true,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !slices.Contains(spec.Args, "--continue") {
		t.Errorf("missing --continue: %v", spec.Args)
	}
}

func TestBuildResumeNeedsIDOrContinue(t *testing.T) {
	t.Parallel()
	_, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICopilot, Mode: invoke.ModeResume,
	})
	if err == nil {
		t.Error("expected error: resume needs id or continue")
	}
}

func TestBuildForkRefused(t *testing.T) {
	t.Parallel()
	_, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICopilot, Mode: invoke.ModeRun, Fork: true,
	})
	if err == nil {
		t.Error("expected error: copilot has no fork")
	}
}

func TestBuildOutputJSONIsShim(t *testing.T) {
	t.Parallel()
	spec, ds, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICopilot, Mode: invoke.ModeRun, Output: invoke.OutputJSON,
		Approval: invoke.ApprovalAutoAll,
		Config:   map[string]string{"uxp.allow_dangerous": "true"},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"--output-format", "json"}) {
		t.Errorf("missing --output-format json: %v", spec.Args)
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

func TestBuildSandboxTiersRefused(t *testing.T) {
	t.Parallel()
	for _, s := range []invoke.SandboxMode{invoke.SandboxReadOnly, invoke.SandboxWorkspaceWrite} {
		_, _, err := New().Build(invoke.Invocation{
			CLI: uxp.CLICopilot, Mode: invoke.ModeRun, Sandbox: s,
			Approval: invoke.ApprovalAutoAll,
			Config:   map[string]string{"uxp.allow_dangerous": "true"},
		})
		if err == nil {
			t.Errorf("expected error for Sandbox=%s on copilot", s)
		}
	}
}

func TestBuildApprovalAutoEditRefused(t *testing.T) {
	t.Parallel()
	_, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICopilot, Mode: invoke.ModeRun, Approval: invoke.ApprovalAutoEdit,
	})
	if err == nil {
		t.Error("expected error: ApprovalAutoEdit unsupported on copilot")
	}
}

func TestBuildApprovalAutoAllOptIn(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICopilot, Mode: invoke.ModeRun,
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

func TestBuildModeRunWithoutAutoApproveWarns(t *testing.T) {
	t.Parallel()
	_, ds, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICopilot, Mode: invoke.ModeRun, Prompt: "x",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	hasWarn := false
	for _, d := range ds {
		if d.Option == "Approval" && d.Level == "warning" {
			hasWarn = true
		}
	}
	if !hasWarn {
		t.Errorf("expected Approval warning for ModeRun without auto-approve: %+v", ds)
	}
}

func TestBuildModeRunWithAllowToolConfigSuppressesWarn(t *testing.T) {
	t.Parallel()
	_, ds, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICopilot, Mode: invoke.ModeRun, Prompt: "x",
		Config: map[string]string{
			"copilot.allow_tool": "shell(git:*),write",
		},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	for _, d := range ds {
		if d.Option == "Approval" && d.Level == "warning" {
			t.Errorf("unexpected Approval warning when allow_tool is set: %+v", d)
		}
	}
}

func TestBuildAddDirsRepeatable(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICopilot, Mode: invoke.ModeRun, Prompt: "x",
		AddDirs:  []string{"/a", "/b"},
		Approval: invoke.ApprovalAutoAll,
		Config:   map[string]string{"uxp.allow_dangerous": "true"},
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
		CLI: uxp.CLICopilot, Mode: invoke.ModeRun,
		Files:    []string{"pkg/a/x.go", "pkg/b/y.go"},
		Prompt:   "review",
		Approval: invoke.ApprovalAutoAll,
		Config:   map[string]string{"uxp.allow_dangerous": "true"},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{"pkg/a", "pkg/b"} {
		if !containsSubslice(spec.Args, []string{"--add-dir", want}) {
			t.Errorf("missing --add-dir %s: %v", want, spec.Args)
		}
	}
	// Prompt block in the -p value.
	for i, a := range spec.Args {
		if a == "-p" && i+1 < len(spec.Args) {
			if !strings.Contains(spec.Args[i+1], "pkg/a/x.go") {
				t.Errorf("prompt missing file block: %q", spec.Args[i+1])
			}
		}
	}
	hasFilesDiag := false
	for _, d := range ds {
		if d.Option == "Files" && d.Level == "warning" {
			hasFilesDiag = true
		}
	}
	if !hasFilesDiag {
		t.Errorf("expected Files warning: %+v", ds)
	}
}

func TestBuildConfigPolicyKeys(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICopilot, Mode: invoke.ModeRun, Prompt: "x",
		Config: map[string]string{
			"copilot.allow_tool":   `shell(git:*),write,WebSearch`,
			"copilot.deny_tool":    "shell(git push)",
			"copilot.allow_url":    "github.com",
			"copilot.allow_all":    "true",
			"copilot.no_ask_user":  "true",
			"copilot.effort":       "high",
			"copilot.unknown_flag": "x",
		},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	allowToolCount := 0
	for _, a := range spec.Args {
		if strings.HasPrefix(a, "--allow-tool=") {
			allowToolCount++
		}
	}
	if allowToolCount != 3 {
		t.Errorf("expected 3 --allow-tool=...; got %d in %v", allowToolCount, spec.Args)
	}
	if !slices.Contains(spec.Args, "--deny-tool=shell(git push)") {
		t.Errorf("missing --deny-tool: %v", spec.Args)
	}
	if !slices.Contains(spec.Args, "--allow-url=github.com") {
		t.Errorf("missing --allow-url: %v", spec.Args)
	}
	if !slices.Contains(spec.Args, "--allow-all") {
		t.Errorf("missing --allow-all: %v", spec.Args)
	}
	if !slices.Contains(spec.Args, "--no-ask-user") {
		t.Errorf("missing --no-ask-user: %v", spec.Args)
	}
	if !containsSubslice(spec.Args, []string{"--effort", "high"}) {
		t.Errorf("missing --effort high: %v", spec.Args)
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
