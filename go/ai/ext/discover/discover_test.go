package discover

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"hop.top/kit/go/ai/ext"
)

func TestScanFindsMatchingBinaries(t *testing.T) {
	dir := t.TempDir()
	writeExec(t, dir, "kit-foo", "#!/bin/sh\necho foo")
	writeExec(t, dir, "kit-bar", "#!/bin/sh\necho bar")
	writeExec(t, dir, "other-baz", "#!/bin/sh\necho baz")

	s := &Scanner{Prefix: "kit-", Paths: []string{dir}}
	found, err := s.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	names := nameSet(found)
	if _, ok := names["foo"]; !ok {
		t.Error("expected to find 'foo'")
	}
	if _, ok := names["bar"]; !ok {
		t.Error("expected to find 'bar'")
	}
	if _, ok := names["baz"]; ok {
		t.Error("should not find 'baz' (wrong prefix)")
	}
	if len(found) != 2 {
		t.Errorf("expected 2 results, got %d", len(found))
	}
}

func TestScanSkipsNonExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable bit not applicable on Windows")
	}

	dir := t.TempDir()
	// Create a file with the right prefix but no execute permission.
	path := filepath.Join(dir, "kit-noexec")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho no"), 0644); err != nil {
		t.Fatal(err)
	}

	s := &Scanner{Prefix: "kit-", Paths: []string{dir}}
	found, err := s.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(found) != 0 {
		t.Errorf("expected 0 results for non-executable, got %d", len(found))
	}
}

func TestScanDeduplicates(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	writeExec(t, dir1, "kit-dup", "#!/bin/sh\necho one")
	writeExec(t, dir2, "kit-dup", "#!/bin/sh\necho two")

	s := &Scanner{Prefix: "kit-", Paths: []string{dir1, dir2}}
	found, err := s.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(found) != 1 {
		t.Errorf("expected 1 result (dedup), got %d", len(found))
	}
	// First occurrence wins.
	if found[0].Path != filepath.Join(dir1, "kit-dup") {
		t.Errorf("expected first dir to win, got %s", found[0].Path)
	}
}

func TestScanEmptyPrefixErrors(t *testing.T) {
	s := &Scanner{Prefix: "", Paths: []string{t.TempDir()}}
	_, err := s.Scan()
	if err == nil {
		t.Fatal("expected error for empty prefix")
	}
}

func TestScanSkipsMissingDirs(t *testing.T) {
	s := &Scanner{
		Prefix: "kit-",
		Paths:  []string{"/nonexistent-path-abc123"},
	}
	found, err := s.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(found) != 0 {
		t.Errorf("expected 0 results, got %d", len(found))
	}
}

func TestInterrogateValidJSON(t *testing.T) {
	dir := t.TempDir()
	script := `#!/bin/sh
echo '{"name":"hello","version":"1.2.3","description":"a test","capabilities":["registry"]}'
`
	bin := filepath.Join(dir, "kit-hello")
	writeExec(t, dir, "kit-hello", script)

	meta, err := Interrogate(bin)
	if err != nil {
		t.Fatalf("Interrogate: %v", err)
	}
	if meta.Name != "hello" {
		t.Errorf("name = %q, want %q", meta.Name, "hello")
	}
	if meta.Version != "1.2.3" {
		t.Errorf("version = %q, want %q", meta.Version, "1.2.3")
	}
	if meta.Description != "a test" {
		t.Errorf("description = %q, want %q", meta.Description, "a test")
	}
}

func TestInterrogateMissingFlag(t *testing.T) {
	dir := t.TempDir()
	// Binary that exits non-zero for unknown flags.
	writeExec(t, dir, "kit-bad", "#!/bin/sh\nexit 1")
	bin := filepath.Join(dir, "kit-bad")

	_, err := Interrogate(bin)
	if err == nil {
		t.Fatal("expected error for binary that fails --ext-info")
	}
}

func TestInterrogateInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	writeExec(t, dir, "kit-garbled", "#!/bin/sh\necho 'not json'")
	bin := filepath.Join(dir, "kit-garbled")

	_, err := Interrogate(bin)
	if err == nil {
		t.Fatal("expected error for invalid JSON output")
	}
}

func TestFoundImplementsExtension(t *testing.T) {
	f := &Found{Name: "test", Path: "/bin/true", Version: "0.1.0"}
	meta := f.Meta()
	if meta.Name != "test" {
		t.Errorf("Meta().Name = %q, want %q", meta.Name, "test")
	}
	if meta.Version != "0.1.0" {
		t.Errorf("Meta().Version = %q, want %q", meta.Version, "0.1.0")
	}
	if !f.Capabilities().Has(ext.CapDiscover) {
		t.Errorf("expected CapDiscover, got %v", f.Capabilities())
	}
	if err := f.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
}

// --- helpers ---

func writeExec(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}
}

func nameSet(found []Found) map[string]struct{} {
	m := make(map[string]struct{}, len(found))
	for _, f := range found {
		m[f.Name] = struct{}{}
	}
	return m
}
