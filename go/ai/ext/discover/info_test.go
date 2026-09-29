package discover

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// extInfoWithExtras is an --ext-info payload carrying host-specific fields
// beyond the four the protocol defines: a nested object, an array of
// objects, and a scalar. Discovery must hand all of them to the host.
const extInfoWithExtras = `{
  "name": "grep",
  "version": "0.3.0",
  "description": "search files",
  "capabilities": ["discover", "hook"],
  "parameters": {
    "type": "object",
    "properties": {"path": {"type": "string"}, "pattern": {"type": "string"}},
    "required": ["pattern"]
  },
  "annotations": [{"arg": "path", "kind": "fs-path", "op": "read"}],
  "tier": 2
}`

// hostFields is what a host defines for the fields it interprets.
type hostFields struct {
	Parameters  json.RawMessage `json:"parameters"`
	Annotations []struct {
		Arg  string `json:"arg"`
		Kind string `json:"kind"`
		Op   string `json:"op"`
	} `json:"annotations"`
	Tier int `json:"tier"`
}

func skipOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts not supported on Windows")
	}
}

// writeExtInfoBin writes a plugin that prints payload on --ext-info and
// appends one line to countFile per invocation.
func writeExtInfoBin(t *testing.T, dir, name, payload, countFile string) string {
	t.Helper()
	script := "#!/bin/sh\n" +
		"echo x >> '" + countFile + "'\n" +
		"if [ \"$1\" = \"--ext-info\" ]; then\ncat <<'EOF'\n" + payload + "\nEOF\nfi\n"
	writeExec(t, dir, name, script)
	return filepath.Join(dir, name)
}

func invocations(t *testing.T, countFile string) int {
	t.Helper()
	b, err := os.ReadFile(countFile)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(b), "x")
}

func assertSameJSON(t *testing.T, got []byte, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("Raw is not valid JSON: %v (%q)", err, got)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("Raw payload mismatch\n got: %s\nwant: %s", got, want)
	}
}

func assertHostFields(t *testing.T, h hostFields) {
	t.Helper()
	var schema struct {
		Type       string                     `json:"type"`
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(h.Parameters, &schema); err != nil {
		t.Fatalf("parameters not decodable: %v (%q)", err, h.Parameters)
	}
	if schema.Type != "object" || len(schema.Properties) != 2 ||
		!reflect.DeepEqual(schema.Required, []string{"pattern"}) {
		t.Errorf("parameters = %s, want the emitted schema", h.Parameters)
	}
	if len(h.Annotations) != 1 || h.Annotations[0].Arg != "path" ||
		h.Annotations[0].Kind != "fs-path" || h.Annotations[0].Op != "read" {
		t.Errorf("annotations = %+v, want [{path fs-path read}]", h.Annotations)
	}
	if h.Tier != 2 {
		t.Errorf("tier = %d, want 2", h.Tier)
	}
}

func TestInterrogateInfoPreservesExtraFields(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	count := filepath.Join(dir, "count")
	bin := writeExtInfoBin(t, dir, "kit-grep", extInfoWithExtras, count)

	info, err := InterrogateInfo(bin)
	if err != nil {
		t.Fatalf("InterrogateInfo: %v", err)
	}
	if info.Metadata.Name != "grep" || info.Metadata.Version != "0.3.0" ||
		info.Metadata.Description != "search files" {
		t.Errorf("Metadata = %+v", info.Metadata)
	}
	if !reflect.DeepEqual(info.Capabilities, []string{"discover", "hook"}) {
		t.Errorf("Capabilities = %v, want [discover hook]", info.Capabilities)
	}
	assertSameJSON(t, info.Raw, extInfoWithExtras)

	var h hostFields
	if err := info.Decode(&h); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	assertHostFields(t, h)

	if n := invocations(t, count); n != 1 {
		t.Errorf("binary executed %d times, want 1", n)
	}
}

func TestInterrogateUnchangedByExtraFields(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	bin := writeExtInfoBin(t, dir, "kit-grep", extInfoWithExtras, filepath.Join(dir, "count"))

	meta, err := Interrogate(bin)
	if err != nil {
		t.Fatalf("Interrogate: %v", err)
	}
	if meta.Name != "grep" || meta.Version != "0.3.0" || meta.Description != "search files" {
		t.Errorf("Interrogate = %+v", meta)
	}
}

func TestFoundInfoAfterEnrich(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	count := filepath.Join(dir, "count")
	writeExtInfoBin(t, dir, "kit-grep", extInfoWithExtras, count)

	found, err := (&Scanner{Prefix: "kit-", Paths: []string{dir}}).Scan()
	if err != nil || len(found) != 1 {
		t.Fatalf("Scan = %v, %v", found, err)
	}
	f := found[0]

	if f.Info() != nil {
		t.Fatal("Info() before Enrich should be nil")
	}
	if n := invocations(t, count); n != 0 {
		t.Fatalf("Scan executed the binary %d times, want 0", n)
	}

	if err := f.Enrich(); err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if n := invocations(t, count); n != 1 {
		t.Errorf("Enrich executed the binary %d times, want 1", n)
	}

	info := f.Info()
	if info == nil {
		t.Fatal("Info() after Enrich is nil")
	}
	if got := f.Meta(); got != info.Metadata {
		t.Errorf("Meta() = %+v, Info().Metadata = %+v; want equal", got, info.Metadata)
	}
	if f.Version != "0.3.0" {
		t.Errorf("Version = %q, want 0.3.0", f.Version)
	}
	assertSameJSON(t, info.Raw, extInfoWithExtras)

	var h hostFields
	if err := info.Decode(&h); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	assertHostFields(t, h)

	if n := invocations(t, count); n != 1 {
		t.Errorf("reading Info executed the binary again: %d invocations, want 1", n)
	}
}

func TestFoundInfoNilAfterFailedEnrich(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	writeExec(t, dir, "kit-bad", "#!/bin/sh\nexit 1")
	f := &Found{Name: "bad", Path: filepath.Join(dir, "kit-bad")}

	if err := f.Enrich(); err == nil {
		t.Fatal("expected Enrich error")
	}
	if f.Info() != nil {
		t.Error("Info() after failed Enrich should be nil")
	}
	if got := f.Meta(); got.Name != "bad" {
		t.Errorf("Meta().Name = %q, want synthesized %q", got.Name, "bad")
	}
}

func TestInfoRawIsDefensiveCopy(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	writeExtInfoBin(t, dir, "kit-grep", extInfoWithExtras, filepath.Join(dir, "count"))
	f := &Found{Name: "grep", Path: filepath.Join(dir, "kit-grep")}
	if err := f.Enrich(); err != nil {
		t.Fatalf("Enrich: %v", err)
	}

	raw := f.Info().Raw
	for i := range raw {
		raw[i] = ' '
	}
	assertSameJSON(t, f.Info().Raw, extInfoWithExtras)
}

func TestInfoDecodeOnNilInfoErrors(t *testing.T) {
	f := &Found{Name: "never-enriched", Path: "/nonexistent"}
	var h hostFields
	if err := f.Info().Decode(&h); err == nil {
		t.Fatal("Decode on nil Info should return an error, not succeed")
	}
}
