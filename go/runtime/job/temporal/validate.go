package temporal

import (
	"fmt"
	"net"
)

// ValidateConfig checks Temporal adapter configuration.
//
// Required:
//   - server_address: host:port for the Temporal frontend service
//   - namespace:      Temporal namespace (non-empty)
func ValidateConfig(cfg map[string]string) error {
	addr := cfg["server_address"]
	if addr == "" {
		return fmt.Errorf("temporal: server_address is required")
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf(
			"temporal: server_address must be host:port, got %q: %w", addr, err,
		)
	}
	if host == "" {
		return fmt.Errorf("temporal: server_address host must be non-empty")
	}
	if port == "" {
		return fmt.Errorf("temporal: server_address port must be non-empty")
	}

	if cfg["namespace"] == "" {
		return fmt.Errorf("temporal: namespace is required")
	}

	return nil
}
