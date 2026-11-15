package cmdsurface

import (
	"context"
	"reflect"
	"testing"

	"github.com/spf13/cobra"
)

// A JSON array flag value (MCP tools/call, SSE repeated flag.<name>)
// reaches the runner as []any / []string. It must become one
// --flag=<element> per element, not one --flag=[a b] value.

func TestBuildArgs_ArrayFlagRepeatsPerElement(t *testing.T) {
	cases := []struct {
		name string
		flag any
		want []string
	}{
		{"[]any strings", []any{"op_1", "op_2"}, []string{"x", "--operation=op_1", "--operation=op_2"}},
		{"[]string", []string{"a,b", "c"}, []string{"x", "--operation=a,b", "--operation=c"}},
		{"single element", []any{"op_1"}, []string{"x", "--operation=op_1"}},
		{"numbers and bools", []any{float64(1), float64(2.5), true}, []string{"x", "--operation=1", "--operation=2.5", "--operation=true"}},
		{"empty array emits nothing", []any{}, []string{"x"}},
		{"empty []string emits nothing", []string{}, []string{"x"}},
		{"scalar unchanged", "op_1", []string{"x", "--operation=op_1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildArgs(Invocation{Path: []string{"x"}, Flags: map[string]any{"operation": tc.flag}})
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("buildArgs = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestInProcessRunner_ArrayFlagReachesSliceAndArray(t *testing.T) {
	var slice, array []string
	root := &cobra.Command{Use: "root"}
	leaf := &cobra.Command{
		Use: "go",
		RunE: func(cmd *cobra.Command, _ []string) error {
			slice, _ = cmd.Flags().GetStringSlice("s")
			array, _ = cmd.Flags().GetStringArray("a")
			return nil
		},
	}
	leaf.Flags().StringSlice("s", nil, "")
	leaf.Flags().StringArray("a", nil, "")
	root.AddCommand(leaf)

	_, err := InProcessRunner(root).Run(context.Background(), Invocation{
		Path:  []string{"go"},
		Flags: map[string]any{"s": []any{"op_1", "op_2"}, "a": []any{"x,y", "z"}},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if want := []string{"op_1", "op_2"}; !reflect.DeepEqual(slice, want) {
		t.Errorf("StringSlice = %q, want %q", slice, want)
	}
	if want := []string{"x,y", "z"}; !reflect.DeepEqual(array, want) {
		t.Errorf("StringArray = %q, want %q", array, want)
	}
}
