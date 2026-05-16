package routellm

import (
	"context"
	"os"
	"time"

	charmlog "charm.land/log/v2"
	"gopkg.in/yaml.v3"
)

// DefaultPollInterval is the default interval between stat polls.
const DefaultPollInterval = 5 * time.Second

// ConfigWatcher polls a YAML config file for changes and invokes a
// callback when the file's modification time changes.
type ConfigWatcher struct {
	path     string
	interval time.Duration
	onChange func(RouterConfig)
	logger   *charmlog.Logger
	cancel   context.CancelFunc
	done     chan struct{}
}

// NewConfigWatcher creates a watcher that polls path for mtime changes
// and calls onChange with the parsed RouterConfig on each detected change.
//
// Apply Options to override defaults; WithLogger swaps the kit/log
// logger used for stat-failed and parse-failed warnings.
func NewConfigWatcher(
	path string, onChange func(RouterConfig), opts ...Option,
) *ConfigWatcher {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(cfg)
	}
	return &ConfigWatcher{
		path:     path,
		interval: DefaultPollInterval,
		onChange: onChange,
		logger:   cfg.logger,
		done:     make(chan struct{}),
	}
}

// Start begins polling in a background goroutine. It blocks until the
// context is canceled or Stop is called.
func (w *ConfigWatcher) Start(ctx context.Context) {
	ctx, w.cancel = context.WithCancel(ctx)
	go w.poll(ctx)
}

// Stop cancels the polling goroutine and waits for it to finish.
// Safe to call even if Start was never called.
func (w *ConfigWatcher) Stop() {
	if w.cancel == nil {
		return // never started
	}
	w.cancel()
	<-w.done
}

func (w *ConfigWatcher) poll(ctx context.Context) {
	defer close(w.done)

	var lastMod time.Time

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			info, err := os.Stat(w.path)
			if err != nil {
				w.logger.Warn("config watcher: stat failed",
					"path", w.path, "err", err)
				continue
			}

			mod := info.ModTime()
			if mod.Equal(lastMod) {
				continue
			}
			lastMod = mod

			cfg, err := loadConfigFile(w.path)
			if err != nil {
				w.logger.Warn("config watcher: parse failed",
					"path", w.path, "err", err)
				continue
			}

			w.onChange(cfg)
		}
	}
}

// loadConfigFile reads and parses a YAML file into a RouterConfig.
func loadConfigFile(path string) (RouterConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return RouterConfig{}, err
	}

	var cfg RouterConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return RouterConfig{}, err
	}

	return cfg, nil
}
