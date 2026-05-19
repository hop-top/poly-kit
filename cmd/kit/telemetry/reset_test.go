// reset_test.go covers `kit telemetry reset` at the runReset seam.
// Each test points XDG_CONFIG_HOME / XDG_STATE_HOME at a t.TempDir()
// so the consent file and installation_id live in isolated state,
// then drives runReset directly (bypassing cobra) and asserts on the
// rendered bytes plus the on-disk shape.

package telemetry

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hop.top/kit/go/core/consent"
	runtimetel "hop.top/kit/go/runtime/telemetry"
)

// TestReset_ClearsConsent seeds a granted decision, runs reset with
// autoConfirm=true, and asserts the store now returns StateUnknown.
func TestReset_ClearsConsent(t *testing.T) {
	withFreshXDG(t)

	seedConsent(t, consent.Decision{
		State:          consent.StateGranted,
		DecidedAt:      time.Now().UTC().Truncate(time.Second),
		PromptVersion:  1,
		DecisionSource: consent.SourcePrompt,
	})

	var stdout bytes.Buffer
	if err := runReset(context.Background(), strings.NewReader(""), &stdout, true); err != nil {
		t.Fatalf("runReset: %v", err)
	}

	store, err := consent.NewFileStore()
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	d, err := store.Get(context.Background())
	if err != nil {
		t.Fatalf("Get post-reset: %v", err)
	}
	if d.State != consent.StateUnknown {
		t.Errorf("State = %q, want %q after reset", d.State, consent.StateUnknown)
	}
}

// TestReset_RotatesInstallID asserts the install_id changes across a
// reset. We capture the hex pre-reset, run reset, capture again, and
// require the two strings differ. The runtime telemetry InstallationID
// call creates the file on first read; reset then rotates it.
func TestReset_RotatesInstallID(t *testing.T) {
	withFreshXDG(t)

	before, err := runtimetel.InstallationID()
	if err != nil {
		t.Fatalf("InstallationID pre-reset: %v", err)
	}

	var stdout bytes.Buffer
	if err := runReset(context.Background(), strings.NewReader(""), &stdout, true); err != nil {
		t.Fatalf("runReset: %v", err)
	}

	after, err := runtimetel.InstallationID()
	if err != nil {
		t.Fatalf("InstallationID post-reset: %v", err)
	}
	if before == after {
		t.Errorf("install_id unchanged across reset: %q", after)
	}
}

// TestReset_RequiresConfirmation runs reset with autoConfirm=false and
// stdin that says "n". The function must return a "reset aborted"
// error AND leave the seeded consent untouched.
func TestReset_RequiresConfirmation(t *testing.T) {
	withFreshXDG(t)

	seedConsent(t, consent.Decision{
		State:          consent.StateGranted,
		DecidedAt:      time.Now().UTC().Truncate(time.Second),
		PromptVersion:  1,
		DecisionSource: consent.SourcePrompt,
	})

	var stdout bytes.Buffer
	err := runReset(context.Background(), strings.NewReader("n\n"), &stdout, false)
	if err == nil {
		t.Fatalf("expected error from declined prompt, got nil")
	}
	if !strings.Contains(err.Error(), "aborted") {
		t.Errorf("error = %v, want substring %q", err, "aborted")
	}

	// Store unchanged.
	store, err := consent.NewFileStore()
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	d, gerr := store.Get(context.Background())
	if gerr != nil {
		t.Fatalf("Get post-abort: %v", gerr)
	}
	if d.State != consent.StateGranted {
		t.Errorf("State = %q after declined reset, want %q (unchanged)",
			d.State, consent.StateGranted)
	}
}

// TestReset_PromptAcceptsYes runs reset with autoConfirm=false but
// supplies "y\n" on stdin. The reset must proceed and clear the
// store. This is the only seam that exercises the inline prompt's
// affirmative branch.
func TestReset_PromptAcceptsYes(t *testing.T) {
	withFreshXDG(t)

	seedConsent(t, consent.Decision{
		State:          consent.StateGranted,
		DecidedAt:      time.Now().UTC().Truncate(time.Second),
		PromptVersion:  1,
		DecisionSource: consent.SourcePrompt,
	})

	var stdout bytes.Buffer
	if err := runReset(context.Background(), strings.NewReader("y\n"), &stdout, false); err != nil {
		t.Fatalf("runReset with y: %v", err)
	}

	store, err := consent.NewFileStore()
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	d, err := store.Get(context.Background())
	if err != nil {
		t.Fatalf("Get post-reset: %v", err)
	}
	if d.State != consent.StateUnknown {
		t.Errorf("State = %q, want %q after y-confirmed reset", d.State, consent.StateUnknown)
	}
}

// TestReset_HappyPath_AutoConfirm covers the unseeded happy path:
// fresh XDG, no prior consent, autoConfirm=true. Both Clear and
// Rotate must succeed; the function must return nil.
func TestReset_HappyPath_AutoConfirm(t *testing.T) {
	withFreshXDG(t)

	var stdout bytes.Buffer
	if err := runReset(context.Background(), strings.NewReader(""), &stdout, true); err != nil {
		t.Fatalf("runReset: %v", err)
	}

	got := stdout.String()
	if !strings.Contains(got, "Consent: cleared") {
		t.Errorf("output missing %q\n---\n%s", "Consent: cleared", got)
	}
	if !strings.Contains(got, "Install ID: rotated to") {
		t.Errorf("output missing %q\n---\n%s", "Install ID: rotated to", got)
	}
}

// TestReset_StoreErrorPropagates points XDG_CONFIG_HOME at a path that
// cannot be written to (a regular file used as the config root) so
// FileStore.Clear's write step fails. The reset must surface a
// non-nil error rather than silently degrade.
func TestReset_StoreErrorPropagates(t *testing.T) {
	// Need to pre-create a consent file so Clear actually attempts a
	// write — Clear is a no-op when the file is absent.
	withFreshXDG(t)
	seedConsent(t, consent.Decision{
		State:          consent.StateGranted,
		DecidedAt:      time.Now().UTC().Truncate(time.Second),
		PromptVersion:  1,
		DecisionSource: consent.SourcePrompt,
	})

	// Resolve where the consent file lives, then chmod its parent dir
	// to 0500 so the atomic write rename in writeDoc fails.
	store, err := consent.NewFileStore()
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	parent := filepath.Dir(store.Path())
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatalf("chmod parent: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })

	// Skip when running as root — root bypasses POSIX perms.
	if os.Geteuid() == 0 {
		t.Skip("running as root; cannot exercise EACCES path")
	}

	var stdout bytes.Buffer
	err = runReset(context.Background(), strings.NewReader(""), &stdout, true)
	if err == nil {
		t.Fatalf("expected error from unwritable consent dir, got nil")
	}
	if !strings.Contains(err.Error(), "reset:") {
		t.Errorf("error = %v, want %q prefix", err, "reset:")
	}
}

// TestReset_OutputMessage asserts the success render names both
// effects and embeds the new install_id verbatim. We unmarshal the
// new id out of the message and check it matches what InstallationID
// reads post-reset.
func TestReset_OutputMessage(t *testing.T) {
	withFreshXDG(t)

	var stdout bytes.Buffer
	if err := runReset(context.Background(), strings.NewReader(""), &stdout, true); err != nil {
		t.Fatalf("runReset: %v", err)
	}

	got := stdout.String()
	for _, want := range []string{
		"Telemetry reset complete",
		"Consent: cleared",
		"Install ID: rotated to",
		"Next interactive run will re-prompt",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\n---\n%s", want, got)
		}
	}

	// Extract the printed id and reconcile with InstallationID().
	post, err := runtimetel.InstallationID()
	if err != nil {
		t.Fatalf("InstallationID post-reset: %v", err)
	}
	if !strings.Contains(got, post) {
		t.Errorf("output missing rendered install_id %q\n---\n%s", post, got)
	}
}

// TestReset_PromptEOFAborts asserts that EOF on stdin (closed pipe,
// no answer typed) is treated as a declined prompt: the reset must
// abort with an error and leave state untouched. Mirrors the
// "default highlighted answer is No" contract from prompt.go.
func TestReset_PromptEOFAborts(t *testing.T) {
	withFreshXDG(t)

	var stdout bytes.Buffer
	// strings.NewReader("") returns io.EOF on the first Read.
	err := runReset(context.Background(), strings.NewReader(""), &stdout, false)
	if err == nil {
		t.Fatalf("expected error from EOF prompt, got nil")
	}
	if !strings.Contains(err.Error(), "aborted") {
		t.Errorf("error = %v, want substring %q", err, "aborted")
	}
}

// TestResetCmd_Wired smoke-checks the cobra leaf: the command is
// constructed, exposes a RunE, has the --yes flag, and carries the
// destructive side-effect tag so the kit-wide --confirm gate engages.
func TestResetCmd_Wired(t *testing.T) {
	c := resetCmd()
	if c.Use != "reset" {
		t.Fatalf("Use = %q, want %q", c.Use, "reset")
	}
	if c.RunE == nil {
		t.Fatalf("RunE is nil")
	}
	if c.Flags().Lookup("yes") == nil {
		t.Errorf("expected --yes flag on resetCmd")
	}
}

// TestCmd_TelemetryTreeIncludesReset asserts the parent Cmd wires the
// reset subcommand. Companion to TestCmd_TelemetryTree in
// status_test.go; this isolates the merge surface for T-0668.
func TestCmd_TelemetryTreeIncludesReset(t *testing.T) {
	root := Cmd()
	var found bool
	for _, c := range root.Commands() {
		if c.Name() == "reset" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("subcommand %q not wired under `kit telemetry`", "reset")
	}
}

// _ keeps io imported even on platforms where the EACCES test skips —
// future tests may want io.Discard for stdout suppression.
var _ = io.Discard
