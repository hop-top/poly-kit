// Package template provides the kit template engine: manifest schema,
// file rules, hooks, and rendering. This file defines the Manifest
// types, YAML parsing, and validation.
//
// See docs/superpowers/specs/2026-04-26-kit-init-design.md §6 for the
// schema reference.
package template

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Masterminds/semver/v3"
	"gopkg.in/yaml.v3"
)

// Variable describes a single user-supplied template variable.
type Variable struct {
	Name     string   `yaml:"name"`
	Prompt   string   `yaml:"prompt"`
	Required bool     `yaml:"required"`
	Default  string   `yaml:"default"`
	Validate string   `yaml:"validate"`
	Type     string   `yaml:"type"`
	Choices  []string `yaml:"choices"`
}

// FileRules controls how the engine treats files at render time.
type FileRules struct {
	Exclude []string `yaml:"exclude"`
	Binary  []string `yaml:"binary"`
}

// RenderRules declares post-substitution rules that BOTH the Go engine
// and bash init.sh apply identically. The spec is the single source of
// truth so the two implementations cannot drift.
//
// Fields default to off when omitted. A template that wants the
// historical behavior (strip ".tmpl", remove manifest files, pick a
// license) declares it explicitly here.
type RenderRules struct {
	// StripSuffixes lists filename suffixes to strip after render.
	// Example: [".tmpl"]. Files ending in these suffixes are renamed
	// in place (foo.go.tmpl -> foo.go). Collisions (the stripped name
	// already exists) are a fatal error in both implementations.
	StripSuffixes []string `yaml:"strip_suffixes"`

	// RemoveAfterRender lists project-relative paths to delete after
	// render completes. Use for manifest leftovers (kit-template.yaml,
	// tiers.yaml) that should not ship in the rendered project. Paths
	// are relative to the project root. Missing files are not an error.
	RemoveAfterRender []string `yaml:"remove_after_render"`

	// LicenseRule, if set, copies one of LicenseRule.Sources into
	// LicenseRule.Target based on the value of variable LicenseRule.Var,
	// then removes ALL files in Sources. If the resolved variable does
	// not match any source key, no copy happens and Sources are left
	// in place.
	LicenseRule *LicenseRule `yaml:"license"`
}

// LicenseRule maps a variable value to one of several license source
// files. After the chosen source is copied to Target, every file in
// Sources is removed.
type LicenseRule struct {
	// Var is the name of the variable whose value selects a source.
	// Conventionally "License" or "license".
	Var string `yaml:"var"`
	// Target is the destination path (project-relative) for the
	// copied license. Conventionally "LICENSE".
	Target string `yaml:"target"`
	// Sources maps variable values to source paths (project-relative).
	// E.g. { MIT: "LICENSE-MIT", Apache-2.0: "LICENSE-Apache-2.0" }.
	Sources map[string]string `yaml:"sources"`
}

// Hooks lists hook script paths (relative to the template root) that
// the orchestrator runs at the named lifecycle phases.
type Hooks struct {
	PreRender  []string `yaml:"pre_render"`
	PostRender []string `yaml:"post_render"`
	PostInit   []string `yaml:"post_init"`
	PostPush   []string `yaml:"post_push"`
}

// Manifest is the root document for kit-template.yaml.
type Manifest struct {
	Name        string      `yaml:"name"`
	Description string      `yaml:"description"`
	KitVersion  string      `yaml:"kit_version"`
	Variables   []Variable  `yaml:"variables"`
	Files       FileRules   `yaml:"files"`
	RenderRules RenderRules `yaml:"render_rules"`
	Hooks       Hooks       `yaml:"hooks"`
}

// Parse reads the YAML manifest at path and returns the decoded
// Manifest. Read or unmarshal errors are wrapped with
// ErrManifestInvalid.
func Parse(path string) (Manifest, error) {
	var m Manifest
	data, err := os.ReadFile(path)
	if err != nil {
		return m, NewManifestInvalidError(path, "read", err)
	}
	if err := yaml.Unmarshal(data, &m); err != nil {
		return m, NewManifestInvalidError(path, "unmarshal", err)
	}
	return m, nil
}

// Validate returns nil if the manifest is internally consistent. All
// validation failures wrap ErrManifestInvalid.
func (m Manifest) Validate() error {
	if strings.TrimSpace(m.Name) == "" {
		return NewManifestInvalidError("", "name is required", nil)
	}
	if m.KitVersion != "" {
		if _, err := semver.NewConstraint(m.KitVersion); err != nil {
			return NewManifestInvalidError("", fmt.Sprintf("kit_version %q", m.KitVersion), err)
		}
	}
	for i, v := range m.Variables {
		if err := validateVariable(i, v); err != nil {
			return err
		}
	}
	if err := validateRenderRules(m.RenderRules); err != nil {
		return err
	}
	for _, group := range [...]struct {
		phase   string
		scripts []string
	}{
		{"pre_render", m.Hooks.PreRender},
		{"post_render", m.Hooks.PostRender},
		{"post_init", m.Hooks.PostInit},
		{"post_push", m.Hooks.PostPush},
	} {
		for _, s := range group.scripts {
			if err := validateHookPath(group.phase, s); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateVariable(idx int, v Variable) error {
	if strings.TrimSpace(v.Name) == "" {
		return NewManifestInvalidError("", fmt.Sprintf("variables[%d]: name is required", idx), nil)
	}
	if v.Type == "choice" && len(v.Choices) == 0 {
		return NewManifestInvalidError("", fmt.Sprintf("variable %q: choices required when type=choice", v.Name), nil)
	}
	if v.Validate != "" {
		if _, err := regexp.Compile(v.Validate); err != nil {
			return NewManifestInvalidError("", fmt.Sprintf("variable %q: validate regex", v.Name), err)
		}
	}
	return nil
}

func validateRenderRules(r RenderRules) error {
	for i, suf := range r.StripSuffixes {
		if !strings.HasPrefix(suf, ".") {
			return NewManifestInvalidError("",
				fmt.Sprintf("render_rules.strip_suffixes[%d]: %q must start with '.'", i, suf), nil)
		}
	}
	for i, p := range r.RemoveAfterRender {
		if filepath.IsAbs(p) {
			return NewManifestInvalidError("",
				fmt.Sprintf("render_rules.remove_after_render[%d]: %q must be relative", i, p), nil)
		}
		cleaned := filepath.ToSlash(filepath.Clean(p))
		if cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, "/../") {
			return NewManifestInvalidError("",
				fmt.Sprintf("render_rules.remove_after_render[%d]: %q must not escape template root", i, p), nil)
		}
	}
	if r.LicenseRule != nil {
		if r.LicenseRule.Var == "" {
			return NewManifestInvalidError("", "render_rules.license.var is required", nil)
		}
		if r.LicenseRule.Target == "" {
			return NewManifestInvalidError("", "render_rules.license.target is required", nil)
		}
		if len(r.LicenseRule.Sources) == 0 {
			return NewManifestInvalidError("", "render_rules.license.sources must have at least one entry", nil)
		}
		for k, v := range r.LicenseRule.Sources {
			if v == "" {
				return NewManifestInvalidError("",
					fmt.Sprintf("render_rules.license.sources[%q]: empty path", k), nil)
			}
			if filepath.IsAbs(v) {
				return NewManifestInvalidError("",
					fmt.Sprintf("render_rules.license.sources[%q]: %q must be relative", k, v), nil)
			}
		}
	}
	return nil
}

func validateHookPath(phase, script string) error {
	if script == "" {
		return NewManifestInvalidError("", fmt.Sprintf("hooks.%s: empty path", phase), nil)
	}
	if filepath.IsAbs(script) {
		return NewManifestInvalidError("", fmt.Sprintf("hooks.%s: %q must be relative", phase, script), nil)
	}
	cleaned := filepath.ToSlash(filepath.Clean(script))
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, "/../") {
		return NewManifestInvalidError("", fmt.Sprintf("hooks.%s: %q must not escape template root", phase, script), nil)
	}
	return nil
}
