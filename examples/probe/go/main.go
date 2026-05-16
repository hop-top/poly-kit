// Command probe — HTTP endpoint monitor using kit SDK packages.
// Demonstrates kit's non-CLI packages: config, bus, log.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"hop.top/kit/go/console/log"
	"hop.top/kit/go/core/config"
	"hop.top/kit/go/runtime/bus"

	"github.com/spf13/viper"
)

func main() {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}

	v := viper.New()
	logger := log.New(v)

	b := bus.New()
	b.SubscribeAsync("kit.probe.#", func(_ context.Context, e bus.Event) {
		if p, ok := e.Payload.(map[string]any); ok {
			logger.Info("event",
				"topic", string(e.Topic),
				"target", p["target"],
				"ok", p["ok"],
			)
		}
	})

	results := runProbe(cfg, b, logger)
	printSummary(results)
	_ = b.Close(context.Background())
}

// probeConfig mirrors probe.yaml.
type probeConfig struct {
	Interval string        `yaml:"interval"`
	Targets  []targetEntry `yaml:"targets"`
}

type targetEntry struct {
	Name    string       `yaml:"name"`
	URL     string       `yaml:"url"`
	Method  string       `yaml:"method"`
	Timeout string       `yaml:"timeout"`
	Expect  expectConfig `yaml:"expect"`
	Alerts  []alertEntry `yaml:"alerts"`
}

type expectConfig struct {
	Status int `yaml:"status"`
}

type alertEntry struct {
	Type string `yaml:"type"`
}

func loadConfig() (*probeConfig, error) {
	cfg := &probeConfig{}

	// Locate probe.yaml relative to executable or cwd
	candidates := []string{
		"../probe.yaml",
		"probe.yaml",
	}

	var cfgPath string
	for _, c := range candidates {
		abs, _ := filepath.Abs(c)
		if _, err := os.Stat(abs); err == nil {
			cfgPath = abs
			break
		}
	}
	if cfgPath == "" {
		return nil, fmt.Errorf("probe.yaml not found")
	}

	err := config.Load(cfg, config.Options{
		ProjectConfigPath: cfgPath,
	})
	return cfg, err
}

func parseDuration(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 5 * time.Second
	}
	return d
}
