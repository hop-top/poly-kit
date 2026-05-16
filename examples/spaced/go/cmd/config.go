package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
	"hop.top/kit/go/core/xdg"
)

// SpacedConfig holds user preferences loaded from ~/.config/spaced/config.yaml.
type SpacedConfig struct {
	DefaultPad      string `yaml:"default_pad"`
	DefaultVehicle  string `yaml:"default_vehicle"`
	DefaultOrbit    string `yaml:"default_orbit"`
	FavoriteMission string `yaml:"favorite_mission"`
}

// LoadConfig loads spaced config from XDG config dir. Missing file = zero config.
func LoadConfig() SpacedConfig {
	var cfg SpacedConfig
	dir, err := xdg.ConfigDir("spaced")
	if err != nil {
		return cfg
	}
	f, err := os.Open(filepath.Join(dir, "config.yaml"))
	if err != nil {
		return cfg
	}
	defer f.Close()
	_ = yaml.NewDecoder(f).Decode(&cfg)
	return cfg
}

// ConfigShowCmd returns the `config show` command.
func ConfigShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "config",
		Short: "Show spaced configuration and paths",
		Run: func(cmd *cobra.Command, args []string) {
			cfg := LoadConfig()
			fmt.Println()
			fmt.Println("  ── SPACED CONFIG ──────────────────────────────────")
			fmt.Printf("  Default Pad      : %s\n", valOrDash(cfg.DefaultPad))
			fmt.Printf("  Default Vehicle  : %s\n", valOrDash(cfg.DefaultVehicle))
			fmt.Printf("  Default Orbit    : %s\n", valOrDash(cfg.DefaultOrbit))
			fmt.Printf("  Favorite Mission : %s\n", valOrDash(cfg.FavoriteMission))
			fmt.Println()
			fmt.Println("  ── PATHS ──────────────────────────────────────────")
			PrintPaths()
			fmt.Println()
		},
	}
}

func valOrDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// ConfigDir returns the XDG config directory for spaced.
func ConfigDir() string {
	dir, err := xdg.ConfigDir("spaced")
	if err != nil {
		return ""
	}
	return dir
}

// DataDir returns the XDG data directory for spaced.
func DataDir() string {
	dir, err := xdg.DataDir("spaced")
	if err != nil {
		return ""
	}
	return dir
}

// PrintPaths prints resolved XDG paths.
func PrintPaths() {
	fmt.Printf("  Config : %s\n", ConfigDir())
	fmt.Printf("  Data   : %s\n", DataDir())
}
