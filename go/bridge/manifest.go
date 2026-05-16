package bridge

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"charm.land/log/v2"
	"gopkg.in/yaml.v3"
)

// Mode is the dispatch transport declared by a manifest.
type Mode string

const (
	ModeSubprocess Mode = "subprocess"
	ModeHTTP       Mode = "http"
	ModeSocket     Mode = "socket"
	ModeInproc     Mode = "inproc"
)

// Kind matches the Payload oneof variant a manifest accepts. The string
// values mirror the JSON wire keys on Payload (text|url|file|blob).
type Kind string

const (
	KindURL  Kind = "url"
	KindText Kind = "text"
	KindFile Kind = "file"
	KindBlob Kind = "blob"
)

// AcceptRule declares one matcher entry under a manifest's accepts list.
// Priority is per-rule, not per-manifest: a CLI may be the default for
// URLs but a fallback for files.
type AcceptRule struct {
	Kind     Kind     `yaml:"kind"`
	Priority int      `yaml:"priority"`
	Schemes  []string `yaml:"schemes,omitempty"`
	MIME     []string `yaml:"mime,omitempty"`
	MaxSize  int64    `yaml:"max_size,omitempty"`
}

// InvokeSpec captures the subprocess/exec contract: argv suffix, env
// overrides, and per-call timeout. Env values may contain
// `${payload.<field>}` placeholders expanded by the dispatcher.
type InvokeSpec struct {
	Args    []string          `yaml:"args,omitempty"`
	Env     map[string]string `yaml:"env,omitempty"`
	Timeout time.Duration     `yaml:"-"`
}

// invokeShadow mirrors InvokeSpec but exposes Timeout as a string so
// yaml.v3 can decode `30s` etc. without a custom Duration type.
type invokeShadow struct {
	Args    []string          `yaml:"args,omitempty"`
	Env     map[string]string `yaml:"env,omitempty"`
	Timeout string            `yaml:"timeout,omitempty"`
}

// UnmarshalYAML decodes InvokeSpec, parsing the timeout via
// time.ParseDuration so `30s` / `2m` etc. round-trip into time.Duration.
func (i *InvokeSpec) UnmarshalYAML(node *yaml.Node) error {
	var s invokeShadow
	if err := node.Decode(&s); err != nil {
		return err
	}
	i.Args = s.Args
	i.Env = s.Env
	if s.Timeout != "" {
		d, err := time.ParseDuration(s.Timeout)
		if err != nil {
			return fmt.Errorf("invoke.timeout: %w", err)
		}
		i.Timeout = d
	}
	return nil
}

// FallbackInproc is an optional escape hatch — if the consumer CLI runs
// a long-lived in-process listener at Socket, the receiver forwards
// rather than spawning a subprocess. Socket may contain $VAR / ~;
// expansion happens at dispatch time, not load time.
type FallbackInproc struct {
	Enabled bool   `yaml:"enabled"`
	Socket  string `yaml:"socket,omitempty"`
}

// Manifest is the on-disk declaration installed under
// `$XDG_CONFIG_HOME/hop-top/bridge.d/<cli>.yaml`. One file per CLI.
type Manifest struct {
	Name           string          `yaml:"name"`
	Version        string          `yaml:"version"`
	Binary         string          `yaml:"binary"`
	Mode           Mode            `yaml:"mode"`
	Accepts        []AcceptRule    `yaml:"accepts"`
	Invoke         InvokeSpec      `yaml:"invoke"`
	FallbackInproc *FallbackInproc `yaml:"fallback_inproc,omitempty"`
}

// validModes is the closed set of dispatch modes accepted by Validate.
var validModes = map[Mode]bool{
	ModeSubprocess: true,
	ModeHTTP:       true,
	ModeSocket:     true,
	ModeInproc:     true,
}

// validKinds is the closed set of accept-rule kinds, mirroring the
// Payload oneof variants.
var validKinds = map[Kind]bool{
	KindURL:  true,
	KindText: true,
	KindFile: true,
	KindBlob: true,
}

// Validate checks required fields + value invariants and returns the
// first violation. Aggregating errors is out of scope for v1; the
// receiver logs + skips on the first failure.
func (m *Manifest) Validate() error {
	if m.Name == "" {
		return errors.New("manifest: name is required")
	}
	if m.Version == "" {
		return errors.New("manifest: version is required")
	}
	if m.Binary == "" {
		return errors.New("manifest: binary is required")
	}
	if !validModes[m.Mode] {
		return fmt.Errorf("manifest: unknown mode %q (want subprocess|http|socket|inproc)", m.Mode)
	}
	if len(m.Accepts) == 0 {
		return errors.New("manifest: accepts must contain at least one rule")
	}
	for i, r := range m.Accepts {
		if err := r.validate(); err != nil {
			return fmt.Errorf("manifest: accepts[%d]: %w", i, err)
		}
	}
	return nil
}

// validate enforces per-AcceptRule invariants. Called from Manifest.Validate.
func (r AcceptRule) validate() error {
	if !validKinds[r.Kind] {
		return fmt.Errorf("unknown kind %q (want url|text|file|blob)", r.Kind)
	}
	if r.Priority < 0 {
		return fmt.Errorf("priority must be >= 0, got %d", r.Priority)
	}
	if r.MaxSize < 0 {
		return fmt.Errorf("max_size must be >= 0, got %d", r.MaxSize)
	}
	if r.Kind == KindURL {
		if len(r.Schemes) == 0 {
			return errors.New("url rule requires schemes (rule would match nothing)")
		}
		return nil
	}
	if len(r.MIME) == 0 {
		return fmt.Errorf("%s rule requires mime patterns", r.Kind)
	}
	return nil
}

// Load reads every *.yaml file under dir, validates each, and returns
// the valid manifests sorted alphabetically by Name. A missing dir
// returns an empty slice + nil error — fresh installs hit this path.
//
// Invalid manifests are logged via the package logger and skipped;
// they are NOT returned as an error to the caller. One broken consumer
// must not brick the receiver.
func Load(dir string) ([]*Manifest, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("manifest: read dir %q: %w", dir, err)
	}

	var out []*Manifest
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if filepath.Ext(name) != ".yaml" {
			continue
		}
		path := filepath.Join(dir, name)
		m, err := loadFile(path)
		if err != nil {
			pkgLogger().Warn("skipping invalid manifest", "path", path, "err", err)
			continue
		}
		out = append(out, m)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// loadFile reads + parses + validates a single manifest file.
func loadFile(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// pkgLogger returns the package-level logger used to surface skipped
// manifests. Lazily initialized so tests don't pay startup cost; nil
// is never returned. Output goes to os.Stderr at WarnLevel by default.
var loggerSingleton *log.Logger

func pkgLogger() *log.Logger {
	if loggerSingleton == nil {
		loggerSingleton = log.NewWithOptions(os.Stderr, log.Options{
			Level:  log.WarnLevel,
			Prefix: "bridge",
		})
	}
	return loggerSingleton
}
