// Command vendor-presidio refreshes kit/redact's Presidio PII rule pack.
//
// Provenance model: Presidio's recognizers are Python `Pattern` objects, not
// portable regex strings. Auto-extracting them via AST walk is brittle
// (Pattern instantiations are scattered across `*_recognizer.py` files in
// inconsistent shapes) and running Python at vendor time pulls in a heavy
// transitive toolchain. We chose the simpler, idempotent path:
//
//   - The curated rule body lives in `curated.go` (this package).
//
//   - This tool stamps the body with provenance headers bound to the
//     supplied --tag, then writes:
//
//     go/core/redact/rules/presidio-pii.toml
//     go/core/redact/rules/SOURCES.md
//     go/core/redact/rules/NOTICE
//     go/core/redact/rules/LICENSE   (copied verbatim from upstream)
//
// Refreshing means: bump --tag, optionally edit `curated.go` to add or
// remove patterns observed in the new release, run the tool, review diff.
//
// Idempotent: same --tag + unchanged curated.go → byte-identical output.
//
// Usage:
//
//	go run ./tools/vendor-presidio --tag 2.2.355 --out go/core/redact/rules/
package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const (
	owner      = "microsoft"
	repo       = "presidio"
	licenseRel = "LICENSE"
)

func main() {
	var (
		tag    = flag.String("tag", "", "upstream Presidio release tag (e.g. 2.2.355)")
		outDir = flag.String("out", "", "output directory (e.g. go/core/redact/rules/)")
	)
	flag.Parse()

	if *tag == "" || *outDir == "" {
		fmt.Fprintln(os.Stderr, "usage: vendor-presidio --tag <tag> --out <dir>")
		os.Exit(2)
	}

	rawLicense, err := fetch(rawURL(*tag, licenseRel))
	if err != nil {
		die("fetch %s: %v", licenseRel, err)
	}

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		die("mkdir %s: %v", *outDir, err)
	}

	if err := writeFile(filepath.Join(*outDir, "presidio-pii.toml"),
		[]byte(renderTOML(*tag))); err != nil {
		die("write presidio-pii.toml: %v", err)
	}
	if err := writeFile(filepath.Join(*outDir, "SOURCES.md"),
		[]byte(renderSources(*tag))); err != nil {
		die("write SOURCES.md: %v", err)
	}
	if err := writeFile(filepath.Join(*outDir, "NOTICE"),
		[]byte(renderNotice(*tag))); err != nil {
		die("write NOTICE: %v", err)
	}
	if err := writeFile(filepath.Join(*outDir, "LICENSE"), rawLicense); err != nil {
		die("write LICENSE: %v", err)
	}

	fmt.Printf("vendored presidio %s → %s\n", *tag, *outDir)
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "vendor-presidio: "+format+"\n", args...)
	os.Exit(1)
}

func rawURL(tag, path string) string {
	return fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s", owner, repo, tag, path)
}

func fetch(url string) ([]byte, error) {
	c := &http.Client{Timeout: 30 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, url)
	}
	return io.ReadAll(resp.Body)
}

func writeFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o644)
}
