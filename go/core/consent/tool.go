package consent

import (
	"fmt"
	"path/filepath"
	"strings"

	"hop.top/kit/go/core/xdg"
	"hop.top/kit/go/runtime/telemetry"
)

// Per-tool consent state lives under <XDG_STATE_HOME>/<tool>/consent/.
const (
	consentStateDir = "consent"
	grantsFileName  = "grants.yaml"
	auditFileName   = "audit.jsonl"
	cursorFileName  = "audit.cursor"
)

// DefaultTool is the tool whose consent state a bare NewFileStore,
// NewFileAuditor or OpenGrants uses: the registered telemetry app
// prefix (telemetry.SetAppPrefix), lowercased, or "kit" when none is
// set. Register the prefix before the first consent call, or pass the
// tool explicitly (NewFileStoreFor, WithTool).
func DefaultTool() string {
	if p := strings.ToLower(strings.TrimSpace(telemetry.CurrentAppPrefix())); validateTool(p) == nil {
		return p
	}
	return xdgTool
}

func validateTool(tool string) error {
	if tool == "" || tool == "." || tool == ".." || strings.ContainsAny(tool, `/\`) || strings.TrimSpace(tool) != tool {
		return fmt.Errorf("consent: invalid tool name %q", tool)
	}
	return nil
}

// stateFile resolves <XDG_STATE_HOME>/<tool>/consent/<name>. Nothing
// is created here; writes create the 0700 directory on demand.
func stateFile(tool, name string) (string, error) {
	dir, err := xdg.StateDir(tool)
	if err != nil {
		return "", fmt.Errorf("consent: resolve state path: %w", err)
	}
	return filepath.Join(dir, consentStateDir, name), nil
}
