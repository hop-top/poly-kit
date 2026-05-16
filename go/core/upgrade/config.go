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
