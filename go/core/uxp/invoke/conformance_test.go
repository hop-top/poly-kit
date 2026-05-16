package invoke_test

import (
	"slices"
	"strings"
	"testing"

	"hop.top/kit/go/core/uxp"
	"hop.top/kit/go/core/uxp/invoke"
	"hop.top/kit/go/core/uxp/invoke/adapters/claude"
	"hop.top/kit/go/core/uxp/invoke/adapters/codex"
	"hop.top/kit/go/core/uxp/invoke/adapters/copilot"
	"hop.top/kit/go/core/uxp/invoke/adapters/crush"
	"hop.top/kit/go/core/uxp/invoke/adapters/cursoragent"
	"hop.top/kit/go/core/uxp/invoke/adapters/gemini"
	"hop.top/kit/go/core/uxp/invoke/adapters/goose"
	"hop.top/kit/go/core/uxp/invoke/adapters/kimi"
	"hop.top/kit/go/core/uxp/invoke/adapters/opencode"
	"hop.top/kit/go/core/uxp/invoke/adapters/qwen"
	"hop.top/kit/go/core/uxp/invoke/adapters/vibe"
)

// allAdapters is the canonical conformance set. Every CLI in this
// list must satisfy the same universal scenarios. Add a new adapter
// here when it ships — the conformance test refuses to leave a CLI
// out of the registry.
//
// This list is the de facto RegisteredAdapters() for the package
// until invoke.Register() is wired up (T-0521 / T-0522 may move it
// into the package itself).
var allAdapters = []invoke.InvocationAdapter{
	claude.New(),
	codex.New(),
	copilot.New(),
	crush.New(),
	cursoragent.New(),
	gemini.New(),
	goose.New(),
	kimi.New(),
	opencode.New(),
	qwen.New(),
	vibe.New(),
}

// happyPathOverrides describes per-adapter Invocation overrides
// needed for the basic ModeRun scenario to succeed. Some CLIs (e.g.
// copilot) require an auto-approve signal in non-interactive mode;
// others (e.g. goose) refuse per-invocation approval entirely.
type happyPathOverride struct {
	Approval invoke.ApprovalMode
	Config   map[string]string
}

var happyPathOverrides = map[uxp.CLIName]happyPathOverride{
	uxp.CLICopilot: {
		Approval: invoke.ApprovalAutoAll,
		Config:   map[string]string{"uxp.allow_dangerous": "true"},
	},
}

// universalOptions enumerates every option name an adapter's
// Mappings() must cover. Drift here is a spec change, not a test
// change — the universal contract lives in spec §15.4.
var universalOptions = []string{
	"ModeRun", "ModeInteractive", "ModeResume", "Continue", "Fork",
	"CWD", "Model", "Agent",
	"OutputText", "OutputJSON", "OutputStreamJSON",
	"SandboxReadOnly", "SandboxWorkspaceWrite", "SandboxDangerFullAccess",
	"ApprovalAsk", "ApprovalPlan", "ApprovalAutoEdit", "ApprovalAutoAll", "ApprovalNever",
	"AddDirs", "Files", "Images",
}

// universalToolCapabilities are the ToolCapability slots adapters
// must populate. Spec §8 defines this as the seed list; every
// adapter declares native, shim, or unsupported for each slot.
var universalToolCapabilities = []string{
	"shell.exec", "file.read", "file.write", "file.edit", "file.search",
	"web.search", "web.fetch", "todo.write", "task.spawn", "plan.update",
	"mcp.call", "image.read", "browser.operate", "user.message",
}

func TestConformance_CLIIsKnown(t *testing.T) {
	t.Parallel()
	reg := uxp.DefaultRegistry()
	for _, a := range allAdapters {
		t.Run(string(a.CLI()), func(t *testing.T) {
			if _, ok := reg.Get(a.CLI()); !ok {
				t.Errorf("adapter CLI %q is not registered in uxp.DefaultRegistry()", a.CLI())
			}
		})
	}
}

func TestConformance_BuildModeRunSucceeds(t *testing.T) {
	t.Parallel()
	for _, a := range allAdapters {
		t.Run(string(a.CLI()), func(t *testing.T) {
			t.Parallel()
			inv := invoke.Invocation{
				CLI:    a.CLI(),
				Mode:   invoke.ModeRun,
				Prompt: "ping",
			}
			if ov, ok := happyPathOverrides[a.CLI()]; ok {
				inv.Approval = ov.Approval
				inv.Config = ov.Config
			}
			spec, ds, err := a.Build(inv)
			if err != nil {
				t.Fatalf("Build(ModeRun) err = %v (diagnostics: %+v)", err, ds)
			}
			if spec.Path == "" {
				t.Errorf("Build returned empty Path")
			}
			if len(spec.Args) == 0 {
				t.Errorf("Build returned empty Args")
			}
			if ds.HasErrors() {
				t.Errorf("Build returned error diagnostics for happy path: %+v", ds.Errors())
			}
		})
	}
}

func TestConformance_BuildResumeWithoutSessionFails(t *testing.T) {
	t.Parallel()
	for _, a := range allAdapters {
		t.Run(string(a.CLI()), func(t *testing.T) {
			t.Parallel()
			_, _, err := a.Build(invoke.Invocation{
				CLI:  a.CLI(),
				Mode: invoke.ModeResume,
				// no SessionID, no Continue
			})
			if err == nil {
				t.Errorf("expected error for ModeResume with no SessionID and no Continue")
			}
		})
	}
}

func TestConformance_BuildContinueSucceeds(t *testing.T) {
	t.Parallel()
	for _, a := range allAdapters {
		t.Run(string(a.CLI()), func(t *testing.T) {
			t.Parallel()
			_, _, err := a.Build(invoke.Invocation{
				CLI:      a.CLI(),
				Mode:     invoke.ModeResume,
				Continue: true,
			})
			if err != nil {
				t.Errorf("Continue should always succeed; err = %v", err)
			}
		})
	}
}

func TestConformance_AntiShimAutoEditNeverDangerous(t *testing.T) {
	t.Parallel()
	// For every adapter, ApprovalAutoEdit either succeeds with a
	// native flag (because the adapter has one) or fails with an
	// error. It must NEVER silently produce a CommandSpec containing
	// a known dangerous flag.
	dangerousFlags := []string{
		"--dangerously-skip-permissions",
		"--dangerously-bypass-approvals-and-sandbox",
		"--yolo",
		"--allow-all",
		"--allow-all-tools",
		"-f", // cursor-agent's --force
		"--afk",
	}
	for _, a := range allAdapters {
		t.Run(string(a.CLI()), func(t *testing.T) {
			t.Parallel()
			spec, _, err := a.Build(invoke.Invocation{
				CLI:      a.CLI(),
				Mode:     invoke.ModeRun,
				Approval: invoke.ApprovalAutoEdit,
			})
			if err != nil {
				return // refused — that's fine
			}
			for _, dangerous := range dangerousFlags {
				if slices.Contains(spec.Args, dangerous) {
					t.Errorf("ApprovalAutoEdit produced dangerous flag %q in argv: %v",
						dangerous, spec.Args)
				}
			}
		})
	}
}

func TestConformance_DangerousMappingsRequireOptIn(t *testing.T) {
	t.Parallel()
	// ApprovalAutoAll without uxp.allow_dangerous must error for
	// every adapter that maps it to MappingDangerous.
	for _, a := range allAdapters {
		t.Run(string(a.CLI()), func(t *testing.T) {
			t.Parallel()
			// Find the AutoAll mapping for this adapter.
			var mapping *invoke.OptionMapping
			for i := range a.Mappings() {
				if a.Mappings()[i].Universal == "ApprovalAutoAll" {
					mapping = &a.Mappings()[i]
					break
				}
			}
			if mapping == nil {
				t.Skip("no ApprovalAutoAll mapping declared")
			}
			if mapping.Support != invoke.MappingDangerous {
				t.Skip("ApprovalAutoAll is not Dangerous on this adapter")
			}

			_, _, err := a.Build(invoke.Invocation{
				CLI:      a.CLI(),
				Mode:     invoke.ModeRun,
				Approval: invoke.ApprovalAutoAll,
				// no uxp.allow_dangerous
			})
			if err == nil {
				t.Error("ApprovalAutoAll without opt-in should error on a Dangerous mapping")
			}
		})
	}
}

func TestConformance_MappingsCoverAllUniversalOptions(t *testing.T) {
	t.Parallel()
	for _, a := range allAdapters {
		t.Run(string(a.CLI()), func(t *testing.T) {
			t.Parallel()
			have := map[string]bool{}
			for _, m := range a.Mappings() {
				have[m.Universal] = true
			}
			for _, want := range universalOptions {
				if !have[want] {
					t.Errorf("Mappings missing universal option %q", want)
				}
			}
		})
	}
}

func TestConformance_MappingsHaveValidSupport(t *testing.T) {
	t.Parallel()
	valid := map[invoke.MappingSupport]bool{
		invoke.MappingNative:      true,
		invoke.MappingShim:        true,
		invoke.MappingUnsupported: true,
		invoke.MappingDangerous:   true,
	}
	for _, a := range allAdapters {
		t.Run(string(a.CLI()), func(t *testing.T) {
			t.Parallel()
			for _, m := range a.Mappings() {
				if !valid[m.Support] {
					t.Errorf("Mappings[%s].Support = %q (invalid)", m.Universal, m.Support)
				}
				// Native and Shim mappings should have Native flag
				// strings; Unsupported should have nil. (Dangerous
				// usually has flag strings — caller needs to know
				// what flag would be emitted.)
				switch m.Support {
				case invoke.MappingNative, invoke.MappingShim, invoke.MappingDangerous:
					if len(m.Native) == 0 {
						t.Errorf("Mappings[%s].Support=%s but Native is empty",
							m.Universal, m.Support)
					}
				case invoke.MappingUnsupported:
					if len(m.Native) != 0 {
						t.Errorf("Mappings[%s].Support=Unsupported but Native is non-empty: %v",
							m.Universal, m.Native)
					}
				}
			}
		})
	}
}

func TestConformance_ToolCapabilitiesCoverSeedList(t *testing.T) {
	t.Parallel()
	for _, a := range allAdapters {
		t.Run(string(a.CLI()), func(t *testing.T) {
			t.Parallel()
			have := map[string]bool{}
			for _, c := range a.ToolCapabilities() {
				have[c.Universal] = true
			}
			for _, want := range universalToolCapabilities {
				if !have[want] {
					t.Errorf("ToolCapabilities missing %q", want)
				}
			}
		})
	}
}

func TestConformance_ToolCapabilitiesValidPermission(t *testing.T) {
	t.Parallel()
	valid := map[invoke.ToolPermission]bool{
		invoke.ToolRead:    true,
		invoke.ToolWrite:   true,
		invoke.ToolExec:    true,
		invoke.ToolNetwork: true,
		invoke.ToolBrowser: true,
		invoke.ToolTask:    true,
	}
	validTranscript := map[invoke.TranscriptSupport]bool{
		invoke.TranscriptNative:      true,
		invoke.TranscriptPartial:     true,
		invoke.TranscriptUnavailable: true,
	}
	for _, a := range allAdapters {
		t.Run(string(a.CLI()), func(t *testing.T) {
			t.Parallel()
			for _, c := range a.ToolCapabilities() {
				if !valid[c.Permission] {
					t.Errorf("ToolCapabilities[%s].Permission = %q (invalid)",
						c.Universal, c.Permission)
				}
				if !validTranscript[c.Transcript] {
					t.Errorf("ToolCapabilities[%s].Transcript = %q (invalid)",
						c.Universal, c.Transcript)
				}
			}
		})
	}
}

func TestConformance_ForkRequiresResume(t *testing.T) {
	t.Parallel()
	for _, a := range allAdapters {
		t.Run(string(a.CLI()), func(t *testing.T) {
			t.Parallel()
			_, _, err := a.Build(invoke.Invocation{
				CLI:  a.CLI(),
				Mode: invoke.ModeRun,
				Fork: true,
			})
			if err == nil {
				t.Error("Fork=true outside ModeResume should always error")
			}
		})
	}
}

func TestConformance_PathIsBinaryName(t *testing.T) {
	t.Parallel()
	// CommandSpec.Path should be the binary name as registered, not a
	// resolved absolute path. The runner is responsible for $PATH
	// resolution.
	reg := uxp.DefaultRegistry()
	for _, a := range allAdapters {
		t.Run(string(a.CLI()), func(t *testing.T) {
			t.Parallel()
			info, _ := reg.Get(a.CLI())
			inv := invoke.Invocation{
				CLI:  a.CLI(),
				Mode: invoke.ModeRun,
			}
			if ov, ok := happyPathOverrides[a.CLI()]; ok {
				inv.Approval = ov.Approval
				inv.Config = ov.Config
			}
			spec, _, err := a.Build(inv)
			if err != nil {
				t.Fatalf("Build err = %v", err)
			}
			expected := info.BinaryNames[0]
			if spec.Path != expected {
				t.Errorf("Path = %q, want %q (binary name from registry)",
					spec.Path, expected)
			}
			// Path must not contain '/' — it's a name, not an absolute
			// path. (Adapters that resolve paths break the runner.)
			if strings.Contains(spec.Path, "/") {
				t.Errorf("Path %q contains '/'; adapters should emit binary name only", spec.Path)
			}
		})
	}
}
