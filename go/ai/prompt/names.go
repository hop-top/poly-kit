package prompt

import (
	"fmt"
	"strings"
)

// checkName accepts one path segment: no separator, no "..", not absolute.
// kind names the thing for the error.
func checkName(kind, name string) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", fmt.Errorf("%w: %s name is required", ErrInvalid, kind)
	case name == ".", strings.Contains(name, ".."):
		return "", fmt.Errorf("%w: %s name %q is invalid", ErrInvalid, kind, name)
	case strings.ContainsAny(name, `/\`):
		return "", fmt.Errorf("%w: %s name %q must be a single path segment", ErrInvalid, kind, name)
	}
	return name, nil
}
