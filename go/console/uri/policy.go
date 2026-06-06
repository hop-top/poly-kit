package uri

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"hop.top/cite/scheme"
)

func resolvePolicy(base scheme.Policy, path string) (scheme.Policy, error) {
	if base.DefaultNamespaceSegments == 0 && base.SchemeNamespaceSegments == nil && base.VanityAliases == nil && base.ActionRoutes == nil {
		base = scheme.DefaultPolicy
	}
	if strings.TrimSpace(path) == "" {
		return base, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return scheme.Policy{}, fmt.Errorf("uri policy: read %s: %w", path, err)
	}
	var decoded scheme.Policy
	if err := json.Unmarshal(raw, &decoded); err != nil {
		if yerr := yaml.Unmarshal(raw, &decoded); yerr != nil {
			return scheme.Policy{}, fmt.Errorf("uri policy: decode %s: %w", path, err)
		}
	}
	return decoded, nil
}
