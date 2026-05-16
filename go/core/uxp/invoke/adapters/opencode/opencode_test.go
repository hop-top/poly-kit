package opencode

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"hop.top/kit/go/core/uxp"
	"hop.top/kit/go/core/uxp/invoke"
)

func TestAdapterCLI(t *testing.T) {
	t.Parallel()
	if got := New().CLI(); got != uxp.CLIOpenCode {
		t.Errorf("CLI() = %q, want %q", got, uxp.CLIOpenCode)
	}
}

func TestBuildModeRunSubcommand(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIOpenCode, Mode: invoke.ModeRun, Prompt: "hi",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if spec.Args[0] != "run" {
		t.Errorf("first arg should be run; got %v", spec.Args)
	}
}

func TestBuildModeResumeWithSession(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIOpenCode, Mode: invoke.ModeResume, SessionID: "abc",
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
		CLI: uxp.CLIOpenCode, Mode: invoke.ModeResume, Continue: true,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !slices.Contains(spec.Args, "--continue") {
		t.Errorf("missing --continue: %v", spec.Args)
	}
}

func TestBuildFork(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIOpenCode, Mode: invoke.ModeResume, SessionID: "x", Fork: true,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !slices.Contains(spec.Args, "--fork") {
		t.Errorf("missing --fork: %v", spec.Args)
	}
}

func TestBuildForkRequiresResume(t *testing.T) {
	t.Parallel()
	_, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIOpenCode, Mode: invoke.ModeRun, Fork: true,
	})
	if err == nil {
		t.Error("expected error for Fork outside Resume")
	}
}

func TestBuildResumeNeedsIDOrContinue(t *testing.T) {
	t.Parallel()
	_, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIOpenCode, Mode: invoke.ModeResume,
	})
	if err == nil {
		t.Error("expected error for resume without id/continue")
	}
}

func TestBuildOutputJSONIsShim(t *testing.T) {
	t.Parallel()
	spec, ds, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIOpenCode, Mode: invoke.ModeRun, Output: invoke.OutputJSON,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"--format", "json"}) {
		t.Errorf("missing --format json: %v", spec.Args)
	}
	hasOutDiag := false
	for _, d := range ds {
		if d.Option == "Output" && d.Level == "warning" {
			hasOutDiag = true
		}
	}
	if !hasOutDiag {
		t.Errorf("expected Output warning diagnostic about JSONL stream: %+v", ds)
	}
}

func TestBuildOutputStreamJSONNative(t *testing.T) {
	t.Parallel()
	spec, ds, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIOpenCode, Mode: invoke.ModeRun, Output: invoke.OutputStreamJSON,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"--format", "json"}) {
		t.Errorf("missing --format json: %v", spec.Args)
	}
	// StreamJSON is native — no warning diagnostic for Output expected.
	for _, d := range ds {
		if d.Option == "Output" && d.Level == "warning" {
			t.Errorf("unexpected Output warning for StreamJSON: %+v", d)
		}
	}
}

func TestBuildSandboxReadOnlyRefused(t *testing.T) {
	t.Parallel()
	_, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIOpenCode, Mode: invoke.ModeRun, Sandbox: invoke.SandboxReadOnly,
	})
	if err == nil {
		t.Error("expected error: opencode has no per-tier sandbox")
	}
}

func TestBuildSandboxDangerNeedsOptIn(t *testing.T) {
	t.Parallel()
	_, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIOpenCode, Mode: invoke.ModeRun, Sandbox: invoke.SandboxDangerFullAccess,
	})
	if err == nil {
		t.Error("expected error: SandboxDangerFullAccess without opt-in")
	}
}

func TestBuildApprovalAutoEditRefused(t *testing.T) {
	t.Parallel()
	_, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIOpenCode, Mode: invoke.ModeRun, Approval: invoke.ApprovalAutoEdit,
	})
	if err == nil {
		t.Error("expected error: ApprovalAutoEdit unsupported on opencode")
	}
}

func TestBuildApprovalAutoAllOptIn(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIOpenCode, Mode: invoke.ModeRun, Approval: invoke.ApprovalAutoAll,
		Config: map[string]string{"uxp.allow_dangerous": "true"},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !slices.Contains(spec.Args, "--dangerously-skip-permissions") {
		t.Errorf("missing --dangerously-skip-permissions: %v", spec.Args)
	}
}

func TestBuildSandboxAndApprovalDontDoublyEmitDangerFlag(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIOpenCode, Mode: invoke.ModeRun,
		Sandbox:  invoke.SandboxDangerFullAccess,
		Approval: invoke.ApprovalAutoAll,
		Config:   map[string]string{"uxp.allow_dangerous": "true"},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	count := 0
	for _, a := range spec.Args {
		if a == "--dangerously-skip-permissions" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected --dangerously-skip-permissions exactly once; got %d in %v", count, spec.Args)
	}
}

func TestBuildFilesNative(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIOpenCode, Mode: invoke.ModeRun,
		Files: []string{"a.go", "b.go"},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	count := 0
	for i, a := range spec.Args {
		if a == "--file" && i+1 < len(spec.Args) {
			count++
		}
	}
	if count != 2 {
		t.Errorf("expected 2 --file flags; got %d in %v", count, spec.Args)
	}
}

func TestBuildAddDirsS2Walk(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, p := range []string{"a.go", "sub/b.go", "sub/c.go"} {
		full := filepath.Join(root, p)
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		_ = os.WriteFile(full, []byte("x"), 0o644)
	}
	spec, ds, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIOpenCode, Mode: invoke.ModeRun,
		AddDirs: []string{root},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	count := 0
	for i, a := range spec.Args {
		if a == "--file" && i+1 < len(spec.Args) {
			count++
		}
	}
	if count != 3 {
		t.Errorf("expected 3 --file flags from S-2 walk; got %d in %v", count, spec.Args)
	}
	hasAddDirsDiag := false
	for _, d := range ds {
		if d.Option == "AddDirs" && d.Level == "warning" {
			hasAddDirsDiag = true
		}
	}
	if !hasAddDirsDiag {
		t.Errorf("expected AddDirs warning diagnostic: %+v", ds)
	}
}

func TestBuildAddDirsS2OverflowRefused(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for i := range 5 {
		_ = os.WriteFile(filepath.Join(root, string(rune('a'+i))+".txt"), []byte("x"), 0o644)
	}
	_, ds, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIOpenCode, Mode: invoke.ModeRun,
		AddDirs: []string{root},
		Config:  map[string]string{"uxp.shim.dir_to_files_max": "2"},
	})
	if err == nil {
		t.Fatal("expected error for S-2 overflow at cap=2")
	}
	hasError := false
	for _, d := range ds {
		if d.Option == "AddDirs" && d.Level == "error" {
			hasError = true
		}
	}
	if !hasError {
		t.Errorf("expected error-level AddDirs diagnostic: %+v", ds)
	}
}

func TestBuildAddDirsAndFilesNoDoubleListing(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	a := filepath.Join(root, "a.txt")
	b := filepath.Join(root, "b.txt")
	for _, p := range []string{a, b} {
		_ = os.WriteFile(p, []byte("x"), 0o644)
	}
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIOpenCode, Mode: invoke.ModeRun,
		Files:   []string{a},
		AddDirs: []string{root},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	aCount := 0
	bCount := 0
	for i, arg := range spec.Args {
		if arg == "--file" && i+1 < len(spec.Args) {
			switch spec.Args[i+1] {
			case a:
				aCount++
			case b:
				bCount++
			}
		}
	}
	if aCount != 1 {
		t.Errorf("a.txt listed %d times; want 1 (already in Files): %v", aCount, spec.Args)
	}
	if bCount != 1 {
		t.Errorf("b.txt listed %d times; want 1 (from S-2 walk): %v", bCount, spec.Args)
	}
}

func TestBuildImagesViaFile(t *testing.T) {
	t.Parallel()
	spec, ds, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIOpenCode, Mode: invoke.ModeRun,
		Images: []string{"a.png"},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"--file", "a.png"}) {
		t.Errorf("expected --file a.png; got %v", spec.Args)
	}
	hasImgInfo := false
	for _, d := range ds {
		if d.Option == "Images" && d.Level == "info" {
			hasImgInfo = true
		}
	}
	if !hasImgInfo {
		t.Errorf("expected Images info diagnostic: %+v", ds)
	}
}

func TestBuildCWDViaDir(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIOpenCode, Mode: invoke.ModeRun, CWD: "/repo",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"--dir", "/repo"}) {
		t.Errorf("expected --dir /repo: %v", spec.Args)
	}
	if spec.Dir != "/repo" {
		t.Errorf("Dir = %q, want /repo", spec.Dir)
	}
}

func TestBuildModelAndAgent(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIOpenCode, Mode: invoke.ModeRun,
		Model: "anthropic/sonnet", Agent: "reviewer",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"--model", "anthropic/sonnet"}) {
		t.Errorf("missing --model: %v", spec.Args)
	}
	if !containsSubslice(spec.Args, []string{"--agent", "reviewer"}) {
		t.Errorf("missing --agent: %v", spec.Args)
	}
}

func TestBuildConfigKeys(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIOpenCode, Mode: invoke.ModeRun,
		Config: map[string]string{
			"opencode.variant":  "high",
			"opencode.thinking": "true",
			"opencode.share":    "true",
			"opencode.title":    "QA review",
		},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"--variant", "high"}) {
		t.Errorf("missing --variant high: %v", spec.Args)
	}
	if !slices.Contains(spec.Args, "--thinking") {
		t.Errorf("missing --thinking: %v", spec.Args)
	}
	if !slices.Contains(spec.Args, "--share") {
		t.Errorf("missing --share: %v", spec.Args)
	}
	if !containsSubslice(spec.Args, []string{"--title", "QA review"}) {
		t.Errorf("missing --title: %v", spec.Args)
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

func TestAtoiDefault(t *testing.T) {
	t.Parallel()
	cases := []struct {
		s    string
		def  int
		want int
	}{
		{"100", 50, 100},
		{"0", 50, 50},
		{"", 50, 50},
		{"abc", 50, 50},
		{"  200  ", 50, 200},
	}
	for _, tc := range cases {
		got := atoiDefault(tc.s, tc.def)
		if got != tc.want {
			t.Errorf("atoiDefault(%q, %d) = %d, want %d", tc.s, tc.def, got, tc.want)
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
