package cursoragent

import (
	"slices"
	"strings"
	"testing"

	"hop.top/kit/go/core/uxp"
	"hop.top/kit/go/core/uxp/invoke"
)

func TestAdapterCLI(t *testing.T) {
	t.Parallel()
	if got := New().CLI(); got != uxp.CLICursorAgent {
		t.Errorf("CLI() = %q, want %q", got, uxp.CLICursorAgent)
	}
}

func TestBuildModeRunPrint(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICursorAgent, Mode: invoke.ModeRun, Prompt: "hi",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !slices.Contains(spec.Args, "-p") {
		t.Errorf("missing -p: %v", spec.Args)
	}
}

func TestBuildResumeWithID(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICursorAgent, Mode: invoke.ModeResume, SessionID: "abc",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"--resume", "abc"}) {
		t.Errorf("missing --resume abc: %v", spec.Args)
	}
}

func TestBuildContinueUsesResumeSubcommand(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICursorAgent, Mode: invoke.ModeResume, Continue: true,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if spec.Args[0] != "resume" {
		t.Errorf("Continue should use resume subcommand; got %v", spec.Args)
	}
}

func TestBuildResumeNeedsIDOrContinue(t *testing.T) {
	t.Parallel()
	_, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICursorAgent, Mode: invoke.ModeResume,
	})
	if err == nil {
		t.Error("expected error for resume without id/continue")
	}
}

func TestBuildForkRefused(t *testing.T) {
	t.Parallel()
	_, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICursorAgent, Mode: invoke.ModeRun, Fork: true,
	})
	if err == nil {
		t.Error("expected error: cursor-agent has no fork")
	}
}

func TestBuildAgentRefused(t *testing.T) {
	t.Parallel()
	_, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICursorAgent, Mode: invoke.ModeRun, Agent: "x",
	})
	if err == nil {
		t.Error("expected error: cursor-agent has no --agent")
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
				CLI: uxp.CLICursorAgent, Mode: invoke.ModeRun, Output: tc.out,
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

func TestBuildOutputJSONInteractiveRefused(t *testing.T) {
	t.Parallel()
	_, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICursorAgent, Mode: invoke.ModeInteractive, Output: invoke.OutputJSON,
	})
	if err == nil {
		t.Error("expected error: OutputJSON requires --print")
	}
}

func TestBuildSandboxTiersRefused(t *testing.T) {
	t.Parallel()
	for _, s := range []invoke.SandboxMode{invoke.SandboxReadOnly, invoke.SandboxWorkspaceWrite} {
		_, _, err := New().Build(invoke.Invocation{
			CLI: uxp.CLICursorAgent, Mode: invoke.ModeRun, Sandbox: s,
		})
		if err == nil {
			t.Errorf("expected error for Sandbox=%s on cursor-agent", s)
		}
	}
}

func TestBuildApprovalAutoEditRefused(t *testing.T) {
	t.Parallel()
	_, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICursorAgent, Mode: invoke.ModeRun, Approval: invoke.ApprovalAutoEdit,
	})
	if err == nil {
		t.Error("expected error: ApprovalAutoEdit refused on cursor-agent")
	}
}

func TestBuildApprovalAutoAllOptIn(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICursorAgent, Mode: invoke.ModeRun,
		Approval: invoke.ApprovalAutoAll,
		Config:   map[string]string{"uxp.allow_dangerous": "true"},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !slices.Contains(spec.Args, "-f") {
		t.Errorf("missing -f: %v", spec.Args)
	}
}

func TestBuildSandboxAndApprovalDontDoublyEmitForce(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICursorAgent, Mode: invoke.ModeRun,
		Sandbox:  invoke.SandboxDangerFullAccess,
		Approval: invoke.ApprovalAutoAll,
		Config:   map[string]string{"uxp.allow_dangerous": "true"},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	count := 0
	for _, a := range spec.Args {
		if a == "-f" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected -f exactly once; got %d in %v", count, spec.Args)
	}
}

func TestBuildPromptBlockForFilesAddDirsImages(t *testing.T) {
	t.Parallel()
	spec, ds, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICursorAgent, Mode: invoke.ModeRun,
		AddDirs: []string{"/repo/pkg"},
		Files:   []string{"/repo/pkg/a.go"},
		Images:  []string{"shot.png"},
		Prompt:  "explain",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	last := spec.Args[len(spec.Args)-1]
	if !strings.Contains(last, "/repo/pkg") {
		t.Errorf("AddDirs not in prompt: %q", last)
	}
	if !strings.Contains(last, "a.go") {
		t.Errorf("Files not in prompt: %q", last)
	}
	if !strings.Contains(last, "shot.png") {
		t.Errorf("Images not in prompt: %q", last)
	}
	if !strings.Contains(last, "explain") {
		t.Errorf("original prompt missing: %q", last)
	}
	// All three diagnostics should be present.
	wantOptions := map[string]bool{"AddDirs": false, "Files": false, "Images": false}
	for _, d := range ds {
		if d.Level == "warning" {
			wantOptions[d.Option] = true
		}
	}
	for opt, seen := range wantOptions {
		if !seen {
			t.Errorf("missing warning diagnostic for %s: %+v", opt, ds)
		}
	}
}

func TestBuildConfigKeys(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLICursorAgent, Mode: invoke.ModeRun,
		Config: map[string]string{
			"cursor.api_key":               "sk-x",
			"cursor.background":            "true",
			"cursor.stream_partial_output": "true",
			"cursor.unknown":               "x",
		},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"--api-key", "sk-x"}) {
		t.Errorf("missing --api-key: %v", spec.Args)
	}
	if !slices.Contains(spec.Args, "-b") {
		t.Errorf("missing -b: %v", spec.Args)
	}
	if !slices.Contains(spec.Args, "--stream-partial-output") {
		t.Errorf("missing --stream-partial-output: %v", spec.Args)
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
