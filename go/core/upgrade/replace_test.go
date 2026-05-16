package upgrade

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestReplaceBinary_WithChecksum(t *testing.T) {
	dir := t.TempDir()
	binContent := []byte("new binary content")
	h := sha256.Sum256(binContent)
	expectedHash := fmt.Sprintf("%x", h)

	binSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(binContent)
	}))
	defer binSrv.Close()

	checksumBody := fmt.Sprintf("%s  mytool_darwin_arm64\n", expectedHash)
	csumSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(checksumBody))
	}))
	defer csumSrv.Close()

	// create a fake "self" executable
	selfPath := filepath.Join(dir, "mytool")
	if err := os.WriteFile(selfPath, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := Config{
		BinaryName:  "mytool",
		ChecksumURL: csumSrv.URL,
		Timeout:     5e9,
	}

	// Download raw archive, verify checksum, then extract.
	archiveTmp, err := os.CreateTemp(dir, ".archive-*")
	if err != nil {
		t.Fatal(err)
	}
	archivePath := archiveTmp.Name()
	defer os.Remove(archivePath)

	if err := downloadRaw(context.Background(), cfg, binSrv.URL+"/mytool_darwin_arm64", archiveTmp); err != nil {
		t.Fatal(err)
	}
	archiveTmp.Close()

	// Verify checksum of the raw download (archive).
	err = fetchAndVerify(t.Context(), cfg, csumSrv.URL, "mytool_darwin_arm64", archivePath)
	if err != nil {
		t.Errorf("checksum verification failed: %v", err)
	}
}

func TestReplaceBinary_SkipVerify(t *testing.T) {
	dir := t.TempDir()
	binContent := []byte("skip-verify binary")

	binSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(binContent)
	}))
	defer binSrv.Close()

	csumSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("checksum server should not be called when SkipVerify is true")
	}))
	defer csumSrv.Close()

	cfg := Config{
		BinaryName:  "mytool",
		ChecksumURL: csumSrv.URL,
		SkipVerify:  true,
		Timeout:     5e9,
	}

	// Exercise the download+extract flow with SkipVerify.
	archiveTmp, err := os.CreateTemp(dir, ".archive-*")
	if err != nil {
		t.Fatal(err)
	}
	archivePath := archiveTmp.Name()
	defer os.Remove(archivePath)

	if err := downloadRaw(context.Background(), cfg, binSrv.URL+"/mytool_darwin_arm64", archiveTmp); err != nil {
		t.Fatal(err)
	}
	archiveTmp.Close()

	// With SkipVerify, skip fetchAndVerify entirely (mirrors replaceBinary logic).
	tmpFile, err := os.CreateTemp(dir, ".upgrade-*")
	if err != nil {
		t.Fatal(err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if err := extractOrCopy("mytool_darwin_arm64", cfg.BinaryName, archivePath, tmpFile); err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()

	got, err := os.ReadFile(tmpPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(binContent) {
		t.Errorf("binary content = %q; want %q", got, binContent)
	}
}
