package upgrade

import (
	"os"
	"path/filepath"
	"time"

	"hop.top/kit/go/runtime/domain"
)

// Config holds all upgrade checker settings.
type Config struct {
	BinaryName     string
	CurrentVersion string
	GitHubRepo     string
	ReleaseURL     string
	ChecksumURL    string
	StateDir       string
	CacheTTL       time.Duration
	SnoozeDuration time.Duration
	Timeout        time.Duration
	SkipVerify     bool

	// TagPrefix selects this tool's releases in a repository that tags
	// several components (e.g. "xray/v"); see WithTagPrefix.
	TagPrefix string
	// IncludePrereleases offers prereleases; see WithIncludePrereleases.
	IncludePrereleases bool

	// pub, topics: opt-in bus emission. See bus.go for WithPublisher,
	// WithTopicPrefix, WithTopics.
	pub    domain.EventPublisher
	topics Topics
}

// Option is a functional option for Config.
type Option func(*Config)

func defaultConfig() Config {
	return Config{
		CacheTTL:       4 * time.Hour,
		SnoozeDuration: 24 * time.Hour,
		Timeout:        10 * time.Second,
		topics:         DefaultTopics,
	}
}

// WithBinary sets the binary name and current version.
func WithBinary(name, currentVersion string) Option {
	return func(c *Config) {
		c.BinaryName = name
		c.CurrentVersion = currentVersion
	}
}

// WithGitHub sets the GitHub owner/repo for release checks.
func WithGitHub(repo string) Option {
	return func(c *Config) { c.GitHubRepo = repo }
}

// WithTagPrefix limits the GitHub check to releases whose tag starts with
// prefix (e.g. "xray/v" for tag "xray/v0.2.0"); the prefix is stripped
// before the version comparison and other tags are ignored. Setting it
// lists releases instead of reading releases/latest, which may belong to
// another component.
func WithTagPrefix(prefix string) Option {
	return func(c *Config) { c.TagPrefix = prefix }
}

// WithIncludePrereleases makes the GitHub check offer prereleases.
// Setting it lists releases instead of reading releases/latest, which
// skips prereleases. Without it, prereleases are never offered.
func WithIncludePrereleases(include bool) Option {
	return func(c *Config) { c.IncludePrereleases = include }
}

// WithReleaseURL sets a custom release endpoint.
func WithReleaseURL(url string) Option {
	return func(c *Config) { c.ReleaseURL = url }
}

// WithCacheTTL overrides the default cache TTL.
func WithCacheTTL(d time.Duration) Option {
	return func(c *Config) { c.CacheTTL = d }
}

// WithSnoozeDuration overrides the default snooze duration.
func WithSnoozeDuration(d time.Duration) Option {
	return func(c *Config) { c.SnoozeDuration = d }
}

// WithTimeout overrides the HTTP request timeout.
func WithTimeout(d time.Duration) Option {
	return func(c *Config) { c.Timeout = d }
}

// WithStateDir overrides the XDG state directory.
func WithStateDir(dir string) Option {
	return func(c *Config) { c.StateDir = dir }
}

// WithChecksumURL sets a custom checksum file URL.
func WithChecksumURL(url string) Option {
	return func(c *Config) { c.ChecksumURL = url }
}

// WithSkipVerify disables checksum verification (dev/testing only).
func WithSkipVerify(skip bool) Option {
	return func(c *Config) { c.SkipVerify = skip }
}

func resolvedStateDir(cfg Config) string {
	if cfg.StateDir != "" {
		return cfg.StateDir
	}
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			base = os.TempDir()
		} else {
			base = filepath.Join(home, ".local", "state")
		}
	}
	return filepath.Join(base, cfg.BinaryName, "upgrade")
}
