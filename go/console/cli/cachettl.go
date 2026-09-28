package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"hop.top/kit/go/transport/cmdsurface"
)

// SetCacheTTL declares that the served result cache may answer cmd, a
// read command, for ttl after a successful run, instead of running it
// again. It sets the kit/cache-ttl annotation; ttl must be greater than
// zero, and cmd must be annotated kit/side-effect: read, or
// Root.Validate refuses the tree.
//
// The cache is the api service's (services.api.cache): it answers the
// same caller's identical call — same path, flags and args — and
// renders ETag and Cache-Control on the GET route.
func SetCacheTTL(cmd *cobra.Command, ttl time.Duration) {
	if cmd.Annotations == nil {
		cmd.Annotations = make(map[string]string)
	}
	cmd.Annotations[cmdsurface.AnnotationCacheTTL] = ttl.String()
}

// checkCacheTTL reports a kit/cache-ttl annotation Root.Validate
// refuses: a value that is not a duration greater than zero, or one on
// a command not declared read, whose results are never cached.
func checkCacheTTL(cmd *cobra.Command) (string, bool) {
	raw, ok := cmd.Annotations[cmdsurface.AnnotationCacheTTL]
	if !ok {
		return "", false
	}
	if _, err := cmdsurface.ParseCacheTTL(raw); err != nil {
		return fmt.Sprintf("%s=%q", cmd.CommandPath(), raw), true
	}
	if s, _ := GetSideEffect(cmd); s != SideEffectRead {
		return fmt.Sprintf("%s=%q (only a kit/side-effect: read command is cached)", cmd.CommandPath(), raw), true
	}
	return "", false
}
