package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"hop.top/kit/go/console/cli/svcconfig"
	"hop.top/kit/go/console/output"
	configoverrides "hop.top/kit/go/core/config"
)

// loadServiceConfig layers every source of services.* keys into the
// root's Viper before `serve` reads them, so each key resolves the way
// the serve contract documents — the flag, then the key from the
// environment, then from the config files, then the code option, then
// the kit default — whatever the tool wired itself:
//
//   - config files: the services block of the tool's system, user
//     (~/.config/<tool>/config.yaml) and project files, then of each
//     -c <path>, later files winning, at viper's config-file rung;
//   - environment: every <TOOL>_SERVICES_* variable, bound to the key
//     it spells (svcconfig.EnvKey), at viper's environment rung;
//   - -c services.<key>=<value>: at viper's override rung, above
//     every file and variable.
//
// Only services.* keys are layered; every other key resolves exactly
// as the tool wired it. Layering is idempotent, so a tool that already
// loads the same files and variables into the Viper gets the same
// values twice.
func (r *Root) loadServiceConfig() error {
	if r == nil || r.Viper == nil {
		return nil
	}
	extra, overrides, err := r.ConfigArgs()
	if err != nil {
		return output.UsageError(err.Error())
	}

	if r.Config.Name == "" {
		// No tool name, no conventional files or variable prefix:
		// only -c applies.
		for _, p := range extra {
			if err := r.mergeServiceFile(p, true); err != nil {
				return err
			}
		}
		r.setServiceOverrides(overrides)
		return nil
	}

	var layers configoverrides.Options
	if r.Config.ProjectMarker != "" {
		layers = configoverrides.OptionsForToolWithMarkers(r.Config.Name, []string{r.Config.ProjectMarker})
	} else {
		layers = configoverrides.OptionsForTool(r.Config.Name)
	}
	for _, p := range []string{layers.SystemConfigPath, layers.UserConfigPath, layers.ProjectConfigPath} {
		if err := r.mergeServiceFile(p, false); err != nil {
			return err
		}
	}
	for _, p := range extra {
		if err := r.mergeServiceFile(p, true); err != nil {
			return err
		}
	}

	var names []string
	if r.serveReg != nil {
		names = r.serveReg.Names()
	}
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		key, ok := svcconfig.EnvKey(name, r.Config.Name, names)
		if !ok {
			continue
		}
		if err := r.Viper.BindEnv(key, name); err != nil {
			return fmt.Errorf("bind %s to %s: %w", name, key, err)
		}
	}

	r.setServiceOverrides(overrides)
	return nil
}

// setServiceOverrides applies the services half of the -c key=value
// overrides.
func (r *Root) setServiceOverrides(overrides map[string]any) {
	if svc, ok := overrides[svcconfig.Root].(map[string]any); ok {
		setLeaves(r, svcconfig.Root, svc)
	} else if v, set := overrides[svcconfig.Root]; set {
		r.Viper.Set(svcconfig.Root, v)
	}
}

// mergeServiceFile merges the services block of the YAML file at path
// into the Viper's config-file rung. A missing file in a conventional
// slot is skipped; a missing file the operator named with -c, or a
// file that does not parse, is a usage error.
func (r *Root) mergeServiceFile(path string, required bool) error {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !required && errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return output.UsageError(fmt.Sprintf("config %s: %v", path, err))
	}
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return output.UsageError(fmt.Sprintf("config %s: %v", path, err))
	}
	block, ok := doc[svcconfig.Root]
	if !ok || block == nil {
		return nil
	}
	if err := r.Viper.MergeConfigMap(map[string]any{svcconfig.Root: block}); err != nil {
		return output.UsageError(fmt.Sprintf("config %s: %v", path, err))
	}
	return nil
}

// setLeaves sets every leaf of m under prefix at the override rung,
// one key at a time, so an override of one key leaves its siblings to
// the lower rungs.
func setLeaves(r *Root, prefix string, m map[string]any) {
	for k, v := range m {
		key := prefix + "." + k
		if sub, ok := v.(map[string]any); ok && len(sub) > 0 {
			setLeaves(r, key, sub)
			continue
		}
		r.Viper.Set(key, v)
	}
}
