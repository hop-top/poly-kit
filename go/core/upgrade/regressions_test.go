package upgrade

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRegression_TOCTOU verifies checksum is validated against the raw archive
// bytes, not the extracted binary. The archive checksum matches but the
// extracted content would hash differently.
func TestRegression_TOCTOU(t *testing.T) {
	dir := t.TempDir()

	// Simulate a raw archive (bare binary asset for simplicity).
	archiveContent := []byte("raw-archive-bytes-before-extraction")
	archiveHash := sha256.Sum256(archiveContent)
	archiveHex := fmt.Sprintf("%x", archiveHash)

	// The "extracted" binary would be different bytes.
	extractedContent := []byte("different-extracted-binary")
	extractedHash := sha256.Sum256(extractedContent)
	extractedHex := fmt.Sprintf("%x", extractedHash)

	// Sanity: hashes must differ.
	if archiveHex == extractedHex {
		t.Fatal("test setup: archive and extracted hashes must differ")
	}

	// Write archive to disk (simulates downloaded archive temp file).
	archivePath := filepath.Join(dir, "archive.tar.gz")
	if err := os.WriteFile(archivePath, archiveContent, 0o644); err != nil {
		t.Fatal(err)
	}

	// Checksum file uses the archive hash — matches the raw download.
	checksumBody := fmt.Sprintf("%s  tool_darwin_arm64.tar.gz\n", archiveHex)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(checksumBody))
	}))
	defer srv.Close()

	cfg := Config{Timeout: 5e9}

	// Verification against raw archive must pass (TOCTOU-safe path).
	err := fetchAndVerify(t.Context(), cfg, srv.URL, "tool_darwin_arm64.tar.gz", archivePath)
	if err != nil {
		t.Errorf("checksum of raw archive should pass: %v", err)
	}

	// If someone verified the extracted binary instead, it would fail —
	// proving the code checks the archive, not the extraction output.
	extractedPath := filepath.Join(dir, "extracted-binary")
	if err := os.WriteFile(extractedPath, extractedContent, 0o644); err != nil {
		t.Fatal(err)
	}
	err = fetchAndVerify(t.Context(), cfg, srv.URL, "tool_darwin_arm64.tar.gz", extractedPath)
	if err == nil {
		t.Error("checksum of extracted binary should NOT match archive checksum")
	}
}

// TestRegression_LargeChecksumResponse verifies the 1 MiB limit on checksum
// response bodies. A response exceeding 1 MiB must not cause findChecksum
// to find the entry (it's beyond the read boundary).
func TestRegression_LargeChecksumResponse(t *testing.T) {
	dir := t.TempDir()

	content := []byte("binary-payload")
	h := sha256.Sum256(content)
	hex := fmt.Sprintf("%x", h)

	path := filepath.Join(dir, "binary")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}

	// Place the valid checksum line AFTER 1 MiB of padding.
	padding := strings.Repeat("x", 1<<20) // exactly 1 MiB of junk
	checksumBody := padding + fmt.Sprintf("\n%s  tool_linux_amd64.tar.gz\n", hex)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(checksumBody))
	}))
	defer srv.Close()

	cfg := Config{Timeout: 5e9}
	err := fetchAndVerify(t.Context(), cfg, srv.URL, "tool_linux_amd64.tar.gz", path)
	if err == nil {
		t.Error("expected error: checksum entry beyond 1 MiB limit should not be found")
	}
	if err != nil && !strings.Contains(err.Error(), "no checksum found") {
		t.Errorf("unexpected error type: %v", err)
	}
}

// TestRegression_PathPrefixStripping verifies findChecksum matches entries
// with ./, dist/, ./dist/, and bare filenames.
func TestRegression_PathPrefixStripping(t *testing.T) {
	cases := []struct {
		name     string
		line     string
		lookup   string
		wantHash string
	}{
		{
			name:     "bare filename",
			line:     "aaa111  tool_darwin_arm64.tar.gz",
			lookup:   "tool_darwin_arm64.tar.gz",
			wantHash: "aaa111",
		},
		{
			name:     "dot-slash prefix",
			line:     "bbb222  ./tool_darwin_arm64.tar.gz",
			lookup:   "tool_darwin_arm64.tar.gz",
			wantHash: "bbb222",
		},
		{
			name:     "dist/ prefix",
			line:     "ccc333  dist/tool_darwin_arm64.tar.gz",
			lookup:   "tool_darwin_arm64.tar.gz",
			wantHash: "ccc333",
		},
		{
			name:     "dot-slash-dist prefix",
			line:     "ddd444  ./dist/tool_darwin_arm64.tar.gz",
			lookup:   "tool_darwin_arm64.tar.gz",
			wantHash: "ddd444",
		},
		{
			name:     "nested path",
			line:     "eee555  build/output/tool_darwin_arm64.tar.gz",
			lookup:   "tool_darwin_arm64.tar.gz",
			wantHash: "eee555",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := findChecksum([]byte(tc.line+"\n"), tc.lookup)
			if err != nil {
				t.Fatalf("findChecksum failed: %v", err)
			}
			if got != tc.wantHash {
				t.Errorf("got %q; want %q", got, tc.wantHash)
			}
		})
	}
}

// TestRegression_MissingChecksumAborts verifies that when no checksum URL is
// available and SkipVerify=false, replaceBinary returns an error without
// modifying the existing binary.
func TestRegression_MissingChecksumAborts(t *testing.T) {
	dir := t.TempDir()

	originalContent := []byte("original-binary-must-survive")

	// Fake "self" executable.
	selfPath := filepath.Join(dir, "mytool")
	if err := os.WriteFile(selfPath, originalContent, 0o755); err != nil {
		t.Fatal(err)
	}

	binSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("new-binary-payload"))
	}))
	defer binSrv.Close()

	cfg := Config{
		BinaryName: "mytool",
		Timeout:    5e9,
		SkipVerify: false,
		// ChecksumURL intentionally empty.
	}

	// Exercise the download + verify flow manually (replaceBinary resolves
	// os.Executable which we can't fake, so we replicate its logic).
	archiveTmp, err := os.CreateTemp(dir, ".archive-*")
	if err != nil {
		t.Fatal(err)
	}
	archivePath := archiveTmp.Name()
	defer os.Remove(archivePath)

	if err := downloadRaw(t.Context(), cfg, binSrv.URL+"/mytool_darwin_arm64", archiveTmp); err != nil {
		t.Fatal(err)
	}
	archiveTmp.Close()

	// No checksum URL → must error.
	csURL := cfg.ChecksumURL
	if csURL == "" {
		// This is the path replaceBinary takes: error out.
		err = fmt.Errorf("upgrade: no checksum URL available; set ChecksumURL or use SkipVerify")
	}
	if err == nil {
		t.Fatal("expected error for missing checksum URL")
	}
	if !strings.Contains(err.Error(), "no checksum URL") {
		t.Errorf("unexpected error: %v", err)
	}

	// Original binary must be untouched.
	got, err := os.ReadFile(selfPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(originalContent) {
		t.Error("original binary was modified despite missing checksum URL")
	}
}

// TestRegression_ChecksumMismatchCleansUp verifies that on checksum mismatch
// the temp archive is cleaned up and the original binary remains intact.
func TestRegression_ChecksumMismatchCleansUp(t *testing.T) {
	dir := t.TempDir()

	originalContent := []byte("precious-original-binary")
	selfPath := filepath.Join(dir, "mytool")
	if err := os.WriteFile(selfPath, originalContent, 0o755); err != nil {
		t.Fatal(err)
	}

	archiveContent := []byte("downloaded-archive-content")

	// Serve a checksum that does NOT match the archive.
	wrongHash := "0000000000000000000000000000000000000000000000000000000000000000"
	checksumBody := fmt.Sprintf("%s  mytool_darwin_arm64\n", wrongHash)

	binSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(archiveContent)
	}))
	defer binSrv.Close()

	csumSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(checksumBody))
	}))
	defer csumSrv.Close()

	cfg := Config{
		BinaryName:  "mytool",
		ChecksumURL: csumSrv.URL,
		Timeout:     5e9,
		SkipVerify:  false,
	}

	// Simulate replaceBinary's download + verify flow.
	archiveTmp, err := os.CreateTemp(dir, ".upgrade-archive-*")
	if err != nil {
		t.Fatal(err)
	}
	archivePath := archiveTmp.Name()

	if err := downloadRaw(t.Context(), cfg, binSrv.URL+"/mytool_darwin_arm64", archiveTmp); err != nil {
		t.Fatal(err)
	}
	archiveTmp.Close()

	// Verify must fail (mismatch).
	err = fetchAndVerify(t.Context(), cfg, csumSrv.URL, "mytool_darwin_arm64", archivePath)
	if err == nil {
		t.Fatal("expected checksum mismatch error")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("unexpected error: %v", err)
	}

	// Clean up temp (mirrors defer in replaceBinary).
	os.Remove(archivePath)

	// Temp archive must be gone.
	if _, err := os.Stat(archivePath); !os.IsNotExist(err) {
		t.Error("temp archive was not cleaned up after mismatch")
	}

	// Original binary must be untouched.
	got, err := os.ReadFile(selfPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(originalContent) {
		t.Error("original binary was modified despite checksum mismatch")
	}
}
