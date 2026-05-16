package uxpcmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func runRoot(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := Cmd()
	var so, se bytes.Buffer
	cmd.SetOut(&so)
	cmd.SetErr(&se)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return so.String(), se.String(), err
}

func TestRunPrintsArgvByDefault(t *testing.T) {
	t.Parallel()
	out, _, err := runRoot(t, "run", "--tool", "claude", "hello world")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.HasPrefix(out, "claude") {
		t.Errorf("expected stdout to start with 'claude '; got %q", out)
	}
	if !strings.Contains(out, "-p") {
		t.Errorf("expected -p flag in argv; got %q", out)
	}
}

func TestRunRefusesUnknownTool(t *testing.T) {
	t.Parallel()
	_, _, err := runRoot(t, "run", "--tool", "bogus", "hi")
	if err == nil {
		t.Error("expected error for unknown --tool")
	}
}

func TestRunMissingToolFlag(t *testing.T) {
	t.Parallel()
	_, _, err := runRoot(t, "run", "hi")
	if err == nil {
		t.Error("expected error for missing --tool")
	}
}

func TestRunWithApprovalAndAllowDangerous(t *testing.T) {
	t.Parallel()
	out, _, err := runRoot(t,
		"run", "--tool", "claude",
		"--approval", "auto-all",
		"--allow-dangerous",
		"hi",
	)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(out, "bypassPermissions") {
		t.Errorf("expected bypassPermissions in argv; got %q", out)
	}
}

func TestRunRefusesApprovalWithoutDangerOptIn(t *testing.T) {
	t.Parallel()
	_, _, err := runRoot(t,
		"run", "--tool", "claude",
		"--approval", "auto-all",
		"hi",
	)
	if err == nil {
		t.Error("expected error: AutoAll without --allow-dangerous")
	}
}

func TestResumeRequiresIDOrContinue(t *testing.T) {
	t.Parallel()
	_, _, err := runRoot(t, "resume", "--tool", "claude")
	if err == nil {
		t.Error("expected error: resume needs --session or --continue")
	}
}

func TestResumeWithSession(t *testing.T) {
	t.Parallel()
	out, _, err := runRoot(t,
		"resume", "--tool", "claude",
		"--session", "abc-123",
	)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(out, "--resume") || !strings.Contains(out, "abc-123") {
		t.Errorf("expected --resume abc-123 in argv: %q", out)
	}
}

func TestResumeContinue(t *testing.T) {
	t.Parallel()
	out, _, err := runRoot(t,
		"resume", "--tool", "claude",
		"--continue",
	)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(out, "--continue") {
		t.Errorf("expected --continue in argv: %q", out)
	}
}

func TestExplainText(t *testing.T) {
	t.Parallel()
	out, _, err := runRoot(t, "explain", "--tool", "gemini")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{"ModeRun", "Fork", "Files"} {
		if !strings.Contains(out, want) {
			t.Errorf("explain output missing %q: %q", want, out)
		}
	}
}

func TestExplainSingleOption(t *testing.T) {
	t.Parallel()
	out, _, err := runRoot(t,
		"explain", "--tool", "codex",
		"--option", "ApprovalAutoEdit",
	)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(out, "ApprovalAutoEdit") {
		t.Errorf("expected ApprovalAutoEdit in output: %q", out)
	}
	if !strings.Contains(out, "unsupported") {
		t.Errorf("expected 'unsupported' in output: %q", out)
	}
}

func TestExplainJSON(t *testing.T) {
	t.Parallel()
	out, _, err := runRoot(t,
		"explain", "--tool", "claude",
		"--format", "json",
	)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	var payload struct {
		Tool     string           `json:"tool"`
		Mappings []map[string]any `json:"mappings"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if payload.Tool != "claude" {
		t.Errorf("Tool = %q, want claude", payload.Tool)
	}
	if len(payload.Mappings) < 20 {
		t.Errorf("expected ≥20 mappings; got %d", len(payload.Mappings))
	}
}

func TestCapabilitiesText(t *testing.T) {
	t.Parallel()
	out, _, err := runRoot(t, "capabilities", "--tool", "opencode")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(out, "Mappings:") || !strings.Contains(out, "ToolCapabilities:") {
		t.Errorf("expected both sections; got %q", out)
	}
}

func TestCapabilitiesJSON(t *testing.T) {
	t.Parallel()
	out, _, err := runRoot(t, "capabilities", "--tool", "opencode", "--format", "json")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	var payload struct {
		CLI              string           `json:"cli"`
		Mappings         []map[string]any `json:"mappings"`
		ToolCapabilities []map[string]any `json:"tool_capabilities"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if payload.CLI != "opencode" {
		t.Errorf("CLI = %q", payload.CLI)
	}
	if len(payload.Mappings) == 0 || len(payload.ToolCapabilities) == 0 {
		t.Error("expected non-empty mappings and tool_capabilities")
	}
}

func TestToolsList(t *testing.T) {
	t.Parallel()
	out, _, err := runRoot(t, "tools", "--tool", "claude")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{"shell.exec", "file.read"} {
		if !strings.Contains(out, want) {
			t.Errorf("tools output missing %q", want)
		}
	}
}

func TestToolsMap(t *testing.T) {
	t.Parallel()
	out, _, err := runRoot(t,
		"tools", "map",
		"--from", "claude",
		"--to", "crush",
	)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(out, "Tool handoff claude → crush") {
		t.Errorf("expected handoff banner: %q", out)
	}
	if !strings.Contains(out, "shell.exec") {
		t.Errorf("expected shell.exec row: %q", out)
	}
}

func TestToolsMapJSON(t *testing.T) {
	t.Parallel()
	out, _, err := runRoot(t,
		"tools", "map",
		"--from", "claude", "--to", "crush",
		"--format", "json",
	)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	var payload struct {
		From string       `json:"from"`
		To   string       `json:"to"`
		Rows []toolMapRow `json:"rows"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if payload.From != "claude" || payload.To != "crush" {
		t.Errorf("from/to wrong: %+v", payload)
	}
	if len(payload.Rows) == 0 {
		t.Error("expected non-empty rows")
	}
}

func TestRunWithConfigKVs(t *testing.T) {
	t.Parallel()
	out, _, err := runRoot(t,
		"run", "--tool", "claude",
		"--config", "claude.system_prompt=be concise",
		"--config", "claude.allowed_tools=Bash,Read",
		"hi",
	)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(out, "be concise") {
		t.Errorf("expected --system-prompt value: %q", out)
	}
}

func TestNeedsQuote(t *testing.T) {
	t.Parallel()
	cases := []struct {
		s    string
		want bool
	}{
		{"plain", false},
		{"with space", true},
		{"with$dollar", true},
		{"", true},
		{"path/to/file", false},
	}
	for _, tc := range cases {
		if got := needsQuote(tc.s); got != tc.want {
			t.Errorf("needsQuote(%q) = %v, want %v", tc.s, got, tc.want)
		}
	}
}
