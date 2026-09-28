package cmdsurface

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// An invocation's Args are positional arguments on every surface: a
// value that happens to start with "-" is still an argument, never a
// flag. The argv a runner hands the command marks the end of options
// before them, so neither cobra nor a child process reads one as a
// flag.

func TestBuildArgs_EndsOptionsBeforePositionals(t *testing.T) {
	cases := []struct {
		name string
		inv  Invocation
		want []string
	}{
		{
			name: "flags only: no marker",
			inv:  Invocation{Path: []string{"echo"}, Flags: map[string]any{"loud": true}},
			want: []string{"echo", "--loud"},
		},
		{
			name: "args follow the marker",
			inv: Invocation{
				Path:  []string{"echo"},
				Flags: map[string]any{"loud": true},
				Args:  []string{"-x", "plain"},
			},
			want: []string{"echo", "--loud", "--", "-x", "plain"},
		},
		{
			name: "a leaf that parses its own argv gets them verbatim",
			inv:  Invocation{Path: []string{"plug"}, Args: []string{"list", "-x"}, ownArgv: true},
			want: []string{"plug", "list", "-x"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildArgs(tc.inv); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("buildArgs = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestInProcessRunner_DashLeadingArgsArePositional(t *testing.T) {
	inv := Invocation{Path: []string{"echo"}, Args: []string{"-x", "--loud", "--"}}

	t.Run("run", func(t *testing.T) {
		res, err := InProcessRunner(newFakeTree()).Run(context.Background(), inv)
		if err != nil {
			t.Fatalf("Run err: %v", err)
		}
		if res.ExitCode != 0 || res.Stdout != "-x --loud --" {
			t.Errorf("got exit=%d stdout=%q stderr=%q, want exit=0 stdout=%q",
				res.ExitCode, res.Stdout, res.Stderr, "-x --loud --")
		}
	})
	t.Run("stream", func(t *testing.T) {
		ch := make(chan Event, 16)
		if err := InProcessRunner(newFakeTree()).Stream(context.Background(), inv, ch); err != nil {
			t.Fatalf("Stream err: %v", err)
		}
		var res *Result
		for ev := range ch {
			if ev.Kind == "done" {
				res, _ = ev.Data.(*Result)
			}
		}
		if res == nil || res.ExitCode != 0 || res.Stdout != "-x --loud --" {
			t.Errorf("done result = %+v, want exit=0 stdout=%q", res, "-x --loud --")
		}
	})
}

// ownArgvTree is a root with one leaf that disables flag parsing, the
// shape of a command that forwards its argv to another program (kit's
// plugin dispatch). It prints the args it received, "|"-joined.
func ownArgvTree() *cobra.Command {
	root := &cobra.Command{Use: "root"}
	root.AddCommand(&cobra.Command{
		Use:                "plug",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprint(cmd.OutOrStdout(), strings.Join(args, "|"))
			return nil
		},
	})
	return root
}

// A command that parses its own argv receives Args exactly as sent:
// kit parses none of them, so none can be misread, and a separator
// would reach the forwarded program as an argument of its own.
func TestInProcessRunner_OwnArgvLeafGetsArgsVerbatim(t *testing.T) {
	res, err := InProcessRunner(ownArgvTree()).Run(context.Background(),
		Invocation{Path: []string{"plug"}, Args: []string{"list", "-x"}})
	if err != nil {
		t.Fatalf("Run err: %v", err)
	}
	if res.Stdout != "list|-x" {
		t.Errorf("Stdout=%q want %q", res.Stdout, "list|-x")
	}
}

// The subprocess runner holds no tree; the bridge tells it, through
// the admitted invocation, which leaf parses its own argv.
func TestBridge_AdmitMarksOwnArgvLeaves(t *testing.T) {
	root := ownArgvTree()
	root.AddCommand(&cobra.Command{Use: "echo", RunE: func(*cobra.Command, []string) error { return nil }})
	b := New(root)

	for _, tc := range []struct {
		path string
		want []string
	}{
		{"plug", []string{"plug", "list", "-x"}},
		{"echo", []string{"echo", "--", "list", "-x"}},
	} {
		adm, err := b.Admit(context.Background(),
			Invocation{Path: []string{tc.path}, Args: []string{"list", "-x"}})
		if err != nil {
			t.Fatalf("Admit %s: %v", tc.path, err)
		}
		if got := buildArgs(adm.Invocation()); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s argv = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestSubprocessRunner_EndsOptionsBeforePositionals(t *testing.T) {
	sh := findSh(t)
	if sh == "" {
		t.Skip("no POSIX shell available")
	}
	// The script rides in Path, ahead of the marker; "$@" is what
	// follows it.
	res, err := SubprocessRunner(sh).Run(context.Background(), Invocation{
		Path: []string{"-c", `printf '%s|' "$@"`, "sh"},
		Args: []string{"-x"},
	})
	if err != nil {
		t.Fatalf("Run err: %v", err)
	}
	if res.Stdout != "--|-x|" {
		t.Errorf("Stdout=%q want %q", res.Stdout, "--|-x|")
	}
}

func TestLibInvokeArgs_TokensAfterMarkerStayPositional(t *testing.T) {
	b := New(newFakeTree())
	res, err := InvokeArgs(context.Background(), b, []string{"echo", "--", "--loud", "-x"})
	if err != nil {
		t.Fatalf("InvokeArgs err: %v", err)
	}
	if res.ExitCode != 0 || res.Stdout != "--loud -x" {
		t.Errorf("got exit=%d stdout=%q stderr=%q, want stdout=%q",
			res.ExitCode, res.Stdout, res.Stderr, "--loud -x")
	}
}
