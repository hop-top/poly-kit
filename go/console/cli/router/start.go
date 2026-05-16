package router

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"hop.top/kit/go/ai/llm/routellm"
)

func startCmd() *cobra.Command {
	var (
		daemon  bool
		pidPath string
	)

	cmd := &cobra.Command{
		Use:   "start [config]",
		Short: "Start a routellm server",
		Long: `Start a routellm server process.

Reads config from the given path or the default location
($XDG_CONFIG_HOME/hop/llm/router/config.yaml). Spawns
python -m routellm.openai_server, writes the PID, and waits
for a health check before returning.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath, err := resolveConfigPath(args)
			if err != nil {
				return err
			}

			cfg, err := loadRouterYAML(cfgPath)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			slug := slugFromConfig(cfg)

			// Resolve PID file path.
			pidFile := pidPath
			if pidFile == "" && cfg.PIDFile != "" {
				pidFile = cfg.PIDFile
			}
			if pidFile == "" {
				pidFile, err = pidFilePath(slug)
				if err != nil {
					return fmt.Errorf("resolve pid path: %w", err)
				}
			}

			if _, err := ensureStateDir(); err != nil {
				return fmt.Errorf("create state dir: %w", err)
			}

			// Build command arguments.
			cmdArgs := buildServerArgs(cfg)

			proc := exec.CommandContext(
				cmd.Context(), "python", cmdArgs...,
			)

			if daemon {
				proc.Stdout = nil
				proc.Stderr = nil
			} else {
				proc.Stdout = cmd.OutOrStdout()
				proc.Stderr = cmd.ErrOrStderr()
			}

			if err := proc.Start(); err != nil {
				return fmt.Errorf("start server: %w", err)
			}

			pid := proc.Process.Pid

			// Write PID file.
			if err := os.MkdirAll(
				filepath.Dir(pidFile), 0o750,
			); err != nil {
				return fmt.Errorf("create pid dir: %w", err)
			}
			if err := os.WriteFile(
				pidFile,
				[]byte(strconv.Itoa(pid)),
				0o644,
			); err != nil {
				return fmt.Errorf("write pid file: %w", err)
			}

			fmt.Fprintf(
				cmd.OutOrStdout(),
				"started routellm (pid %d, slug %s)\n", pid, slug,
			)

			// Health check: poll /health for up to 30 seconds.
			if err := waitForHealth(
				cfg.BaseURL, 30*time.Second,
			); err != nil {
				fmt.Fprintf(
					cmd.ErrOrStderr(),
					"warning: health check failed: %v\n", err,
				)
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "health check passed")
			}

			if daemon {
				// Detach — let the process run.
				_ = proc.Process.Release()
			} else {
				return proc.Wait()
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(
		&daemon, "daemon", false,
		"Run server in background",
	)
	cmd.Flags().StringVar(
		&pidPath, "pid", "",
		"Path to PID file (default: state dir / slug.pid)",
	)

	return cmd
}

// resolveConfigPath returns the config path from args or the default.
func resolveConfigPath(args []string) (string, error) {
	if len(args) > 0 {
		return args[0], nil
	}
	return defaultConfigPath()
}

// loadRouterYAML reads a router config YAML file into a RouterConfig.
func loadRouterYAML(path string) (routellm.RouterConfig, error) {
	cfg := routellm.DefaultRouterConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

// slugFromConfig derives a slug from the config for PID file naming.
func slugFromConfig(cfg routellm.RouterConfig) string {
	// Use first router name, or "default".
	if len(cfg.Routers) > 0 {
		return strings.ToLower(cfg.Routers[0])
	}
	return "default"
}

// buildServerArgs builds arguments for python -m routellm.openai_server.
func buildServerArgs(cfg routellm.RouterConfig) []string {
	args := []string{"-m", "routellm.openai_server"}

	if cfg.StrongModel != "" {
		args = append(args, "--strong-model", cfg.StrongModel)
	}
	if cfg.WeakModel != "" {
		args = append(args, "--weak-model", cfg.WeakModel)
	}
	for _, r := range cfg.Routers {
		args = append(args, "--routers", r)
	}
	if cfg.BaseURL != "" {
		// Extract host:port for --host / --port if needed.
		args = append(args, "--base-url", cfg.BaseURL)
	}
	return args
}

// waitForHealth polls baseURL/health until OK or timeout.
func waitForHealth(baseURL string, timeout time.Duration) error {
	url := strings.TrimRight(baseURL, "/") + "/health"
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}

	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf(
		"server at %s did not become healthy within %s", baseURL, timeout,
	)
}
