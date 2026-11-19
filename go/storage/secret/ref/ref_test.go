package ref_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"hop.top/kit/go/storage/secret/ref"
)

type opCall struct{ ref, account string }

func fakeOp(table map[string]string, calls *[]opCall) ref.OpReader {
	return func(_ context.Context, r, account string) (string, error) {
		if calls != nil {
			*calls = append(*calls, opCall{r, account})
		}
		v, ok := table[r]
		if !ok {
			return "", errors.New("item not found")
		}
		return v, nil
	}
}

func lookupOf(m map[string]string) ref.Lookup {
	return func(_ context.Context, name string) (string, bool, error) {
		v, ok := m[name]
		return v, ok, nil
	}
}

func TestIsRef(t *testing.T) {
	for _, v := range []string{"op://vault/item/field", "  op://v/i/f", "secret:NAME"} {
		if !ref.IsRef(v) {
			t.Errorf("IsRef(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"", "plain", "op://", "secret:", "secret:  ", "https://op://x", "a secret:NAME", "postgres://localhost"} {
		if ref.IsRef(v) {
			t.Errorf("IsRef(%q) = true, want false", v)
		}
	}
}

func TestResolvePlainValueUnchanged(t *testing.T) {
	var calls []opCall
	o := ref.Opener{OpRead: fakeOp(nil, &calls)}
	for _, v := range []string{"secret123", "postgres://h/db", "", "  padded  "} {
		got, err := ref.Resolve(context.Background(), v, o)
		if err != nil || got != v {
			t.Errorf("Resolve(%q) = (%q, %v), want unchanged", v, got, err)
		}
	}
	if len(calls) != 0 {
		t.Errorf("plain values reached the vault: %v", calls)
	}
}

func TestResolveOnePassword(t *testing.T) {
	var calls []opCall
	o := ref.Opener{OpRead: fakeOp(map[string]string{"op://V/item-id/token": "tok"}, &calls)}
	got, err := ref.Resolve(context.Background(), "op://V/item-id/token", o)
	if err != nil || got != "tok" {
		t.Fatalf("got (%q, %v)", got, err)
	}
	if len(calls) != 1 || calls[0] != (opCall{"op://V/item-id/token", ""}) {
		t.Errorf("calls = %v", calls)
	}
}

func TestResolveOnePasswordAccountSelector(t *testing.T) {
	var calls []opCall
	o := ref.Opener{OpRead: fakeOp(map[string]string{"op://V/i/f": "x", "op://V/i/f?attribute=otp": "otp"}, &calls)}
	if got, err := ref.Resolve(context.Background(), "op://V/i/f?account=acme", o); err != nil || got != "x" {
		t.Fatalf("got (%q, %v)", got, err)
	}
	if got, err := ref.Resolve(context.Background(), "op://V/i/f?attribute=otp&account=a", o); err != nil || got != "otp" {
		t.Fatalf("got (%q, %v)", got, err)
	}
	want := []opCall{{"op://V/i/f", "acme"}, {"op://V/i/f?attribute=otp", "a"}}
	if len(calls) != 2 || calls[0] != want[0] || calls[1] != want[1] {
		t.Errorf("calls = %v, want %v", calls, want)
	}
}

func TestResolveSecretName(t *testing.T) {
	o := ref.Opener{Lookup: lookupOf(map[string]string{"slack_bot": "  xoxb  "})}
	got, err := ref.Resolve(context.Background(), "secret:slack_bot", o)
	if err != nil || got != "xoxb" {
		t.Fatalf("got (%q, %v)", got, err)
	}
}

func TestResolveFailsClosed(t *testing.T) {
	boom := func(context.Context, string) (string, bool, error) { return "", false, errors.New("locked") }
	cases := map[string]struct {
		value string
		o     ref.Opener
	}{
		"op error":        {"op://v/i/f", ref.Opener{OpRead: fakeOp(nil, nil)}},
		"op empty":        {"op://v/i/f", ref.Opener{OpRead: fakeOp(map[string]string{"op://v/i/f": ""}, nil)}},
		"op whitespace":   {"op://v/i/f", ref.Opener{OpRead: fakeOp(map[string]string{"op://v/i/f": " \n"}, nil)}},
		"secret missing":  {"secret:NOPE", ref.Opener{Lookup: lookupOf(nil)}},
		"secret no store": {"secret:NOPE", ref.Opener{}},
		"secret error":    {"secret:NOPE", ref.Opener{Lookup: boom}},
		"secret empty":    {"secret:K", ref.Opener{Lookup: lookupOf(map[string]string{"K": ""})}},
		"chained":         {"secret:K", ref.Opener{Lookup: lookupOf(map[string]string{"K": "op://v/i/f"})}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := ref.Resolve(context.Background(), c.value, c.o)
			if err == nil {
				t.Fatalf("want error, got %q", got)
			}
			if got != "" {
				t.Errorf("value returned alongside error: %q", got)
			}
			if !errors.Is(err, ref.ErrUnresolved) {
				t.Errorf("error does not wrap ErrUnresolved: %v", err)
			}
		})
	}
}

func TestResolveErrorNeverContainsValues(t *testing.T) {
	o := ref.Opener{Lookup: lookupOf(map[string]string{"K": "op://leaked-chain-value/i/f"})}
	_, err := ref.Resolve(context.Background(), "secret:K", o)
	if err == nil || strings.Contains(err.Error(), "leaked-chain-value") {
		t.Fatalf("chained error leaks resolved value: %v", err)
	}
}

func TestResolveMapNamesKeyNotValues(t *testing.T) {
	in := map[string]string{
		"BOS_AGENT_TOKEN": "op://v/i/f",
		"OTHER":           "plain-sibling-value-9f3",
	}
	out, err := ref.ResolveMap(context.Background(), in, ref.Opener{OpRead: fakeOp(nil, nil)})
	if err == nil {
		t.Fatal("want error")
	}
	if out != nil {
		t.Error("fail closed: no partial map")
	}
	if !strings.Contains(err.Error(), "BOS_AGENT_TOKEN") {
		t.Errorf("error does not name the key: %v", err)
	}
	if strings.Contains(err.Error(), "plain-sibling-value-9f3") {
		t.Errorf("error leaks a sibling value: %v", err)
	}
	if !errors.Is(err, ref.ErrUnresolved) {
		t.Errorf("error does not wrap ErrUnresolved: %v", err)
	}
}

func TestResolveMapMixed(t *testing.T) {
	in := map[string]string{"A": "plain", "B": "op://v/i/f", "C": "secret:n", "D": ""}
	o := ref.Opener{
		OpRead: fakeOp(map[string]string{"op://v/i/f": "from-op"}, nil),
		Lookup: lookupOf(map[string]string{"n": "from-store"}),
	}
	out, err := ref.ResolveMap(context.Background(), in, o)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"A": "plain", "B": "from-op", "C": "from-store", "D": ""}
	for k, v := range want {
		if out[k] != v {
			t.Errorf("%s = %q, want %q", k, out[k], v)
		}
	}
	if in["B"] != "op://v/i/f" {
		t.Error("input map was mutated")
	}
}

// --- default op reader: a stand-in `op` on PATH ---

func installFakeOp(t *testing.T, body string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell stand-in for op")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "op"), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

func TestExecOpReadArgsAndTrim(t *testing.T) {
	argFile := filepath.Join(t.TempDir(), "args")
	installFakeOp(t, `printf '%s\n' "$@" > `+argFile+`; printf 'value\n'`)
	got, err := ref.Resolve(context.Background(), "op://V/3r5q/token?account=ideacrafters", ref.Opener{})
	if err != nil || got != "value" {
		t.Fatalf("got (%q, %v)", got, err)
	}
	raw, _ := os.ReadFile(argFile)
	want := "read\n--no-newline\n--account\nideacrafters\nop://V/3r5q/token\n"
	if string(raw) != want {
		t.Errorf("args = %q, want %q", raw, want)
	}
}

func TestExecOpReadFailure(t *testing.T) {
	installFakeOp(t, `echo "[ERROR] no such item" >&2; exit 1`)
	_, err := ref.Resolve(context.Background(), "op://V/i/f", ref.Opener{})
	if err == nil || !errors.Is(err, ref.ErrUnresolved) {
		t.Fatalf("want ErrUnresolved, got %v", err)
	}
}

func TestExecOpReadMissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := ref.Resolve(context.Background(), "op://V/i/f", ref.Opener{})
	if err == nil || !errors.Is(err, ref.ErrUnresolved) {
		t.Fatalf("want ErrUnresolved, got %v", err)
	}
}
