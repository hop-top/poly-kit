package policy

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
	"hop.top/kit/go/core/xdg"
)

// Load reads a YAML policy file from path and returns the parsed
// Policy. path may be:
//
//   - An absolute path or path relative to the working directory.
//   - A bare name (no separators) — Load resolves it as
//     $XDG_CONFIG_HOME/<tool>/policies/<name>.yaml when tool is
//     non-empty (use Resolve to construct that path explicitly).
//
// Returns an error wrapping any fs.* sentinel; callers can check via
// errors.Is(err, fs.ErrNotExist).
func Load(path string) (Policy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Policy{}, fmt.Errorf("policy: load %s: %w", path, err)
	}
	var p Policy
	if err := yaml.Unmarshal(data, &p); err != nil {
		return Policy{}, fmt.Errorf("policy: parse %s: %w", path, err)
	}
	if p.Name == "" {
		// Default Name to the file's stem so audit output has a
		// human-friendly handle even when the YAML omits "name:".
		base := filepath.Base(path)
		p.Name = trimYAMLExt(base)
	}
	return p, nil
}

// Resolve returns the canonical XDG path for a named policy:
//
//	$XDG_CONFIG_HOME/<tool>/policies/<name>.yaml
//
// Adopters call this from cli.WithPolicy so the resolution rules
// stay in one place. Returns an error when xdg.RawConfigDir fails.
func Resolve(tool, name string) (string, error) {
	if tool == "" {
		return "", fmt.Errorf("policy: tool name required for XDG resolution")
	}
	if name == "" {
		return "", fmt.Errorf("policy: policy name required")
	}
	dir, err := xdg.RawConfigDir(tool)
	if err != nil {
		return "", fmt.Errorf("policy: resolve %s/%s: %w", tool, name, err)
	}
	return filepath.Join(dir, "policies", name+".yaml"), nil
}

// LoadNamed combines Resolve and Load for the common case.
func LoadNamed(tool, name string) (Policy, error) {
	p, err := Resolve(tool, name)
	if err != nil {
		return Policy{}, err
	}
	return Load(p)
}

func trimYAMLExt(name string) string {
	for _, ext := range []string{".yaml", ".yml"} {
		if l := len(name) - len(ext); l > 0 && name[l:] == ext {
			return name[:l]
		}
	}
	return name
}
