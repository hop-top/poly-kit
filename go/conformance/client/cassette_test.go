package client

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestPackIsDeterministic packs the same fixture twice in a row and
// asserts that the byte output (and therefore the Idempotency-Key) is
// identical. This is the contract that lets retries collapse server-
// side. Regression-guards design.md §2.
func TestPackIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "manifest.yaml", "schema_version: \"1\"\nscenario_id: t.deterministic\n")
	writeFixture(t, dir, "steps/launch/cassette/http.yaml", "url: https://example.com\n")
	writeFixture(t, dir, "steps/launch/stdout.bin", "hello\n")
	writeFixture(t, dir, "steps/launch/result.json", "{\"exit_code\":0}\n")
	writeFixture(t, dir, "steps/replay/cassette/http.yaml", "url: https://example.com/2\n")

	m := &Manifest{
		SchemaVersion: "1",
		ScenarioID:    "t.deterministic",
		RecordedAt:    time.Unix(1700000000, 0).UTC(),
	}

	body1, key1, err := Pack(dir, m, 0)
	if err != nil {
		t.Fatalf("first Pack: %v", err)
	}
	defer body1.Close()
	raw1, _ := io.ReadAll(body1)

	body2, key2, err := Pack(dir, m, 0)
	if err != nil {
		t.Fatalf("second Pack: %v", err)
	}
	defer body2.Close()
	raw2, _ := io.ReadAll(body2)

	if !bytes.Equal(raw1, raw2) {
		t.Fatalf("Pack is not deterministic: byte length first=%d second=%d", len(raw1), len(raw2))
	}
	if key1 != key2 {
		t.Fatalf("Idempotency-Key changed across Pack calls: %q vs %q", key1, key2)
	}
	if !strings.HasPrefix(key1, "sha256:") {
		t.Fatalf("Idempotency-Key missing sha256: prefix: %q", key1)
	}
}

// TestPackContainsManifest verifies that the produced archive begins
// with manifest.yaml so svc-side stream parsers can read it without
// buffering the whole body.
func TestPackContainsManifest(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "steps/x/cassette/keep", "")

	m := &Manifest{SchemaVersion: "1", ScenarioID: "t.contains"}
	body, _, err := Pack(dir, m, 0)
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	defer body.Close()
	raw, _ := io.ReadAll(body)

	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	tr := tar.NewReader(gz)
	hdr, err := tr.Next()
	if err != nil {
		t.Fatalf("tar.Next: %v", err)
	}
	if hdr.Name != "manifest.yaml" {
		t.Fatalf("first tar entry is %q, want manifest.yaml", hdr.Name)
	}
	// Read the manifest body, ensure it parses + has the scenario id.
	manifestBytes, _ := io.ReadAll(tr)
	if !strings.Contains(string(manifestBytes), "t.contains") {
		t.Fatalf("manifest body missing scenario id: %s", manifestBytes)
	}
}

// TestPackTooLarge asserts ErrCassetteTooLarge fires before any body
// reaches the network.
func TestPackTooLarge(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "big.bin", strings.Repeat("X", 1024))
	m := &Manifest{SchemaVersion: "1", ScenarioID: "t.toolarge"}
	_, _, err := Pack(dir, m, 32)
	if err == nil {
		t.Fatalf("Pack succeeded with maxBytes=32, want ErrCassetteTooLarge")
	}
	if !errIsCassetteTooLarge(err) {
		t.Fatalf("Pack returned %v, want ErrCassetteTooLarge", err)
	}
}

func TestLoadManifest(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "manifest.yaml", "schema_version: \"1\"\nscenario_id: t.load\nstory_path: s.yaml\n")
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m.ScenarioID != "t.load" {
		t.Fatalf("ScenarioID = %q, want t.load", m.ScenarioID)
	}
	if m.StoryPath != "s.yaml" {
		t.Fatalf("StoryPath = %q, want s.yaml", m.StoryPath)
	}
}

func TestLoadManifestMissing(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadManifest(dir)
	if err == nil {
		t.Fatal("LoadManifest succeeded with no manifest.yaml")
	}
}

// writeFixture writes body to <dir>/<rel>, creating parents as
// needed. Helper for the package tests.
func writeFixture(t *testing.T, dir, rel, body string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func errIsCassetteTooLarge(err error) bool {
	return strings.Contains(err.Error(), "size limit") || strings.Contains(err.Error(), "exceeds cap")
}
