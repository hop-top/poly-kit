package hatchet

import (
	"fmt"
	"net/url"
)

// ValidateConfig checks Hatchet adapter configuration.
//
// Required:
//   - server_url: valid URL for the Hatchet API server
//   - api_token:  non-empty authentication token
func ValidateConfig(cfg map[string]string) error {
	serverURL := cfg["server_url"]
	if serverURL == "" {
		return fmt.Errorf("hatchet: server_url is required")
	}
	if _, err := url.ParseRequestURI(serverURL); err != nil {
		return fmt.Errorf("hatchet: invalid server_url: %w", err)
	}

	if cfg["api_token"] == "" {
		return fmt.Errorf("hatchet: api_token is required")
	}

	return nil
}
