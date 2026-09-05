package routellm

import (
	"context"
	"errors"
	"os"
	"time"

	charmlog "charm.land/log/v2"
	"gopkg.in/yaml.v3"
)

// DefaultPollInterval is the default interval between stat polls.
const DefaultPollInterval = 5 * time.Second

// ConfigWatcher polls a YAML config file for changes and invokes a
// callback when the file's modification time changes.
//
// # Partial writes
//
// A poll can land between a truncating writer's O_TRUNC and its write —
// os.WriteFile, shell redirection, most editors' save-in-place paths all
// leave that window open. The file's mtime has already advanced, so the
// change is detected, but the read returns zero bytes. yaml.Unmarshal
// accepts empty input without error and produces a zero-value
// RouterConfig, so a naive watcher hands the adopter a blank config and
// reports success.
//
// The watcher therefore treats a zero-length read as "not ready" rather
// than as an empty config: nothing is delivered and lastMod is not
// advanced, so the next tick re-reads the same mtime and picks up the
// real content once the writer finishes.
//
// A file that is *legitimately* empty is byte-identical to one caught
// mid-truncation, so the two cannot be told apart and the watcher fails
// safe — it keeps the last good config rather than blanking it. An
// adopter who genuinely wants an empty config expresses it with a
// present-but-keyless document ("{}" or "---"), which is non-zero-length
// and is delivered normally as a zero-value RouterConfig.
//
// For the same reason lastMod advances only after a config is actually
// delivered. Advancing it on a rejected read would retire the mtime
// permanently: a file written once and never touched again would be
// skipped forever, because there is no second change to re-trigger on.
//
// Adopters who can write atomically — temp file in the same directory
// plus rename — avoid the window entirely and are the preferred path.
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

			cfg, err := loadConfigFile(w.path)
			if err != nil {
				if errors.Is(err, errEmptyConfigFile) {
					// Truncated mid-write, most likely. Leave
					// lastMod alone so the next tick retries.
					w.logger.Warn("config watcher: empty file, retrying",
						"path", w.path)
					continue
				}
				w.logger.Warn("config watcher: parse failed",
					"path", w.path, "err", err)
				continue
			}

			// Only a delivered config consumes the mtime; a rejected
			// read stays eligible for the next tick.
			lastMod = mod
			w.onChange(cfg)
		}
	}
}

// errEmptyConfigFile reports a zero-length read. It is returned instead
// of a zero-value RouterConfig because yaml.Unmarshal accepts empty
// input without error, which would otherwise make a truncated file
// look like a successfully parsed blank config.
var errEmptyConfigFile = errors.New("config file is empty")

// loadConfigFile reads and parses a YAML file into a RouterConfig. A
// zero-length file yields errEmptyConfigFile rather than a zero-value
// config.
func loadConfigFile(path string) (RouterConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return RouterConfig{}, err
	}

	if len(data) == 0 {
		return RouterConfig{}, errEmptyConfigFile
	}

	var cfg RouterConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return RouterConfig{}, err
	}

	return cfg, nil
}
