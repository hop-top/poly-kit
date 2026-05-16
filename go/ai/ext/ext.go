// Package ext defines the shared extensibility contract for kit-based projects.
//
// Extensions declare capabilities (Registry, Hook, Discover, Config) and
// the framework routes them to the appropriate mechanism(s).
package ext

import (
	"context"
	"strings"
)

// Capability identifies which extensibility mechanism(s) an extension supports.
type Capability int

const (
	// CapRegistry enables init()-based plugin registration.
	CapRegistry Capability = 1 << iota
	// CapHook enables lifecycle hook subscription.
	CapHook
	// CapDiscover enables PATH-based external plugin discovery.
	CapDiscover
	// CapConfig enables config-driven feature toggling.
	CapConfig
)

func (c Capability) String() string {
	if c == 0 {
		return "none"
	}
	var parts []string
	for _, pair := range []struct {
		cap  Capability
		name string
	}{
		{CapRegistry, "registry"},
		{CapHook, "hook"},
		{CapDiscover, "discover"},
		{CapConfig, "config"},
	} {
		if c&pair.cap != 0 {
			parts = append(parts, pair.name)
		}
	}
	if len(parts) == 0 {
		return "unknown"
	}
	return strings.Join(parts, "|")
}

// Has reports whether c includes the given capability.
func (c Capability) Has(cap Capability) bool {
	return c&cap != 0
}

// Metadata holds descriptive information about an extension.
type Metadata struct {
	Name        string
	Version     string
	Description string
}

// Extension is the universal contract all extensibility mechanisms share.
type Extension interface {
	// Meta returns the extension's identity and version.
	Meta() Metadata
	// Capabilities returns the bitmask of supported mechanisms.
	Capabilities() Capability
	// Init is called once when the extension is activated.
	Init(ctx context.Context) error
	// Close is called when the extension is deactivated.
	Close() error
}
