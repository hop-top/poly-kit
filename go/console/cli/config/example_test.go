package config_test

import (
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"hop.top/kit/go/console/cli/config"
)

func ExampleRegisterPathSubcommands() {
	resolver := func(cwd string) []config.ResolvedPath {
		return []config.ResolvedPath{
			{Path: filepath.Join(cwd, ".demo.yaml"), Source: "cwd", Exists: false},
			{Path: "/home/me/.config/demo/config.yaml", Source: "user", Exists: true},
			{Path: "<defaults>", Source: "default", Exists: true},
		}
	}

	cfg := &cobra.Command{Use: "config", Short: "Inspect demo configuration"}
	config.RegisterPathSubcommands(cfg, "demo", config.WithResolver(resolver))
	cfg.SetOut(os.Stdout)

	cfg.SetArgs([]string{"path", "--from", "/work"})
	_ = cfg.Execute()
	cfg.SetArgs([]string{"paths", "--from", "/work"})
	_ = cfg.Execute()
	// Output:
	// /home/me/.config/demo/config.yaml
	// /work/.demo.yaml
	// /home/me/.config/demo/config.yaml
	// <defaults>
}
