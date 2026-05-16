package invoke

import (
	"context"
	"testing"

	"hop.top/kit/go/core/uxp"
)

func TestModeValid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		m    Mode
		want bool
	}{
		{ModeInteractive, true},
		{ModeRun, true},
		{ModeResume, true},
		{Mode(""), false},
		{Mode("unknown"), false},
	}
	for _, tc := range cases {
		if got := tc.m.Valid(); got != tc.want {
			t.Errorf("Mode(%q).Valid() = %v, want %v", tc.m, got, tc.want)
		}
		if string(tc.m) != tc.m.String() {
			t.Errorf("Mode(%q).String() round-trip mismatch", tc.m)
		}
	}
}

func TestOutputFormatValid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		f    OutputFormat
		want bool
	}{
		{OutputDefault, true},
		{OutputText, true},
		{OutputJSON, true},
		{OutputStreamJSON, true},
		{OutputFormat("ndjson"), false},
	}
	for _, tc := range cases {
		if got := tc.f.Valid(); got != tc.want {
			t.Errorf("OutputFormat(%q).Valid() = %v, want %v", tc.f, got, tc.want)
		}
	}
}

func TestSandboxModeValid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		s    SandboxMode
		want bool
	}{
		{SandboxDefault, true},
		{SandboxReadOnly, true},
		{SandboxWorkspaceWrite, true},
		{SandboxDangerFullAccess, true},
		{SandboxMode("yolo"), false},
	}
	for _, tc := range cases {
		if got := tc.s.Valid(); got != tc.want {
			t.Errorf("SandboxMode(%q).Valid() = %v, want %v", tc.s, got, tc.want)
		}
	}
}

func TestApprovalModeValid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		a    ApprovalMode
		want bool
	}{
		{ApprovalDefault, true},
		{ApprovalAsk, true},
		{ApprovalAutoEdit, true},
		{ApprovalAutoAll, true},
		{ApprovalPlan, true},
		{ApprovalNever, true},
		{ApprovalMode("acceptEdits"), false}, // claude-native, not universal
	}
	for _, tc := range cases {
		if got := tc.a.Valid(); got != tc.want {
			t.Errorf("ApprovalMode(%q).Valid() = %v, want %v", tc.a, got, tc.want)
		}
	}
}

func TestDiagnosticsAddAndFilter(t *testing.T) {
	t.Parallel()
	var ds Diagnostics
	ds.Add(Diagnostic{Level: "info", Option: "Config", Message: "unknown key"})
	ds.Add(Diagnostic{Level: "warning", Option: "Files", Message: "shimmed via parent dirs"})
	ds.Add(Diagnostic{Level: "error", Option: "Approval", Message: "auto-edit unsupported"})
	ds.Add(Diagnostic{Level: "warning", Option: "Output", Message: "JSON shim"})

	if got := len(ds); got != 4 {
		t.Fatalf("len(ds) = %d, want 4", got)
	}

	infos := ds.Filter("info")
	if len(infos) != 1 || infos[0].Option != "Config" {
		t.Errorf("Filter(info) = %+v, want one Config entry", infos)
	}

	warnings := ds.Filter("warning")
	if len(warnings) != 2 {
		t.Errorf("Filter(warning) = %d entries, want 2", len(warnings))
	}
	if warnings[0].Option != "Files" || warnings[1].Option != "Output" {
		t.Errorf("Filter preserves order: got %+v", warnings)
	}

	errs := ds.Errors()
	if len(errs) != 1 || errs[0].Option != "Approval" {
		t.Errorf("Errors() = %+v, want one Approval entry", errs)
	}

	if !ds.HasErrors() {
		t.Error("HasErrors() = false, want true")
	}

	clean := Diagnostics{
		{Level: "info", Option: "Config", Message: "ok"},
	}
	if clean.HasErrors() {
		t.Error("HasErrors() on info-only Diagnostics = true, want false")
	}
}

func TestDiagnosticsFilterEmpty(t *testing.T) {
	t.Parallel()
	var ds Diagnostics
	if got := ds.Errors(); len(got) != 0 {
		t.Errorf("nil Diagnostics.Errors() len = %d, want 0", len(got))
	}
	if ds.HasErrors() {
		t.Error("nil Diagnostics.HasErrors() = true, want false")
	}
}

func TestMappingSupportConstants(t *testing.T) {
	t.Parallel()
	for _, m := range []MappingSupport{
		MappingNative, MappingShim, MappingUnsupported, MappingDangerous,
	} {
		if string(m) == "" {
			t.Errorf("MappingSupport %q is empty", m)
		}
	}
}

func TestToolPermissionConstants(t *testing.T) {
	t.Parallel()
	for _, p := range []ToolPermission{
		ToolRead, ToolWrite, ToolExec, ToolNetwork, ToolBrowser, ToolTask,
	} {
		if string(p) == "" {
			t.Errorf("ToolPermission %q is empty", p)
		}
	}
}

func TestTranscriptSupportConstants(t *testing.T) {
	t.Parallel()
	for _, ts := range []TranscriptSupport{
		TranscriptNative, TranscriptPartial, TranscriptUnavailable,
	} {
		if string(ts) == "" {
			t.Errorf("TranscriptSupport %q is empty", ts)
		}
	}
}

// stubAdapter exists only to confirm InvocationAdapter is satisfiable
// with all five methods. No adapter implementations live in this
// package — they go under invoke/adapters/<cli>/ per spec §16.1.
type stubAdapter struct{ name uxp.CLIName }

func (s stubAdapter) CLI() uxp.CLIName { return s.name }
func (s stubAdapter) Build(_ Invocation) (CommandSpec, Diagnostics, error) {
	return CommandSpec{Path: string(s.name)}, nil, nil
}
func (s stubAdapter) Mappings() []OptionMapping          { return nil }
func (s stubAdapter) ToolCapabilities() []ToolCapability { return nil }

// stubRunner asserts Runner is satisfiable.
type stubRunner struct{}

func (stubRunner) Run(_ context.Context, _ Invocation) (Result, Diagnostics, error) {
	return Result{}, nil, nil
}

func TestInvocationAdapterCompiles(t *testing.T) {
	t.Parallel()
	var a InvocationAdapter = stubAdapter{name: "claude"}
	if a.CLI() != "claude" {
		t.Errorf("stubAdapter.CLI() = %q, want claude", a.CLI())
	}
	spec, ds, err := a.Build(Invocation{CLI: "claude", Mode: ModeRun})
	if err != nil {
		t.Errorf("stub Build err = %v, want nil", err)
	}
	if spec.Path != "claude" {
		t.Errorf("stub Build Path = %q, want claude", spec.Path)
	}
	if ds.HasErrors() {
		t.Error("stub Build returned errors")
	}
	if a.Mappings() != nil {
		t.Error("stub Mappings should be nil")
	}
	if a.ToolCapabilities() != nil {
		t.Error("stub ToolCapabilities should be nil")
	}
}

func TestRunnerCompiles(t *testing.T) {
	t.Parallel()
	var r Runner = stubRunner{}
	res, ds, err := r.Run(context.Background(), Invocation{})
	if err != nil {
		t.Errorf("stub Run err = %v", err)
	}
	if res.Code != 0 {
		t.Errorf("stub Run Code = %d, want 0", res.Code)
	}
	if ds != nil {
		t.Error("stub Run diagnostics should be nil")
	}
}

func TestInvocationZeroValueIsSafe(t *testing.T) {
	t.Parallel()
	var inv Invocation
	if inv.Mode.Valid() {
		t.Error("zero-value Invocation.Mode reported Valid()=true; want false")
	}
	if inv.Output.Valid() != true {
		// OutputDefault ("") is valid by design.
		t.Error("zero-value Invocation.Output (= OutputDefault) should be Valid()=true")
	}
	if !inv.Sandbox.Valid() {
		t.Error("zero-value Invocation.Sandbox (= SandboxDefault) should be Valid()=true")
	}
	if !inv.Approval.Valid() {
		t.Error("zero-value Invocation.Approval (= ApprovalDefault) should be Valid()=true")
	}
	if inv.Config != nil {
		t.Error("zero-value Invocation.Config should be nil")
	}
}
