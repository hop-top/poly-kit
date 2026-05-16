package invoke

import (
	"context"

	"hop.top/kit/go/core/uxp"
)

// Mode is the agent CLI invocation mode.
type Mode string

const (
	ModeInteractive Mode = "interactive"
	ModeRun         Mode = "run"
	ModeResume      Mode = "resume"
)

// String implements fmt.Stringer.
func (m Mode) String() string { return string(m) }

// Valid reports whether m is one of the known modes.
func (m Mode) Valid() bool {
	switch m {
	case ModeInteractive, ModeRun, ModeResume:
		return true
	}
	return false
}

// OutputFormat is the desired stdout shape from a run.
//
// OutputJSON resolves to final-message JSON (one object). OutputStreamJSON
// resolves to the CLI's native event stream. See spec §16.3.
type OutputFormat string

const (
	OutputDefault    OutputFormat = ""
	OutputText       OutputFormat = "text"
	OutputJSON       OutputFormat = "json"
	OutputStreamJSON OutputFormat = "stream-json"
)

// String implements fmt.Stringer.
func (f OutputFormat) String() string { return string(f) }

// Valid reports whether f is one of the known formats.
func (f OutputFormat) Valid() bool {
	switch f {
	case OutputDefault, OutputText, OutputJSON, OutputStreamJSON:
		return true
	}
	return false
}

// SandboxMode is the requested filesystem/exec sandbox tier.
type SandboxMode string

const (
	SandboxDefault          SandboxMode = ""
	SandboxReadOnly         SandboxMode = "read-only"
	SandboxWorkspaceWrite   SandboxMode = "workspace-write"
	SandboxDangerFullAccess SandboxMode = "danger-full-access"
)

// String implements fmt.Stringer.
func (s SandboxMode) String() string { return string(s) }

// Valid reports whether s is one of the known sandbox modes.
func (s SandboxMode) Valid() bool {
	switch s {
	case SandboxDefault, SandboxReadOnly, SandboxWorkspaceWrite, SandboxDangerFullAccess:
		return true
	}
	return false
}

// ApprovalMode is the requested per-action approval policy.
//
// ApprovalAutoEdit must never silently degrade to a target CLI's
// auto-all/yolo flag. Adapters with no auto-edit equivalent return
// MappingUnsupported and refuse the build. See spec §15.5 anti-shims.
type ApprovalMode string

const (
	ApprovalDefault  ApprovalMode = ""
	ApprovalAsk      ApprovalMode = "ask"
	ApprovalAutoEdit ApprovalMode = "auto-edit"
	ApprovalAutoAll  ApprovalMode = "auto-all"
	ApprovalPlan     ApprovalMode = "plan"
	ApprovalNever    ApprovalMode = "never"
)

// String implements fmt.Stringer.
func (a ApprovalMode) String() string { return string(a) }

// Valid reports whether a is one of the known approval modes.
func (a ApprovalMode) Valid() bool {
	switch a {
	case ApprovalDefault, ApprovalAsk, ApprovalAutoEdit, ApprovalAutoAll, ApprovalPlan, ApprovalNever:
		return true
	}
	return false
}

// Invocation is the normalized request a caller assembles. Build
// translates it to native argv for a specific CLI.
//
// Files and AddDirs may both be set; adapters reduce them per the
// per-CLI shim policy (see spec §15.5). Config holds per-adapter
// extras keyed as "<cli>.<key>" or "uxp.<key>" for cross-adapter
// settings.
type Invocation struct {
	CLI       uxp.CLIName
	Mode      Mode
	Prompt    string
	CWD       string
	Model     string
	Agent     string
	SessionID string
	Continue  bool
	Fork      bool
	Output    OutputFormat
	Sandbox   SandboxMode
	Approval  ApprovalMode
	Files     []string
	Images    []string
	AddDirs   []string
	Config    map[string]string
	Raw       bool
	Debug     bool
	ExtraArgs []string
	Env       []string
}

// CommandSpec is a portable description of how to spawn the CLI.
// Path is the binary name (or absolute path) the runner should exec.
// Args excludes Path (consistent with os/exec.Cmd.Args[1:] convention
// when callers reconstruct the full argv).
type CommandSpec struct {
	Path string
	Args []string
	Dir  string
	Env  []string
}

// MappingSupport classifies how a universal option lands on a target
// CLI.
type MappingSupport string

const (
	// MappingNative — exact native flag or command exists.
	MappingNative MappingSupport = "native"
	// MappingShim — close-enough behavior with documented loss; adapter
	// emits a Diagnostic explaining the gap.
	MappingShim MappingSupport = "shim"
	// MappingUnsupported — no safe equivalent. Build returns a hard
	// error if the caller actually requested the option.
	MappingUnsupported MappingSupport = "unsupported"
	// MappingDangerous — a native flag exists but widens authority
	// beyond the universal option's intent. Build refuses unless the
	// caller opts in via Config["uxp.allow_dangerous"]="true".
	MappingDangerous MappingSupport = "dangerous"
)

// OptionMapping describes how one universal option maps onto one CLI.
// Adapters declare a static slice covering every universal option.
type OptionMapping struct {
	Universal string
	Support   MappingSupport
	Native    []string
	Notes     string
}

// ToolPermission classifies the broad capability a built-in agent tool
// exercises. Distinct from go/ai/toolspec's permission tokens, which
// describe a CLI's own command tree (see spec §16.5).
type ToolPermission string

const (
	ToolRead    ToolPermission = "read"
	ToolWrite   ToolPermission = "write"
	ToolExec    ToolPermission = "exec"
	ToolNetwork ToolPermission = "network"
	ToolBrowser ToolPermission = "browser"
	ToolTask    ToolPermission = "task"
)

// TranscriptSupport reports how observable a tool's invocation is in
// the CLI's transcript.
type TranscriptSupport string

const (
	TranscriptNative      TranscriptSupport = "native"
	TranscriptPartial     TranscriptSupport = "partial"
	TranscriptUnavailable TranscriptSupport = "unavailable"
)

// ToolCapability describes one agent built-in tool on one target CLI.
// Universal is the cross-CLI name (e.g. "shell.exec", "file.read");
// NativeNames lists the per-CLI labels the transcript actually uses.
type ToolCapability struct {
	Universal    string
	NativeNames  []string
	Support      MappingSupport
	Permission   ToolPermission
	Transcript   TranscriptSupport
	Controllable bool
	Notes        string
}

// Diagnostic carries one piece of mapping feedback. Level is one of
// "info", "warning", "error". Adapters use info for unknown Config
// keys, warning for shimmed options, error for refused mappings.
type Diagnostic struct {
	Level   string
	Option  string
	Message string
}

// Diagnostics is an ordered collection of Diagnostic entries.
type Diagnostics []Diagnostic

// Add appends d. Returns the receiver for chaining.
func (ds *Diagnostics) Add(d Diagnostic) *Diagnostics {
	*ds = append(*ds, d)
	return ds
}

// Filter returns the subset of diagnostics matching level. The
// receiver is unchanged.
func (ds Diagnostics) Filter(level string) Diagnostics {
	out := make(Diagnostics, 0, len(ds))
	for _, d := range ds {
		if d.Level == level {
			out = append(out, d)
		}
	}
	return out
}

// Errors returns diagnostics with Level=="error".
func (ds Diagnostics) Errors() Diagnostics { return ds.Filter("error") }

// HasErrors reports whether any diagnostic is at error level.
func (ds Diagnostics) HasErrors() bool {
	for _, d := range ds {
		if d.Level == "error" {
			return true
		}
	}
	return false
}

// InvocationAdapter is the contract every per-CLI adapter implements.
type InvocationAdapter interface {
	// CLI returns the canonical name this adapter handles.
	CLI() uxp.CLIName
	// Build translates inv into a CommandSpec plus diagnostics. A
	// hard error is reserved for impossible inputs (e.g. ModeResume
	// without SessionID and without Continue) and refused mappings.
	Build(inv Invocation) (CommandSpec, Diagnostics, error)
	// Mappings returns the static parity table for this CLI. The
	// regenerate-on-build parity README at go/core/uxp/README.md
	// is built from these slices (see spec §15.8 + T-0521).
	Mappings() []OptionMapping
	// ToolCapabilities returns the built-in agent tool taxonomy for
	// this CLI.
	ToolCapabilities() []ToolCapability
}

// Result is the outcome of a Run.
type Result struct {
	Command CommandSpec
	Code    int
	Stdout  []byte
	Stderr  []byte
}

// Runner optionally executes an Invocation. Build remains the pure
// API; Runner is reserved for callers that want a one-shot
// build-and-exec without writing their own os/exec wrapper.
type Runner interface {
	Run(ctx context.Context, inv Invocation) (Result, Diagnostics, error)
}
