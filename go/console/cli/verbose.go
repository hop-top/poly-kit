package cli

// VerboseCount returns the -V count from the root command.
// 0=info (default), 1=debug, 2+=trace. --quiet overrides.
func (r *Root) VerboseCount() int {
	return r.verboseCount
}
