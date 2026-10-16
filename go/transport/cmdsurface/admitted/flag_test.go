package admitted_test

import (
	"errors"
	"testing"

	"github.com/spf13/pflag"

	"hop.top/kit/go/transport/cmdsurface/admitted"
)

func TestResetFlagToDefault_ScalarAndChanged(t *testing.T) {
	fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
	fs.String("name", "def", "")
	if err := fs.Parse([]string{"--name", "set"}); err != nil {
		t.Fatal(err)
	}
	f := fs.Lookup("name")
	admitted.ResetFlagToDefault(f)
	if f.Value.String() != "def" || f.Changed {
		t.Fatalf("after reset: value %q changed %v, want def false", f.Value.String(), f.Changed)
	}
	admitted.ResetFlagToDefault(nil) // no-op
}

func TestResetFlagToDefault_SliceIsReplacedNotAppended(t *testing.T) {
	fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
	fs.StringSlice("tag", []string{"a", "b"}, "")
	if err := fs.Parse([]string{"--tag", "x", "--tag", "y"}); err != nil {
		t.Fatal(err)
	}
	f := fs.Lookup("tag")
	admitted.ResetFlagToDefault(f)
	got, _ := fs.GetStringSlice("tag")
	if len(got) != 2 || got[0] != "a" || got[1] != "b" || f.Changed {
		t.Fatalf("after reset: %v changed %v, want [a b] false", got, f.Changed)
	}
}

func TestResetFlagToDefault_CallbackFlagKeepsValueClearsChanged(t *testing.T) {
	fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
	calls := 0
	fs.Func("hook", "", func(string) error { calls++; return nil })
	if err := fs.Parse([]string{"--hook", "v"}); err != nil {
		t.Fatal(err)
	}
	f := fs.Lookup("hook")
	admitted.ResetFlagToDefault(f)
	if calls != 1 {
		t.Fatalf("reset invoked the adopter's callback: %d calls, want 1", calls)
	}
	if f.Changed {
		t.Fatal("Changed not cleared on a callback flag")
	}
}

// stubborn is a Value whose String form does not parse back through
// Set, like pflag's nil IP printing "<nil>".
type stubborn struct{ v string }

func (s *stubborn) String() string { return s.v }
func (s *stubborn) Type() string   { return "stubborn" }
func (s *stubborn) Set(v string) error {
	if v == "<bad>" {
		return errors.New("cannot parse <bad>")
	}
	s.v = v
	return nil
}

func TestRestoreFlag_NonRoundTripFallsBackToDefault(t *testing.T) {
	fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
	val := &stubborn{v: "ok"}
	fs.Var(val, "x", "")
	f := fs.Lookup("x")
	val.v = "<bad>"
	st := admitted.CaptureFlag(f)
	val.v = "other"
	admitted.RestoreFlag(f, &st, false)
	if val.v != "ok" {
		t.Fatalf("value = %q, want the default ok", val.v)
	}
}

func TestCaptureRestore_ReturnsToBaseline(t *testing.T) {
	fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
	fs.String("name", "def", "")
	fs.StringSlice("tag", nil, "")
	if err := fs.Parse([]string{"--name", "base", "--tag", "p"}); err != nil {
		t.Fatal(err)
	}
	name, tag := fs.Lookup("name"), fs.Lookup("tag")
	nameSt, tagSt := admitted.CaptureFlag(name), admitted.CaptureFlag(tag)
	if err := fs.Parse([]string{"--name", "other", "--tag", "q"}); err != nil {
		t.Fatal(err)
	}

	admitted.RestoreFlag(name, &nameSt, false)
	admitted.RestoreFlag(tag, &tagSt, false)
	if name.Value.String() != "base" || !name.Changed {
		t.Fatalf("name = %q changed %v, want base true", name.Value.String(), name.Changed)
	}
	if got, _ := fs.GetStringSlice("tag"); len(got) != 1 || got[0] != "p" {
		t.Fatalf("tag = %v, want [p]", got)
	}

	// A slice the coming invocation passes is emptied, so its values
	// do not stack on the baseline's.
	admitted.RestoreFlag(tag, &tagSt, true)
	if got, _ := fs.GetStringSlice("tag"); len(got) != 0 {
		t.Fatalf("passed tag = %v, want empty", got)
	}
}
