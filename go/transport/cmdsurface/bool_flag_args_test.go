package cmdsurface

import (
	"context"
	"reflect"
	"testing"

	"github.com/spf13/cobra"
)

// A bool flag the caller set to false must reach the command as
// --flag=false: for a default-true or tri-state flag, omitting it
// leaves the default in force and the caller's choice is lost.

func TestBuildArgs_BoolFlag(t *testing.T) {
	cases := []struct {
		name  string
		flags map[string]any
		want  []string
	}{
		{"true is bare flag", map[string]any{"work-item": true}, []string{"x", "--work-item"}},
		{"explicit false is --flag=false", map[string]any{"work-item": false}, []string{"x", "--work-item=false"}},
		{"absent emits nothing", nil, []string{"x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildArgs(Invocation{Path: []string{"x"}, Flags: tc.flags})
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("buildArgs = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestInProcessRunner_ExplicitFalseOverridesDefaultTrue(t *testing.T) {
	cases := []struct {
		name  string
		flags map[string]any
		want  bool
	}{
		{"absent keeps default", nil, true},
		{"explicit false wins", map[string]any{"work-item": false}, false},
		{"explicit true", map[string]any{"work-item": true}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got bool
			root := &cobra.Command{Use: "root"}
			leaf := &cobra.Command{
				Use: "list",
				RunE: func(cmd *cobra.Command, _ []string) error {
					got, _ = cmd.Flags().GetBool("work-item")
					return nil
				},
			}
			leaf.Flags().Bool("work-item", true, "")
			root.AddCommand(leaf)

			_, err := InProcessRunner(root).Run(context.Background(), Invocation{
				Path: []string{"list"}, Flags: tc.flags,
			})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if got != tc.want {
				t.Errorf("work-item = %v, want %v", got, tc.want)
			}
		})
	}
}
