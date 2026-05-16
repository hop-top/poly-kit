package upgrade

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// fetchAndVerify downloads the checksums file and verifies the local file's SHA-256.
func fetchAndVerify(ctx context.Context, cfg Config, checksumURL, assetName, localPath string) error {
	client := &http.Client{Timeout: cfg.Timeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, checksumURL, nil)
	if err != nil {
		return fmt.Errorf("upgrade: checksum request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("upgrade: fetch checksums: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("upgrade: checksums returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("upgrade: read checksums: %w", err)
	}

	expected, err := findChecksum(body, assetName)
	if err != nil {
		return err
	}

	return verifyChecksum(localPath, expected)
}

// findChecksum extracts the expected hash for a given filename from checksums content.
func findChecksum(body []byte, filename string) (string, error) {
	for _, line := range strings.Split(string(body), "\n") {
		hash, file, ok := parseChecksumLine(line)
		if ok && filepath.Base(file) == filename {
			return hash, nil
		}
	}
	return "", fmt.Errorf("upgrade: no checksum found for %q", filename)
}

// parseChecksumLine parses "hash  filename" or "hash filename" format.
func parseChecksumLine(line string) (hash, file string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}

	// GNU coreutils format: "hash  filename" (two spaces) or "hash filename"
	var parts []string
	if idx := strings.Index(line, "  "); idx > 0 {
		parts = []string{line[:idx], line[idx+2:]}
	} else {
		parts = strings.Fields(line)
	}

	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// verifyChecksum computes SHA-256 of file at path and compares to expected hex hash.
func verifyChecksum(path, expected string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("upgrade: open for checksum: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("upgrade: hash file: %w", err)
	}

	got := fmt.Sprintf("%x", h.Sum(nil))
	if got != expected {
		return fmt.Errorf("upgrade: checksum mismatch: got %s, want %s", got, expected)
	}
	return nil
}
