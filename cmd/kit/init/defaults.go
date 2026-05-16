// Package kitinit implements the `kit init` command and supporting helpers.
//
// defaults.go persists user-level defaults at xdg.ConfigDir("kit")/defaults.yaml
// so the wizard can pre-fill prompts across runs. Read is best-effort: missing
// or malformed files yield empty Defaults so callers degrade gracefully.
package kitinit

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
	"hop.top/kit/go/core/xdg"
)

// Defaults captures the values the init wizard remembers between runs.
type Defaults struct {
	Author           string   `yaml:"author"`
	Email            string   `yaml:"email"`
	AccountType      string   `yaml:"account_type"` // personal|org|none
	Org              string   `yaml:"org"`
	Visibility       string   `yaml:"visibility"` // public|private|internal
	License          string   `yaml:"license"`
	Theme            string   `yaml:"theme"`
	Template         string   `yaml:"template"`
	TemplateRegistry string   `yaml:"template_registry"`
	Runtime          []string `yaml:"runtime"`
	// Hop is *bool so absent (nil) is distinguishable from explicit false.
	// nil → fall through to built-in default (true); false → opt out.
	Hop *bool `yaml:"hop,omitempty"`
}

// Path returns the resolved location of the defaults file.
func Path() (string, error) {
	dir, err := xdg.ConfigDir("kit")
	if err != nil {
		return "", fmt.Errorf("kitinit: resolve config dir: %w", err)
	}
	return filepath.Join(dir, "defaults.yaml"), nil
}

// Read returns Defaults from disk. Missing file or parse error yields an empty
// Defaults with nil error. Only unexpected read failures propagate.
func Read() (Defaults, error) {
	path, err := Path()
	if err != nil {
		return Defaults{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Defaults{}, nil
		}
		return Defaults{}, fmt.Errorf("kitinit: read defaults: %w", err)
	}
	var d Defaults
	if err := yaml.Unmarshal(data, &d); err != nil {
		return Defaults{}, nil
	}
	return d, nil
}

// Write persists Defaults atomically via temp+rename. Parent dir is created
// at 0o750 if missing; the file is written with mode 0o644.
func Write(d Defaults) error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("kitinit: create config dir: %w", err)
	}
	data, err := yaml.Marshal(d)
	if err != nil {
		return fmt.Errorf("kitinit: marshal defaults: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("kitinit: write defaults tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("kitinit: rename defaults: %w", err)
	}
	return nil
}
