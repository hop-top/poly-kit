// Command check-kit-pin fails when the cli-go template's kit pin lags
// the published releases of hop.top/kit.
//
// A project `kit init --from cli-go` generates requires the kit version
// pinned in templates/cli-go/go.mod.tmpl. The release PR bumps that pin
// (release-please extra-files, generic updater), so on a healthy tree
// the pin is the latest release, or the release in flight. This check
// is the backstop: it reads the published versions from the Go module
// proxy — the list a `go get` resolves against, not local tags, which
// can be missing, stale, or never pushed — and fails when more than
// -max-lag published versions are newer than the pin. A pin newer than
// every published version is a release in flight and passes; a pin
// that is neither published nor newer names a version that does not
// exist and fails.
//
// Usage:
//
//	go run ./internal/tools/check-kit-pin
//	go run ./internal/tools/check-kit-pin -max-lag 0 -proxy https://proxy.golang.org
//
// Exit status: 0 current, 1 lagging or unknown pin, 2 usage or I/O
// failure.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

const kitModule = "hop.top/kit"

func main() {
	var (
		pinFile = flag.String("pin-file", "templates/cli-go/go.mod.tmpl", "template go.mod holding the kit pin")
		proxy   = flag.String("proxy", "https://proxy.golang.org", "Go module proxy base URL")
		maxLag  = flag.Int("max-lag", 1, "published versions allowed to be newer than the pin")
	)
	flag.Parse()

	body, err := os.ReadFile(*pinFile)
	if err != nil {
		fail(2, "%v", err)
	}
	pin, err := readPin(string(body))
	if err != nil {
		fail(2, "%s: %v", *pinFile, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	published, err := fetchVersions(ctx, http.DefaultClient, *proxy, kitModule)
	if err != nil {
		fail(2, "%v", err)
	}

	newer, err := check(pin, published, *maxLag)
	if err != nil {
		fail(1, "%s pins %s %s: %v", *pinFile, kitModule, pin, err)
	}
	if len(newer) == 0 {
		state := "current"
		if !slices.Contains(published, pin) {
			state = "ahead of the proxy (a release in flight)"
		}
		fmt.Printf("%s pins %s %s: %s\n", *pinFile, kitModule, pin, state)
		return
	}
	fmt.Printf("%s pins %s %s: %d newer (%s), within -max-lag %d\n",
		*pinFile, kitModule, pin, len(newer), strings.Join(newer, ", "), *maxLag)
}

// pinLine matches the kit requirement in a go.mod, template actions
// and comments after the version included.
var pinLine = regexp.MustCompile(`(?m)^\s*(?:require\s+)?` + regexp.QuoteMeta(kitModule) + `\s+([^\s{]+)`)

// readPin returns the kit version a go.mod requires.
func readPin(gomod string) (string, error) {
	m := pinLine.FindAllStringSubmatch(gomod, -1)
	switch {
	case len(m) == 0:
		return "", fmt.Errorf("no %s requirement", kitModule)
	case len(m) > 1:
		return "", fmt.Errorf("%d %s requirements, want one", len(m), kitModule)
	}
	v := m[0][1]
	if !semver.IsValid(v) {
		return "", fmt.Errorf("pin %q is not a semantic version", v)
	}
	return v, nil
}

// fetchVersions reads the proxy's version list for mod.
func fetchVersions(ctx context.Context, c *http.Client, proxy, mod string) ([]string, error) {
	escaped, err := module.EscapePath(mod)
	if err != nil {
		return nil, err
	}
	url := strings.TrimSuffix(proxy, "/") + "/" + escaped + "/@v/list"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("GET %s: %s: %s", url, resp.Status, strings.TrimSpace(string(b)))
	}
	var out []string
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if v := strings.TrimSpace(sc.Text()); semver.IsValid(v) {
			out = append(out, v)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("GET %s: no versions listed", url)
	}
	return out, nil
}

// errUnpublished is a pin that is neither published nor newer than
// every published version.
var errUnpublished = errors.New("not a published version")

// check returns the published versions newer than pin, sorted
// ascending. It fails when there are more than maxLag of them, or when
// pin is unpublished yet not newer than the latest release.
func check(pin string, published []string, maxLag int) ([]string, error) {
	var newer []string
	for _, v := range published {
		if semver.Compare(v, pin) > 0 {
			newer = append(newer, v)
		}
	}
	semver.Sort(newer)
	if len(newer) > 0 && !slices.Contains(published, pin) {
		return newer, fmt.Errorf("%w; latest is %s", errUnpublished, newer[len(newer)-1])
	}
	if len(newer) > maxLag {
		return newer, fmt.Errorf("%d published versions are newer (%s), more than %d; "+
			"set the pin in templates/cli-go/go.mod.tmpl and kit-template.yaml, "+
			"and their internal/template/builtins mirrors, to %s",
			len(newer), strings.Join(newer, ", "), maxLag, newer[len(newer)-1])
	}
	return newer, nil
}

func fail(code int, format string, args ...any) {
	fmt.Fprintf(os.Stderr, "check-kit-pin: "+format+"\n", args...)
	os.Exit(code)
}
