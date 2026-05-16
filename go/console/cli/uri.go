package cli

import (
	uricmd "hop.top/kit/go/console/uri"
)

// URIConfig configures the kit-shipped `uri` command group.
type URIConfig = uricmd.Config

// URIHandlerConfig supplies defaults for `uri handler` commands.
type URIHandlerConfig = uricmd.HandlerConfig

// WithURI returns a cli.New option that mounts the kit-shipped URI command
// group. Apps opt in when they configure URI namespace policy, vanity aliases,
// completion providers, or handler defaults.
func WithURI(cfg URIConfig) func(*Root) {
	return func(r *Root) {
		if r == nil || r.Cmd == nil {
			return
		}
		r.Cmd.AddCommand(uricmd.Command(cfg))
	}
}
