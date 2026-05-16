package claude

import (
	"slices"
	"strings"
	"testing"

	"hop.top/kit/go/core/uxp"
	"hop.top/kit/go/core/uxp/invoke"
)

func TestAdapterCLI(t *testing.T) {
	t.Parallel()
	if got := New().CLI(); got != uxp.CLIClaude {
		t.Errorf("CLI() = %q, want %q", got, uxp.CLIClaude)
	}
}

func TestBuildModeRunBasic(t *testing.T) {
	t.Parallel()
	spec, ds, err := New().Build(invoke.Invocation{
		CLI:    uxp.CLIClaude,
		Mode:   invoke.ModeRun,
		Prompt: "hello world",
	})
	if err != nil {
		t.Fatalf("Build err = %v", err)
	}
	if ds.HasErrors() {
		t.Errorf("unexpected error diagnostics: %+v", ds)
	}
	if spec.Path != Binary {
		t.Errorf("Path = %q, want %q", spec.Path, Binary)
	}
	if !slices.Contains(spec.Args, "-p") {
		t.Errorf("ModeRun missing -p flag: %v", spec.Args)
	}
	if spec.Args[len(spec.Args)-1] != "hello world" {
		t.Errorf("prompt should be last arg; got %v", spec.Args)
	}
}

func TestBuildModeInteractiveNoFlags(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI:  uxp.CLIClaude,
		Mode: invoke.ModeInteractive,
	})
	if err != nil {
		t.Fatalf("Build err = %v", err)
	}
	if slices.Contains(spec.Args, "-p") {
		t.Errorf("ModeInteractive should not include -p: %v", spec.Args)
	}
}

func TestBuildModeResumeRequiresIDOrContinue(t *testing.T) {
	t.Parallel()
	_, _, err := New().Build(invoke.Invocation{
		CLI:  uxp.CLIClaude,
		Mode: invoke.ModeResume,
	})
	if err == nil {
		t.Error("expected error when ModeResume has no SessionID and no Continue")
	}
}

func TestBuildModeResumeWithSession(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI:       uxp.CLIClaude,
		Mode:      invoke.ModeResume,
		SessionID: "abc-123",
	})
	if err != nil {
		t.Fatalf("Build err = %v", err)
	}
	wantPair := []string{"--resume", "abc-123"}
	if !containsSubslice(spec.Args, wantPair) {
		t.Errorf("args missing %v: %v", wantPair, spec.Args)
	}
}

func TestBuildModeResumeContinue(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI:      uxp.CLIClaude,
		Mode:     invoke.ModeResume,
		Continue: true,
	})
	if err != nil {
		t.Fatalf("Build err = %v", err)
	}
	if !slices.Contains(spec.Args, "--continue") {
		t.Errorf("args missing --continue: %v", spec.Args)
	}
}

func TestBuildModeResumeFork(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI:       uxp.CLIClaude,
		Mode:      invoke.ModeResume,
		SessionID: "x",
		Fork:      true,
	})
	if err != nil {
		t.Fatalf("Build err = %v", err)
	}
	if !slices.Contains(spec.Args, "--fork-session") {
		t.Errorf("args missing --fork-session: %v", spec.Args)
	}
}

func TestBuildForkRequiresResume(t *testing.T) {
	t.Parallel()
	_, _, err := New().Build(invoke.Invocation{
		CLI:  uxp.CLIClaude,
		Mode: invoke.ModeRun,
		Fork: true,
	})
	if err == nil {
		t.Error("expected error when Fork=true outside ModeResume")
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
				CLI:    uxp.CLIClaude,
				Mode:   invoke.ModeRun,
				Output: tc.out,
				Prompt: "x",
			})
			if err != nil {
				t.Fatalf("Build err = %v", err)
			}
			if !containsSubslice(spec.Args, []string{"--output-format", tc.flag}) {
				t.Errorf("args missing --output-format %s: %v", tc.flag, spec.Args)
			}
		})
	}
}

func TestBuildOutputJSONInInteractiveRefused(t *testing.T) {
	t.Parallel()
	_, _, err := New().Build(invoke.Invocation{
		CLI:    uxp.CLIClaude,
		Mode:   invoke.ModeInteractive,
		Output: invoke.OutputJSON,
	})
	if err == nil {
		t.Error("expected error when OutputJSON used with ModeInteractive")
	}
}

func TestBuildApprovalNative(t *testing.T) {
	t.Parallel()
	cases := []struct {
		a    invoke.ApprovalMode
		mode string
	}{
		{invoke.ApprovalAutoEdit, "acceptEdits"},
		{invoke.ApprovalPlan, "plan"},
		{invoke.ApprovalNever, "dontAsk"},
	}
	for _, tc := range cases {
		t.Run(string(tc.a), func(t *testing.T) {
			spec, _, err := New().Build(invoke.Invocation{
				CLI:      uxp.CLIClaude,
				Mode:     invoke.ModeRun,
				Approval: tc.a,
				Prompt:   "x",
			})
			if err != nil {
				t.Fatalf("Build err = %v", err)
			}
			if !containsSubslice(spec.Args, []string{"--permission-mode", tc.mode}) {
				t.Errorf("args missing --permission-mode %s: %v", tc.mode, spec.Args)
			}
		})
	}
}

func TestBuildApprovalAutoAllRefusedWithoutOptIn(t *testing.T) {
	t.Parallel()
	_, ds, err := New().Build(invoke.Invocation{
		CLI:      uxp.CLIClaude,
		Mode:     invoke.ModeRun,
		Approval: invoke.ApprovalAutoAll,
	})
	if err == nil {
		t.Error("expected error for ApprovalAutoAll without uxp.allow_dangerous opt-in")
	}
	if !ds.HasErrors() {
		t.Error("expected error-level diagnostic")
	}
	if errs := ds.Errors(); len(errs) != 1 || errs[0].Option != "Approval" {
		t.Errorf("expected one Approval-level error diagnostic; got %+v", ds)
	}
}

func TestBuildApprovalAutoAllAllowedWithOptIn(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI:      uxp.CLIClaude,
		Mode:     invoke.ModeRun,
		Approval: invoke.ApprovalAutoAll,
		Config:   map[string]string{"uxp.allow_dangerous": "true"},
	})
	if err != nil {
		t.Fatalf("Build err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"--permission-mode", "bypassPermissions"}) {
		t.Errorf("args missing bypassPermissions: %v", spec.Args)
	}
}

func TestBuildSandboxDangerFullAccessRefused(t *testing.T) {
	t.Parallel()
	_, _, err := New().Build(invoke.Invocation{
		CLI:     uxp.CLIClaude,
		Mode:    invoke.ModeRun,
		Sandbox: invoke.SandboxDangerFullAccess,
	})
	if err == nil {
		t.Error("expected error for SandboxDangerFullAccess without opt-in")
	}
}

func TestBuildAddDirsVariadic(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI:     uxp.CLIClaude,
		Mode:    invoke.ModeRun,
		AddDirs: []string{"/a", "/b/c"},
	})
	if err != nil {
		t.Fatalf("Build err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"--add-dir", "/a", "/b/c"}) {
		t.Errorf("args missing --add-dir /a /b/c: %v", spec.Args)
	}
}

func TestBuildFilesShimToParentDirsAndPromptBlock(t *testing.T) {
	t.Parallel()
	spec, ds, err := New().Build(invoke.Invocation{
		CLI:    uxp.CLIClaude,
		Mode:   invoke.ModeRun,
		Files:  []string{"pkg/a/x.go", "pkg/a/y.go", "pkg/b/z.go"},
		Prompt: "review",
	})
	if err != nil {
		t.Fatalf("Build err = %v", err)
	}
	// Parent dirs (S-1).
	if !containsAny(spec.Args, "pkg/a") {
		t.Errorf("args missing parent dir pkg/a: %v", spec.Args)
	}
	if !containsAny(spec.Args, "pkg/b") {
		t.Errorf("args missing parent dir pkg/b: %v", spec.Args)
	}
	// Prompt block (S-3): the prompt should be prefixed with the file
	// list. The composed prompt is the *last* arg.
	last := spec.Args[len(spec.Args)-1]
	if !strings.Contains(last, "pkg/a/x.go") {
		t.Errorf("prompt block missing pkg/a/x.go: %q", last)
	}
	if !strings.Contains(last, "review") {
		t.Errorf("original prompt lost: %q", last)
	}
	// Files diagnostic.
	want := false
	for _, d := range ds {
		if d.Option == "Files" && d.Level == "warning" {
			want = true
			break
		}
	}
	if !want {
		t.Errorf("expected warning diagnostic for Files; got %+v", ds)
	}
}

func TestBuildImagesPromptBlock(t *testing.T) {
	t.Parallel()
	spec, ds, err := New().Build(invoke.Invocation{
		CLI:    uxp.CLIClaude,
		Mode:   invoke.ModeRun,
		Images: []string{"a.png"},
		Prompt: "describe",
	})
	if err != nil {
		t.Fatalf("Build err = %v", err)
	}
	last := spec.Args[len(spec.Args)-1]
	if !strings.Contains(last, "a.png") {
		t.Errorf("image filename missing from prompt block: %q", last)
	}
	if !ds.Filter("warning").Filter("warning").HasErrors() && len(ds.Filter("warning")) == 0 {
		t.Errorf("expected warning diagnostic for Images; got %+v", ds)
	}
}

func TestBuildModelAndAgent(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI:   uxp.CLIClaude,
		Mode:  invoke.ModeRun,
		Model: "sonnet",
		Agent: "reviewer",
	})
	if err != nil {
		t.Fatalf("Build err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"--model", "sonnet"}) {
		t.Errorf("args missing --model sonnet: %v", spec.Args)
	}
	if !containsSubslice(spec.Args, []string{"--agent", "reviewer"}) {
		t.Errorf("args missing --agent reviewer: %v", spec.Args)
	}
}

func TestBuildCWDViaSpecDir(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI:  uxp.CLIClaude,
		Mode: invoke.ModeRun,
		CWD:  "/repo",
	})
	if err != nil {
		t.Fatalf("Build err = %v", err)
	}
	if spec.Dir != "/repo" {
		t.Errorf("Dir = %q, want /repo", spec.Dir)
	}
	// claude has no --cd flag — confirm we did not invent one.
	if slices.Contains(spec.Args, "--cd") {
		t.Errorf("unexpected --cd flag: %v", spec.Args)
	}
}

func TestBuildConfigKeys(t *testing.T) {
	t.Parallel()
	spec, ds, err := New().Build(invoke.Invocation{
		CLI:  uxp.CLIClaude,
		Mode: invoke.ModeRun,
		Config: map[string]string{
			"claude.system_prompt": "be concise",
			"claude.allowed_tools": "Bash,Read,Edit",
			"claude.unknown":       "x",
			"unrelated.key":        "y",
			"uxp.allow_dangerous":  "false", // not consumed unless dangerous mapping triggers
		},
	})
	if err != nil {
		t.Fatalf("Build err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"--system-prompt", "be concise"}) {
		t.Errorf("args missing --system-prompt: %v", spec.Args)
	}
	allowedCount := 0
	for i, a := range spec.Args {
		if a == "--allowedTools" && i+1 < len(spec.Args) {
			allowedCount++
		}
	}
	if allowedCount != 3 {
		t.Errorf("expected 3 --allowedTools flags; got %d in %v", allowedCount, spec.Args)
	}
	// unknown claude.* key emits info diagnostic; unrelated.* key does not.
	infoCount := 0
	for _, d := range ds {
		if d.Level == "info" && d.Option == "Config" {
			infoCount++
		}
	}
	if infoCount != 1 {
		t.Errorf("expected 1 info diagnostic for unknown claude key; got %d in %+v", infoCount, ds)
	}
}

func TestMappingsCompletes(t *testing.T) {
	t.Parallel()
	m := New().Mappings()
	if len(m) == 0 {
		t.Fatal("Mappings empty")
	}
	wantOptions := []string{
		"ModeRun", "ModeInteractive", "ModeResume", "Continue", "Fork",
		"CWD", "Model", "Agent",
		"OutputText", "OutputJSON", "OutputStreamJSON",
		"SandboxReadOnly", "SandboxWorkspaceWrite", "SandboxDangerFullAccess",
		"ApprovalPlan", "ApprovalAutoEdit", "ApprovalAutoAll", "ApprovalAsk", "ApprovalNever",
		"AddDirs", "Files", "Images",
	}
	have := map[string]bool{}
	for _, om := range m {
		have[om.Universal] = true
	}
	for _, w := range wantOptions {
		if !have[w] {
			t.Errorf("Mappings missing universal option %q", w)
		}
	}
}

func TestToolCapabilitiesIncludesCore(t *testing.T) {
	t.Parallel()
	tc := New().ToolCapabilities()
	if len(tc) == 0 {
		t.Fatal("ToolCapabilities empty")
	}
	wantUniversals := []string{
		"shell.exec", "file.read", "file.write", "file.edit",
		"file.search", "web.search", "web.fetch", "todo.write",
		"task.spawn", "mcp.call",
	}
	have := map[string]bool{}
	for _, t := range tc {
		have[t.Universal] = true
	}
	for _, w := range wantUniversals {
		if !have[w] {
			t.Errorf("ToolCapabilities missing %q", w)
		}
	}
}

func TestConfigBoolHelper(t *testing.T) {
	t.Parallel()
	cases := []struct {
		v    string
		want bool
	}{
		{"true", true}, {"True", true}, {"YES", true}, {"1", true}, {"on", true},
		{"false", false}, {"0", false}, {"", false}, {"random", false},
	}
	for _, tc := range cases {
		got := configBool(map[string]string{"k": tc.v}, "k")
		if got != tc.want {
			t.Errorf("configBool(%q) = %v, want %v", tc.v, got, tc.want)
		}
	}
	if configBool(nil, "k") {
		t.Error("nil map should yield false")
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

func containsAny(haystack []string, needle string) bool {
	return slices.Contains(haystack, needle)
}
