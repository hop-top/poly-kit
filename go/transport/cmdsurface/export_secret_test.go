package cmdsurface

import (
	"testing"

	"github.com/spf13/cobra"
)

// Exports for the external transport tests of secret output: the
// command tree, the runner, the values, and the sink rig.
const (
	OSValue  = osValue
	OSNested = osNested
	OSRow1   = osRow1
	OSRow2   = osRow2
	OSText   = osText
)

// OSRig is the sink rig of output_secret_test.go: every shipped sink
// type and a recording custom sink.
type OSRig = osRig

// NewOSRig builds the rig.
func NewOSRig(t *testing.T) *OSRig { return newOSRig(t) }

// OSTree is the command tree.
func OSTree() *cobra.Command { return osTree() }

// OSRunner is the runner producing the value for every leaf.
func OSRunner() Runner { return &osRunner{} }

// OSData is the structured result of reveal.
func OSData() map[string]any { return osData() }

// Specs are the rig's sinks.
func (r *osRig) Specs() []SinkSpec { return r.specs }

// Written is every byte the rig's sinks wrote.
func (r *osRig) Written() string { return r.bytesWritten() }

// PerSink is each sink's bytes by name.
func (r *osRig) PerSink() map[string]string { return r.perSink() }
