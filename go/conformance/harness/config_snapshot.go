package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

// installConfigSnapshot pins viper's effective settings to the
// configured map (and/or file). Returns a restore callback. The
// mutex guard fires when a second concurrent invocation tries to
// install a snapshot — viper is global state and parallel use is
// unsafe.
//
// When neither WithConfigSnapshot nor WithConfigSnapshotFile is
// set, the function returns a no-op restore and never touches
// viper.
func (c *config) installConfigSnapshot() (func(), error) {
	if c.configSnapshot == nil && c.configSnapFile == "" {
		return func() {}, nil
	}
	if !configSnapshotMu.TryLock() {
		return func() {}, fmt.Errorf(
			"WithConfigSnapshot: concurrent harness invocation detected\n\n" +
				"  another test or harness call is already holding the config snapshot\n" +
				"  lock. WithConfigSnapshot mutates viper global state and cannot run\n" +
				"  in parallel.\n\n" +
				"  fix: serialize the affected tests, or run them in separate packages")
	}
	saved := viper.AllSettings()

	settings := c.configSnapshot
	if c.configSnapFile != "" {
		fileSettings, err := loadSnapshotFile(c.configSnapFile)
		if err != nil {
			configSnapshotMu.Unlock()
			return func() {}, err
		}
		if settings == nil {
			settings = fileSettings
		} else {
			for k, v := range fileSettings {
				if _, ok := settings[k]; !ok {
					settings[k] = v
				}
			}
		}
	}
	for k, v := range settings {
		viper.Set(k, v)
	}

	return func() {
		// viper.Reset() blasts everything. Re-set saved values to
		// the pre-install snapshot so any caller-installed keys are
		// preserved.
		for k, v := range saved {
			viper.Set(k, v)
		}
		// Remove keys we set that weren't in the saved snapshot.
		for k := range settings {
			if _, has := saved[k]; !has {
				viper.Set(k, nil)
			}
		}
		configSnapshotMu.Unlock()
	}, nil
}

// loadSnapshotFile reads path and returns its decoded settings map.
// Format is sniffed from the extension: .yaml/.yml → YAML, anything
// else → JSON.
func loadSnapshotFile(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("WithConfigSnapshotFile: read %q: %w", path, err)
	}
	out := map[string]any{}
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(raw, &out); err != nil {
			return nil, fmt.Errorf("WithConfigSnapshotFile: parse YAML %q: %w", path, err)
		}
	default:
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, fmt.Errorf("WithConfigSnapshotFile: parse JSON %q: %w", path, err)
		}
	}
	return out, nil
}
