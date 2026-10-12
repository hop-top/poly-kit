package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"hop.top/kit/go/transport/cmdsurface"
)

// SetWithholdFrom keeps cmd off the named served surfaces while every
// other surface keeps it. It sets the kit/withhold-from annotation
// ([cmdsurface.AnnotationWithholdFrom]); calling it again replaces the
// list, and an empty list removes the annotation.
//
// A command that must never run inside a served invocation — a
// long-running watch that would hold the one-call-at-a-time runner — is
// withheld from the network surfaces and stays on the socket:
//
//	cli.SetWithholdFrom(watch,
//	    cmdsurface.SurfaceREST, cmdsurface.SurfaceSSE,
//	    cmdsurface.SurfaceRPC, cmdsurface.SurfaceMCP)
//
// Each named surface's discovery leaves the command out — REST
// describes it non-invocable with the reason withheld-by-command and
// mounts no route, MCP lists no tool — and a call there is refused as
// not enabled. No Expose, Hide or configuration serves it there. Name
// cmdsurface.SurfaceSocket as well to take it off the socket.
//
// Use kit/self-hosting instead for a command that must not run inside
// any served invocation, the socket's included.
func SetWithholdFrom(cmd *cobra.Command, surfaces ...cmdsurface.Surface) {
	if len(surfaces) == 0 {
		delete(cmd.Annotations, cmdsurface.AnnotationWithholdFrom)
		return
	}
	names := make([]string, len(surfaces))
	for i, s := range surfaces {
		names[i] = string(s)
	}
	if cmd.Annotations == nil {
		cmd.Annotations = make(map[string]string)
	}
	cmd.Annotations[cmdsurface.AnnotationWithholdFrom] = strings.Join(names, ",")
}
