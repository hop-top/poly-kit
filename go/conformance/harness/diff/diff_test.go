package diff

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeCassette emits a {adapter}-{fp}.{kind}.yaml pair into dir.
func writeCassette(t *testing.T, dir, adapter, fp, kind, recorded, errStr, payload string) {
	t.Helper()
	body := strings.Join([]string{
		`xrr: "1"`,
		"adapter: " + adapter,
		"fingerprint: " + fp,
		"recorded_at: " + recorded,
		"error: " + errStr,
		"payload:",
		payload,
	}, "\n")
	path := filepath.Join(dir, adapter+"-"+fp+"."+kind+".yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestEqualDirs(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	writeCassette(t, a, "http", "aaaa1111", "req", "2026-05-11T00:00:00Z", `""`,
		"  method: GET\n  url: http://example/x")
	writeCassette(t, a, "http", "aaaa1111", "resp", "2026-05-11T00:00:00Z", `""`,
		"  status: 200\n  body: ok")
	writeCassette(t, b, "http", "aaaa1111", "req", "2026-05-11T22:33:44Z", `""`,
		"  method: GET\n  url: http://example/x")
	writeCassette(t, b, "http", "aaaa1111", "resp", "2026-05-11T22:33:44Z", `""`,
		"  status: 200\n  body: ok")

	d, err := Cassettes(a, b)
	if err != nil {
		t.Fatalf("Cassettes: %v", err)
	}
	if !d.Empty() {
		t.Errorf("expected empty diff, got %d entries: %s", len(d.Entries), d.Format(nil))
	}
}

func TestAdded(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	// b has one extra POST.
	writeCassette(t, b, "http", "bbbb2222", "req", "2026-05-11T00:00:00Z", `""`,
		"  method: POST\n  url: http://example/missions")
	writeCassette(t, b, "http", "bbbb2222", "resp", "2026-05-11T00:00:00Z", `""`,
		"  status: 201")

	d, err := Cassettes(a, b)
	if err != nil {
		t.Fatalf("Cassettes: %v", err)
	}
	if d.Empty() {
		t.Fatalf("expected non-empty diff")
	}
	if d.Entries[0].Kind != KindAdded {
		t.Errorf("want added, got %s", d.Entries[0].Kind)
	}
	if !strings.Contains(d.Entries[0].Summary, "POST") {
		t.Errorf("summary missing POST: %q", d.Entries[0].Summary)
	}
}

func TestModified(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	// Same fingerprint, different resp body.
	writeCassette(t, a, "http", "cccc3333", "req", "2026-05-11T00:00:00Z", `""`,
		"  method: GET\n  url: http://example/list")
	writeCassette(t, a, "http", "cccc3333", "resp", "2026-05-11T00:00:00Z", `""`,
		"  status: 200\n  body: '[]'")
	writeCassette(t, b, "http", "cccc3333", "req", "2026-05-11T00:00:01Z", `""`,
		"  method: GET\n  url: http://example/list")
	writeCassette(t, b, "http", "cccc3333", "resp", "2026-05-11T00:00:01Z", `""`,
		"  status: 200\n  body: '[1]'")

	d, err := Cassettes(a, b)
	if err != nil {
		t.Fatalf("Cassettes: %v", err)
	}
	if d.Empty() {
		t.Fatalf("expected modified diff entry")
	}
	if d.Entries[0].Kind != KindModified {
		t.Errorf("want modified, got %s", d.Entries[0].Kind)
	}
	if !strings.Contains(d.Entries[0].ALine, "[]") || !strings.Contains(d.Entries[0].BLine, "[1]") {
		t.Errorf("modified lines missing payloads: A=%q B=%q",
			d.Entries[0].ALine, d.Entries[0].BLine)
	}
}

func TestList(t *testing.T) {
	dir := t.TempDir()
	writeCassette(t, dir, "sql", "dddd4444", "req", "2026-05-11T00:00:00Z", `""`,
		"  query: SELECT 1\n  args: []")
	writeCassette(t, dir, "sql", "dddd4444", "resp", "2026-05-11T00:00:00Z", `""`,
		"  rows: []")

	got, err := List(dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d interactions, want 1", len(got))
	}
	if got[0].Adapter != "sql" {
		t.Errorf("adapter: %q", got[0].Adapter)
	}
	if got[0].Summary != "SELECT 1" {
		t.Errorf("summary: %q", got[0].Summary)
	}
}

func TestFormatReadable(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	writeCassette(t, b, "sql", "eeee5555", "req", "2026-05-11T00:00:00Z", `""`,
		"  query: INSERT INTO t VALUES (1)\n  args: []")
	writeCassette(t, b, "sql", "eeee5555", "resp", "2026-05-11T00:00:00Z", `""`,
		"  affected: 1")
	d, err := Cassettes(a, b)
	if err != nil {
		t.Fatalf("Cassettes: %v", err)
	}
	out := d.Format(nil)
	if !strings.Contains(out, "cassette diff non-empty") {
		t.Errorf("missing header: %q", out)
	}
	if !strings.Contains(out, "+ sql INSERT") {
		t.Errorf("missing entry: %q", out)
	}
}
