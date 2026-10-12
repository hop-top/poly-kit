package cli

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"

	"hop.top/kit/go/transport/cmdsurface"
)

func TestSetWithholdFromWritesReplacesAndClears(t *testing.T) {
	cmd := &cobra.Command{Use: "watch"}
	SetWithholdFrom(cmd, cmdsurface.SurfaceREST, cmdsurface.SurfaceMCP)
	assert.Equal(t, "rest,mcp", cmd.Annotations[cmdsurface.AnnotationWithholdFrom])

	SetWithholdFrom(cmd, cmdsurface.SurfaceRPC)
	assert.Equal(t, "rpc", cmd.Annotations[cmdsurface.AnnotationWithholdFrom], "a second call replaces")

	SetWithholdFrom(cmd)
	_, ok := cmd.Annotations[cmdsurface.AnnotationWithholdFrom]
	assert.False(t, ok, "an empty list removes the annotation")

	// No annotations map yet: clearing is a no-op, not a panic.
	SetWithholdFrom(&cobra.Command{Use: "bare"})
}
