// Package real provides the production implementations of the
// sideeffect interfaces. Each impl delegates directly to the
// stdlib equivalent or to a kit primitive without instrumentation.
//
// These are the implementations the cli wires up by default.
// Switching to dryrun or testfake happens at the boundary; command
// code never observes the swap.
package real

import (
	"context"
	"net/http"
	"os"
	"os/exec"

	"hop.top/kit/go/runtime/domain"
)

// FS is the production sideeffect.FS implementation. Zero value is
// usable; no fields needed because every method delegates to os.
type FS struct{}

// WriteFile delegates to os.WriteFile.
func (FS) WriteFile(path string, data []byte, perm os.FileMode) error {
	return os.WriteFile(path, data, perm)
}

// MkdirAll delegates to os.MkdirAll.
func (FS) MkdirAll(path string, perm os.FileMode) error {
	return os.MkdirAll(path, perm)
}

// Rename delegates to os.Rename.
func (FS) Rename(oldpath, newpath string) error {
	return os.Rename(oldpath, newpath)
}

// Remove delegates to os.Remove.
func (FS) Remove(path string) error {
	return os.Remove(path)
}

// HTTP is the production sideeffect.HTTP implementation. Zero value
// uses http.DefaultClient; pass a configured *http.Client via
// NewHTTP for custom transport / timeout / cookie jar.
type HTTP struct {
	Client *http.Client
}

// NewHTTP returns an HTTP impl backed by client. Pass nil to use
// http.DefaultClient.
func NewHTTP(client *http.Client) HTTP {
	return HTTP{Client: client}
}

// Do delegates to (*http.Client).Do, falling back to
// http.DefaultClient when no client is configured.
func (h HTTP) Do(req *http.Request) (*http.Response, error) {
	c := h.Client
	if c == nil {
		c = http.DefaultClient
	}
	return c.Do(req)
}

// Bus is the production sideeffect.Bus implementation, wrapping a
// domain.EventPublisher. The wrapper exists so the interface is
// satisfied uniformly across real/dryrun/testfake; in steady state
// adopters that already have a domain.EventPublisher can pass it
// directly via NewBus.
type Bus struct {
	Pub domain.EventPublisher
}

// NewBus returns a Bus wrapping pub. nil pub is accepted but Publish
// will return ErrNilPublisher in that case.
func NewBus(pub domain.EventPublisher) Bus {
	return Bus{Pub: pub}
}

// ErrNilPublisher is returned by Bus.Publish when the wrapped
// publisher is nil. Adopters that wire up the real impl with a nil
// publisher (e.g. during a partial bootstrap) get a clear error
// instead of a nil-pointer panic.
var ErrNilPublisher = &busError{"sideeffect/real: nil publisher"}

type busError struct{ msg string }

func (e *busError) Error() string { return e.msg }

// Publish delegates to the wrapped EventPublisher.
func (b Bus) Publish(ctx context.Context, topic, source string, payload any) error {
	if b.Pub == nil {
		return ErrNilPublisher
	}
	return b.Pub.Publish(ctx, topic, source, payload)
}

// Exec is the production sideeffect.Exec implementation. Zero value
// is usable.
type Exec struct{}

// Run delegates to (*exec.Cmd).Run.
func (Exec) Run(cmd *exec.Cmd) error {
	return cmd.Run()
}

// Output delegates to (*exec.Cmd).Output.
func (Exec) Output(cmd *exec.Cmd) ([]byte, error) {
	return cmd.Output()
}
