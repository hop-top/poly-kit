package sideeffect

import (
	"context"
	"net/http"
	"os"
	"os/exec"
)

// FS is the mutating-only file-system seam. Reads (Open, Stat,
// ReadFile, ReadDir, etc.) intentionally bypass FS and use the
// stdlib directly.
//
// All paths are interpreted by the implementation, not normalised
// by the interface. Adopters that need joins or cleanups do them
// before calling.
type FS interface {
	// WriteFile writes data to path with permission perm. Creates
	// the file if missing; truncates and overwrites if present.
	// Mirrors os.WriteFile.
	WriteFile(path string, data []byte, perm os.FileMode) error

	// MkdirAll creates path and any necessary parents with
	// permission perm. Mirrors os.MkdirAll.
	MkdirAll(path string, perm os.FileMode) error

	// Rename renames (moves) old to new. Mirrors os.Rename.
	Rename(oldpath, newpath string) error

	// Remove removes the named file or empty directory. Mirrors
	// os.Remove.
	Remove(path string) error
}

// HTTP wraps the mutating verbs of net/http.Client. The
// implementation decides whether the request is sent on the wire.
//
// Safe verbs (GET/HEAD) ALWAYS reach the underlying http.Client
// even in dry-run mode; the dryrun impl proxies them to the real
// client. Mutating verbs (POST/PUT/PATCH/DELETE) are intercepted.
//
// Adopters that need full http.Client functionality (CheckRedirect,
// CookieJar, custom Transport) configure those on the *http.Client
// passed to the implementation, not on the interface.
type HTTP interface {
	// Do sends the request and returns the response. The
	// implementation chooses whether to dispatch on the wire.
	// Mirrors (*http.Client).Do.
	Do(req *http.Request) (*http.Response, error)
}

// Bus is the event-publication seam. Wraps the existing
// domain.EventPublisher shape so a Bus implementation can substitute
// for any kit consumer that already takes a domain.EventPublisher.
//
// The dryrun impl auto-tags bus.Qualifiers{Mechanism: "dry_run"} on
// payloads that embed bus.Qualifiers. Payloads that do not embed
// Qualifiers are published unchanged with a debug log line; tagging
// is best-effort. See ADR-0019 for the rationale.
type Bus interface {
	// Publish sends an event to the bus. Mirrors
	// domain.EventPublisher.Publish.
	Publish(ctx context.Context, topic, source string, payload any) error
}

// Exec is the subprocess seam. Wraps os/exec for the two most
// common shapes: fire-and-forget (Run) and capture-stdout (Output).
//
// Subprocess containment in dry-run is hopeless — the child process
// has its own decisions to make. The dryrun impl prints the argv
// and skips invocation; this is documented as a UX limitation.
type Exec interface {
	// Run starts the command and waits for it to complete.
	// Mirrors (*exec.Cmd).Run.
	Run(cmd *exec.Cmd) error

	// Output runs the command and returns its standard output.
	// Mirrors (*exec.Cmd).Output.
	Output(cmd *exec.Cmd) ([]byte, error)
}
