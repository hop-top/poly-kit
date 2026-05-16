package restate

import (
	"fmt"
	"net/url"
)

// ValidateConfig checks Restate adapter configuration.
//
// Required:
//   - endpoint: valid URL for the Restate ingress endpoint
func ValidateConfig(cfg map[string]string) error {
	endpoint := cfg["endpoint"]
	if endpoint == "" {
		return fmt.Errorf("restate: endpoint is required")
	}
	if _, err := url.ParseRequestURI(endpoint); err != nil {
		return fmt.Errorf("restate: invalid endpoint: %w", err)
	}

	return nil
}
