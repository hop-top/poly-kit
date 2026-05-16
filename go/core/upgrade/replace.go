package upgrade

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func replaceBinary(ctx context.Context, cfg Config, assetURL, checksumURL string, hooks replaceHooks) error {
	if assetURL == "" {
		return fmt.Errorf("upgrade: no download URL for %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("upgrade: resolve self: %w", err)
	}
	self, err = filepath.EvalSymlinks(self)
	if err != nil {
		return fmt.Errorf("upgrade: eval symlinks: %w", err)
	}

	dir := filepath.Dir(self)

	// Download raw archive/binary to a temp file first.
	archiveTmp, err := os.CreateTemp(dir, ".upgrade-archive-*")
	if err != nil {
		return fmt.Errorf("upgrade: create temp: %w", err)
	}
	archivePath := archiveTmp.Name()
	defer os.Remove(archivePath) //nolint:errcheck

	if err := downloadRaw(ctx, cfg, assetURL, archiveTmp); err != nil {
		archiveTmp.Close()
		return err
	}
	archiveTmp.Close()

	if hooks.onDownloaded != nil {
		fi, statErr := os.Stat(archivePath)
		var bytes int64
		if statErr == nil {
			bytes = fi.Size()
		}
		hooks.onDownloaded(archivePath, bytes)
	}

	// Verify checksum of the raw archive before extraction.
	if !cfg.SkipVerify {
		csURL := cfg.ChecksumURL
		if csURL == "" {
			csURL = checksumURL
		}
		if csURL == "" {
			return fmt.Errorf("upgrade: no checksum URL available; set ChecksumURL or use SkipVerify")
		}
		assetName := filepath.Base(assetURL)
		if err := fetchAndVerify(ctx, cfg, csURL, assetName, archivePath); err != nil {
			return err
		}
	}

	// Extract binary (or copy bare binary) to final temp file.
	tmpFile, err := os.CreateTemp(dir, ".upgrade-*")
	if err != nil {
		return fmt.Errorf("upgrade: create temp: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		tmpFile.Close()
		os.Remove(tmpPath) //nolint:errcheck
	}()

	name := filepath.Base(assetURL)
	if err := extractOrCopy(name, cfg.BinaryName, archivePath, tmpFile); err != nil {
		return err
	}
	tmpFile.Close()

	if err := os.Chmod(tmpPath, 0o755); err != nil {
		return fmt.Errorf("upgrade: chmod: %w", err)
	}
	if err := os.Rename(tmpPath, self); err != nil {
		return fmt.Errorf("upgrade: rename: %w", err)
	}
	if hooks.onInstalled != nil {
		hooks.onInstalled(self, self)
	}
	return nil
}

// replaceHooks lets the caller observe successful download and install
// boundaries without coupling replace.go to bus emission. Both fields
// are optional; nil hooks are no-ops.
type replaceHooks struct {
	onDownloaded func(path string, bytes int64)
	onInstalled  func(from, to string)
}

// downloadRaw saves the HTTP response body to w without any extraction.
func downloadRaw(ctx context.Context, cfg Config, url string, w io.Writer) error {
	client := &http.Client{Timeout: cfg.Timeout * 30}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("upgrade: download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("upgrade: download returned %d", resp.StatusCode)
	}

	_, err = io.Copy(w, resp.Body)
	return err
}

// extractOrCopy extracts the binary from an archive file, or copies a bare binary.
func extractOrCopy(assetName, binaryName, archivePath string, w io.Writer) error {
	switch {
	case strings.HasSuffix(assetName, ".tar.gz") || strings.HasSuffix(assetName, ".tgz"):
		f, err := os.Open(archivePath)
		if err != nil {
			return fmt.Errorf("upgrade: open archive: %w", err)
		}
		defer f.Close()
		return extractTarGz(f, binaryName, w)
	case strings.HasSuffix(assetName, ".zip"):
		return fmt.Errorf("upgrade: zip extraction not yet supported; use tar.gz releases")
	default:
		f, err := os.Open(archivePath)
		if err != nil {
			return fmt.Errorf("upgrade: open downloaded binary: %w", err)
		}
		defer f.Close()
		_, err = io.Copy(w, f)
		return err
	}
}
