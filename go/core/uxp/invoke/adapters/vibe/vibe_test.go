package vibe

import (
	"slices"
	"testing"

	"hop.top/kit/go/core/uxp"
	"hop.top/kit/go/core/uxp/invoke"
)

func TestAdapterCLI(t *testing.T) {
	t.Parallel()
	if got := New().CLI(); got != uxp.CLIVibe {
		t.Errorf("CLI() = %q, want %q", got, uxp.CLIVibe)
	}
}

func TestBuildModeRunUsesP(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIVibe, Mode: invoke.ModeRun, Prompt: "hi",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"-p", "hi"}) {
		t.Errorf("missing -p hi: %v", spec.Args)
	}
}

func TestBuildResume(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIVibe, Mode: invoke.ModeResume, SessionID: "abc",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"--resume", "abc"}) {
		t.Errorf("missing --resume abc: %v", spec.Args)
	}
}

func TestBuildContinue(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIVibe, Mode: invoke.ModeResume, Continue: true,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !slices.Contains(spec.Args, "-c") {
		t.Errorf("missing -c: %v", spec.Args)
	}
}

func TestBuildModelRefused(t *testing.T) {
	t.Parallel()
	_, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIVibe, Mode: invoke.ModeRun, Model: "x",
	})
	if err == nil {
		t.Error("expected error: vibe has no --model")
	}
}

func TestBuildForkRefused(t *testing.T) {
	t.Parallel()
	_, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIVibe, Mode: invoke.ModeRun, Fork: true,
	})
	if err == nil {
		t.Error("expected error: vibe has no fork")
	}
}

func TestBuildApprovalShimsViaAgent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		a    invoke.ApprovalMode
		want string
	}{
		{invoke.ApprovalPlan, "plan"},
		{invoke.ApprovalAutoEdit, "accept-edits"},
	}
	for _, tc := range cases {
		t.Run(string(tc.a), func(t *testing.T) {
			spec, ds, err := New().Build(invoke.Invocation{
				CLI: uxp.CLIVibe, Mode: invoke.ModeRun, Approval: tc.a,
			})
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if !containsSubslice(spec.Args, []string{"--agent", tc.want}) {
				t.Errorf("expected --agent %s; got %v", tc.want, spec.Args)
			}
			hasShimDiag := false
			for _, d := range ds {
				if d.Option == "Approval" && d.Level == "warning" {
					hasShimDiag = true
				}
			}
			if !hasShimDiag {
				t.Errorf("expected Approval shim warning: %+v", ds)
			}
		})
	}
}

func TestBuildAgentAndApprovalConflictRefused(t *testing.T) {
	t.Parallel()
	_, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIVibe, Mode: invoke.ModeRun,
		Agent: "custom", Approval: invoke.ApprovalPlan,
	})
	if err == nil {
		t.Error("expected error when Agent and Approval both want --agent")
	}
}

func TestBuildAgentWithoutApproval(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIVibe, Mode: invoke.ModeRun, Agent: "custom",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"--agent", "custom"}) {
		t.Errorf("missing --agent custom: %v", spec.Args)
	}
}

func TestBuildApprovalAutoAllOptIn(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIVibe, Mode: invoke.ModeRun,
		Approval: invoke.ApprovalAutoAll,
		Config:   map[string]string{"uxp.allow_dangerous": "true"},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"--agent", "auto-approve"}) {
		t.Errorf("missing --agent auto-approve: %v", spec.Args)
	}
}

func TestBuildOutputFormats(t *testing.T) {
	t.Parallel()
	cases := []struct {
		out  invoke.OutputFormat
		flag string
	}{
		{invoke.OutputJSON, "json"},
		{invoke.OutputStreamJSON, "streaming"},
	}
	for _, tc := range cases {
		t.Run(string(tc.out), func(t *testing.T) {
			spec, _, err := New().Build(invoke.Invocation{
				CLI: uxp.CLIVibe, Mode: invoke.ModeRun, Output: tc.out,
			})
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if !containsSubslice(spec.Args, []string{"--output", tc.flag}) {
				t.Errorf("missing --output %s: %v", tc.flag, spec.Args)
			}
		})
	}
}

func TestBuildSandboxRefused(t *testing.T) {
	t.Parallel()
	_, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIVibe, Mode: invoke.ModeRun, Sandbox: invoke.SandboxReadOnly,
	})
	if err == nil {
		t.Error("expected error: vibe has no sandbox")
	}
}

func TestBuildCWDViaWorkdir(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIVibe, Mode: invoke.ModeRun, CWD: "/repo",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"--workdir", "/repo"}) {
		t.Errorf("missing --workdir /repo: %v", spec.Args)
	}
}

func TestBuildConfigKeys(t *testing.T) {
	t.Parallel()
	spec, _, err := New().Build(invoke.Invocation{
		CLI: uxp.CLIVibe, Mode: invoke.ModeRun,
		Config: map[string]string{
			"vibe.max_turns":     "10",
			"vibe.max_price":     "5.00",
			"vibe.enabled_tools": "bash*,read,re:edit_.*",
			"vibe.trust":         "true",
		},
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !containsSubslice(spec.Args, []string{"--max-turns", "10"}) {
		t.Errorf("missing --max-turns 10: %v", spec.Args)
	}
	if !containsSubslice(spec.Args, []string{"--max-price", "5.00"}) {
		t.Errorf("missing --max-price: %v", spec.Args)
	}
	enabledCount := 0
	for i, a := range spec.Args {
		if a == "--enabled-tools" && i+1 < len(spec.Args) {
			enabledCount++
		}
	}
	if enabledCount != 3 {
		t.Errorf("expected 3 --enabled-tools; got %d in %v", enabledCount, spec.Args)
	}
	if !slices.Contains(spec.Args, "--trust") {
		t.Errorf("missing --trust: %v", spec.Args)
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
