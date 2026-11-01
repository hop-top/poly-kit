package mcpserve

import (
	"fmt"
	"strings"

	"hop.top/kit/go/transport/cmdsurface"
)

// How the stdio peer is established, the values of
// services.mcp.stdio_caller and Config.StdioCaller.
const (
	// StdioCallerOwner establishes the stdio peer as the tool's owner:
	// it spawned the process and runs as its user, so it holds the
	// owner's authority (every scope) and kit/auth-required leaves
	// admit. It is the default.
	StdioCallerOwner = "owner"
	// StdioCallerUnestablished establishes nobody, like an
	// unauthenticated remote caller: caller-bound rules and budgets do
	// not match, permission rules see an unestablished principal, and
	// kit/auth-required leaves are refused.
	StdioCallerUnestablished = "unestablished"
)

const subkeyStdioCaller = ".stdio_caller"

// stdioCallerKey is the config key, for messages.
const stdioCallerKey = keyPrefix + subkeyStdioCaller

// parseStdioCaller normalizes and checks a stdio_caller value.
func parseStdioCaller(v string) (string, error) {
	switch c := strings.ToLower(strings.TrimSpace(v)); c {
	case StdioCallerOwner, StdioCallerUnestablished:
		return c, nil
	default:
		return "", fmt.Errorf("%s: unknown caller %q; use %q or %q",
			stdioCallerKey, v, StdioCallerOwner, StdioCallerUnestablished)
	}
}

// stdioCaller resolves how the stdio peer is established:
// Config.StdioCaller, then services.mcp.stdio_caller (env
// <TOOL>_SERVICES_MCP_STDIO_CALLER), then [StdioCallerOwner]. An
// unknown value is an error wherever it came from.
func (s *mcpService) stdioCaller() (string, error) {
	if s.cfg.StdioCaller != "" {
		return parseStdioCaller(s.cfg.StdioCaller)
	}
	if s.root.Viper != nil {
		if v := s.root.Viper.GetString(stdioCallerKey); strings.TrimSpace(v) != "" {
			return parseStdioCaller(v)
		}
	}
	return StdioCallerOwner, nil
}

// established is the Meta.Established the stdio peer's calls carry.
func stdioEstablished(caller string) cmdsurface.Establishment {
	if caller == StdioCallerUnestablished {
		return cmdsurface.EstablishedNone
	}
	return cmdsurface.EstablishedTransport
}
