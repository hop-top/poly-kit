package gemini

import (
	"slices"
	"strings"
	"testing"

	"hop.top/kit/go/core/uxp"
	"hop.top/kit/go/core/uxp/invoke"
)

func TestAdapterCLI(t *testing.T) {
	t.Parallel()
	if got := New().CLI(); got != uxp.CLIGemini {
		t.Errorf("CLI() = %q, want %q", got, uxp.CLIGemini)
	}
}

func TestBuildModeRunUsesDashP(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIGemini, Mode: invoke.ModeRun, Prompt: "hi",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"-p", "hi"}) {
		t.Errorf("args missing -p hi: %v", spec.Args)
	}
}

func TestBuildModeResumeWithSession(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIGemini, Mode: invoke.ModeResume, SessionID: "abc",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"--resume", "abc"}) {
		t.Errorf("args missing --resume abc: %v", spec.Args)
	}
}

func TestBuildContinueResumesLatest(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIGemini, Mode: invoke.ModeResume, Continue: true,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"--resume", "latest"}) {
		t.Errorf("args missing --resume latest: %v", spec.Args)
	}
}

func TestBuildResumeRequiresIDOrContinue(t *testing.T) {
	t.Parallel()
	_, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIGemini, Mode: invoke.ModeResume,
	})
	if err == nil {
		t.Error("expected error for resume without id/continue")
	}
}

func TestBuildForkRefused(t *testing.T) {
	t.Parallel()
	_, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIGemini, Mode: invoke.ModeRun, Fork: true,
	})
	if err == nil {
		t.Error("expected error: gemini does not support fork")
	}
}

func TestBuildAgentRefused(t *testing.T) {
	t.Parallel()
	_, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIGemini, Mode: invoke.ModeRun, Agent: "reviewer",
	})
	if err == nil {
		t.Error("expected error: gemini does not have --agent")
	}
}

func TestBuildApprovalModes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		a    invoke.ApprovalMode
		mode string
	}{
		{invoke.ApprovalAutoEdit, "auto_edit"},
		{invoke.ApprovalPlan, "plan"},
	}
	for _, tc := range cases {
		t.Run(string(tc.a), func(t *testing.T) {
			spec, _, err := New().Build(invoke.Invocation{
				CLI: uxp.CLIGemini, Mode: invoke.ModeRun, Approval: tc.a,
			})
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if !containsSubslice(spec.Args, []string{"--approval-mode", tc.mode}) {
				t.Errorf("args missing --approval-mode %s: %v", tc.mode, spec.Args)
			}
		})
	}
}

func TestBuildApprovalNeverRefused(t *testing.T) {
	t.Parallel()
	_, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIGemini, Mode: invoke.ModeRun, Approval: invoke.ApprovalNever,
	})
	if err == nil {
		t.Error("expected error for ApprovalNever (gemini has no equivalent)")
	}
}

func TestBuildApprovalAutoAllRequiresOptIn(t *testing.T) {
	t.Parallel()
	_, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIGemini, Mode: invoke.ModeRun, Approval: invoke.ApprovalAutoAll,
	})
	if err == nil {
		t.Error("expected error for AutoAll without opt-in")
	}
}

func TestBuildApprovalAutoAllOptIn(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIGemini, Mode: invoke.ModeRun,
		Approval: invoke.ApprovalAutoAll,
		Config:   map[string]string{"uxp.allow_dangerous": "true"},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"--approval-mode", "yolo"}) {
		t.Errorf("args missing --approval-mode yolo: %v", spec.Args)
	}
}

func TestBuildSandboxBoolean(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIGemini, Mode: invoke.ModeRun,
		Sandbox: invoke.SandboxWorkspaceWrite,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !slices.Contains(spec.Args, "--sandbox") {
		t.Errorf("args missing --sandbox: %v", spec.Args)
	}
}

func TestBuildSandboxDangerNeedsOptIn(t *testing.T) {
	t.Parallel()
	_, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIGemini, Mode: invoke.ModeRun,
		Sandbox: invoke.SandboxDangerFullAccess,
	})
	if err == nil {
		t.Error("expected error for SandboxDangerFullAccess without opt-in")
	}
}

func TestBuildAddDirsRepeatable(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIGemini, Mode: invoke.ModeRun,
		AddDirs: []string{"/a", "/b"},
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

func TestBuildFilesShimToParents(t *testing.T) {
	t.Parallel()
	spec, ds, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIGemini, Mode: invoke.ModeRun,
		Files:  []string{"pkg/a/x.go", "pkg/a/y.go", "pkg/b/z.go"},
		Prompt: "review",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	// pkg/a and pkg/b should both appear behind --include-directories.
	if !containsSubslice(spec.Args, []string{"--include-directories", "pkg/a"}) {
		t.Errorf("args missing --include-directories pkg/a: %v", spec.Args)
	}
	if !containsSubslice(spec.Args, []string{"--include-directories", "pkg/b"}) {
		t.Errorf("args missing --include-directories pkg/b: %v", spec.Args)
	}
	// Files diagnostic.
	hasDiag := false
	for _, d := range ds {
		if d.Option == "Files" && d.Level == "warning" {
			hasDiag = true
		}
	}
	if !hasDiag {
		t.Errorf("expected Files warning diagnostic; got %+v", ds)
	}
	// Prompt block in -p value.
	for i, a := range spec.Args {
		if a == "-p" && i+1 < len(spec.Args) {
			if !strings.Contains(spec.Args[i+1], "pkg/a/x.go") {
				t.Errorf("-p prompt missing file block: %q", spec.Args[i+1])
			}
			if !strings.Contains(spec.Args[i+1], "review") {
				t.Errorf("-p prompt missing original text: %q", spec.Args[i+1])
			}
		}
	}
}

func TestBuildFilesNoDoubleListWithAddDirs(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIGemini, Mode: invoke.ModeRun,
		AddDirs: []string{"pkg/a"},
		Files:   []string{"pkg/a/x.go"}, // parent already in AddDirs
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	pkgACount := 0
	for i, a := range spec.Args {
		if a == "--include-directories" && i+1 < len(spec.Args) && spec.Args[i+1] == "pkg/a" {
			pkgACount++
		}
	}
	if pkgACount != 1 {
		t.Errorf("pkg/a should appear once after dedup; got %d in %v", pkgACount, spec.Args)
	}
}

func TestBuildOutputJSON(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIGemini, Mode: invoke.ModeRun, Output: invoke.OutputJSON,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"--output-format", "json"}) {
		t.Errorf("args missing --output-format json: %v", spec.Args)
	}
}

func TestBuildConfigPolicies(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIGemini, Mode: invoke.ModeRun,
		Config: map[string]string{
			"gemini.policy":       "policies/team.yaml,policies/strict.yaml",
			"gemini.skip_trust":   "true",
			"gemini.unknown_flag": "x",
		},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	policyCount := 0
	for i, a := range spec.Args {
		if a == "--policy" && i+1 < len(spec.Args) {
			policyCount++
		}
	}
	if policyCount != 2 {
		t.Errorf("expected 2 --policy flags; got %d in %v", policyCount, spec.Args)
	}
	if !slices.Contains(spec.Args, "--skip-trust") {
		t.Errorf("missing --skip-trust: %v", spec.Args)
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

func TestToolCapabilitiesNonEmpty(t *testing.T) {
	t.Parallel()
	tc := New().ToolCapabilities()
	if len(tc) == 0 {
		t.Fatal("ToolCapabilities empty")
	}
	for _, w := range []string{"shell.exec", "file.read", "web.search"} {
		found := false
		for _, c := range tc {
			if c.Universal == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing %q in ToolCapabilities", w)
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
