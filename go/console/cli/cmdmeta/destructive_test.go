package cmdmeta

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestInferredDestructive(t *testing.T) {
	cases := []struct {
		name string
		ann  map[string]string
		want bool
	}{
		{"delete", nil, true},
		{"rm", map[string]string{}, true},
		{"purge", map[string]string{KeySideEffect: ""}, true},
		{"drop", map[string]string{KeySideEffect: "read"}, false},
		{"destroy", map[string]string{KeySideEffect: "bogus"}, false},
		{"list", nil, false},
		{"delete-all", nil, false},
	}
	for _, c := range cases {
		cmd := &cobra.Command{Use: c.name, Annotations: c.ann}
		if got := InferredDestructive(cmd); got != c.want {
			t.Errorf("%s %v: got %v want %v", c.name, c.ann, got, c.want)
		}
	}
	if InferredDestructive(nil) {
		t.Error("nil command inferred destructive")
	}
	if len(DestructiveNames()) != 6 {
		t.Errorf("table = %v", DestructiveNames())
	}
}
