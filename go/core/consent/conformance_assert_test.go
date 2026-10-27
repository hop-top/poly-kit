package consent

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

// Assertion helpers for the contracts/consent-v1 runner.

func expectErr(t *testing.T, where string, err error, kind string) {
	t.Helper()
	if kind == "" {
		if err != nil {
			t.Fatalf("%s: unexpected error %v", where, err)
		}
		return
	}
	if err == nil {
		t.Fatalf("%s: want error kind %s, got nil", where, kind)
	}
	if got := KindOf(err); string(got) != kind {
		t.Fatalf("%s: error kind %q, want %q (%v)", where, got, kind, err)
	}
}

func expectOutcome(t *testing.T, where string, err error, ex *expectJSON) {
	t.Helper()
	if ex.ErrorKind != "" {
		expectErr(t, where, err, ex.ErrorKind)
		var re *RefusedError
		if errors.As(err, &re) {
			t.Fatalf("%s: a store failure must not read as a refusal: %v", where, err)
		}
		return
	}
	if ex.OK != nil && *ex.OK {
		if err != nil {
			t.Fatalf("%s: unexpected error %v", where, err)
		}
		return
	}
	var re *RefusedError
	if !errors.As(err, &re) {
		t.Fatalf("%s: want refusal, got %v", where, err)
	}
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("%s: refusal does not match ErrRefused", where)
	}
	ce := re.AsCLIError()
	got := refusedJSON{Reason: string(re.Reason), Code: ce.Code, ExitCode: ce.ExitCode, Transience: ce.Transience}
	if got != *ex.Refused {
		t.Fatalf("%s: refusal %+v, want %+v", where, got, *ex.Refused)
	}
}

func assertFile(t *testing.T, what, path string, want json.RawMessage) {
	t.Helper()
	b, err := os.ReadFile(path)
	if string(want) == "null" {
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s: want no file at %s, got err=%v body=\n%s", what, path, err, b)
		}
		return
	}
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	var v any
	if err := yaml.Unmarshal(b, &v); err != nil {
		t.Fatalf("%s: parse: %v", what, err)
	}
	assertJSONEqual(t, what, mustJSON(t, v), want)
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// assertJSONEqual compares two JSON documents as values.
func assertJSONEqual(t *testing.T, what string, got, want json.RawMessage) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &w); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Fatalf("%s:\n got %s\nwant %s", what, got, want)
	}
}
