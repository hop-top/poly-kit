package upgrade

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestParseChecksumLine(t *testing.T) {
	cases := []struct {
		line     string
		wantHash string
		wantFile string
		wantOK   bool
	}{
		{"abc123  mytool_darwin_arm64.tar.gz", "abc123", "mytool_darwin_arm64.tar.gz", true},
		{"abc123 mytool_darwin_arm64.tar.gz", "abc123", "mytool_darwin_arm64.tar.gz", true},
		{"", "", "", false},
		{"abc123", "", "", false},
		{"# comment line", "", "", false},
	}
	for _, c := range cases {
		hash, file, ok := parseChecksumLine(c.line)
		if ok != c.wantOK || hash != c.wantHash || file != c.wantFile {
			t.Errorf("parseChecksumLine(%q) = (%q, %q, %v); want (%q, %q, %v)",
				c.line, hash, file, ok, c.wantHash, c.wantFile, c.wantOK)
		}
	}
}

func TestFindChecksum(t *testing.T) {
	body := `e3b0c44298fc1c149afbf4c8996fb924  mytool_linux_amd64.tar.gz
a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4  mytool_darwin_arm64.tar.gz
f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0  mytool_darwin_amd64.tar.gz
`
	got, err := findChecksum([]byte(body), "mytool_darwin_arm64.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if got != "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4" {
		t.Errorf("got %q", got)
	}

	_, err = findChecksum([]byte(body), "missing.tar.gz")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestVerifyChecksum(t *testing.T) {
	dir := t.TempDir()
	content := []byte("hello world binary content")
	h := sha256.Sum256(content)
	expectedHash := fmt.Sprintf("%x", h)

	path := filepath.Join(dir, "binary")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := verifyChecksum(path, expectedHash); err != nil {
		t.Errorf("valid checksum failed: %v", err)
	}

	if err := verifyChecksum(path, "0000000000000000000000000000000000000000000000000000000000000000"); err == nil {
		t.Error("expected error for wrong checksum")
	}
}

func TestFetchAndVerify(t *testing.T) {
	dir := t.TempDir()
	content := []byte("test binary data")
	h := sha256.Sum256(content)
	expectedHash := fmt.Sprintf("%x", h)

	checksumBody := fmt.Sprintf("%s  mytool_darwin_arm64.tar.gz\n", expectedHash)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(checksumBody))
	}))
	defer srv.Close()

	path := filepath.Join(dir, "binary")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Config{Timeout: 5e9}
	err := fetchAndVerify(t.Context(), cfg, srv.URL, "mytool_darwin_arm64.tar.gz", path)
	if err != nil {
		t.Errorf("fetchAndVerify failed: %v", err)
	}
}

func TestFetchAndVerify_Mismatch(t *testing.T) {
	dir := t.TempDir()
	content := []byte("test binary data")

	checksumBody := "0000000000000000000000000000000000000000000000000000000000000000  mytool_darwin_arm64.tar.gz\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(checksumBody))
	}))
	defer srv.Close()

	path := filepath.Join(dir, "binary")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Config{Timeout: 5e9}
	err := fetchAndVerify(t.Context(), cfg, srv.URL, "mytool_darwin_arm64.tar.gz", path)
	if err == nil {
		t.Error("expected checksum mismatch error")
	}
}

func TestSelectChecksumAsset(t *testing.T) {
	assets := []ghAsset{
		{Name: "mytool_linux_amd64.tar.gz", BrowserDownloadURL: "http://linux-amd64"},
		{Name: "mytool_darwin_arm64.tar.gz", BrowserDownloadURL: "http://darwin-arm64"},
		{Name: "mytool_checksums.txt", BrowserDownloadURL: "http://checksums"},
		{Name: "mytool_SHA256SUMS", BrowserDownloadURL: "http://sha256sums"},
	}

	got := selectChecksumAsset(assets)
	if got != "http://checksums" && got != "http://sha256sums" {
		t.Errorf("selectChecksumAsset returned %q; want checksums URL", got)
	}

	noChecksum := []ghAsset{
		{Name: "mytool_linux_amd64.tar.gz", BrowserDownloadURL: "http://linux-amd64"},
	}
	got = selectChecksumAsset(noChecksum)
	if got != "" {
		t.Errorf("selectChecksumAsset returned %q for no checksums; want empty", got)
	}
}

func TestSelectChecksumAsset_SHA256SUMSOnly(t *testing.T) {
	assets := []ghAsset{
		{Name: "mytool_linux_amd64.tar.gz", BrowserDownloadURL: "http://linux-amd64"},
		{Name: "SHA256SUMS", BrowserDownloadURL: "http://sha256sums-only"},
	}

	got := selectChecksumAsset(assets)
	if got != "http://sha256sums-only" {
		t.Errorf("selectChecksumAsset returned %q; want %q", got, "http://sha256sums-only")
	}
}

func TestFindChecksum_PathPrefix(t *testing.T) {
	body := []byte("abc123  ./mytool_darwin_arm64.tar.gz\ndef456  dist/mytool_linux_amd64.tar.gz\n")

	got, err := findChecksum(body, "mytool_darwin_arm64.tar.gz")
	if err != nil {
		t.Fatalf("expected match for ./prefix: %v", err)
	}
	if got != "abc123" {
		t.Errorf("got %q; want %q", got, "abc123")
	}

	got, err = findChecksum(body, "mytool_linux_amd64.tar.gz")
	if err != nil {
		t.Fatalf("expected match for dist/ prefix: %v", err)
	}
	if got != "def456" {
		t.Errorf("got %q; want %q", got, "def456")
	}
}
