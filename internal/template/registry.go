// Registry resolves template specs to fs.FS roots. Supported forms:
//
//  1. Built-in name      "cli-go"                  → embed sub-fs
//  2. @org/name          "@acme/internal"          → registry index → git
//  3. Direct git URL     "github.com/foo/bar@v1"   → git clone + cache
//  4. Filesystem path    "./local" or "/abs/path"  → os.DirFS
//
// Spec: ops/docs/superpowers/specs/2026-04-26-kit-init-design.md §9.
package template

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Registry resolves template specs into fs.FS trees. It caches
// git-cloned templates under cache, keyed by sha256(gitURL@ref).
type Registry struct {
	indexURL string
	cache    string
	client   *http.Client
}

// NewRegistry builds a Registry. Empty indexURL disables @org/name lookup.
// Empty cacheDir disables on-disk caching (clones go to a temp dir
// each time). client defaults to http.DefaultClient when nil.
func NewRegistry(indexURL, cacheDir string) *Registry {
	return &Registry{
		indexURL: indexURL,
		cache:    cacheDir,
		client:   http.DefaultClient,
	}
}

// Resolve maps spec → fs.FS using the rules in the package doc.
// Returns *TemplateNotFoundError when no rule resolves.
func (r *Registry) Resolve(ctx context.Context, spec string) (fs.FS, error) {
	if spec == "" {
		return nil, NewTemplateNotFoundError(spec)
	}

	// 1. Filesystem path: ./rel or /abs.
	if strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "/") {
		if _, err := os.Stat(spec); err != nil {
			if os.IsNotExist(err) {
				return nil, NewTemplateNotFoundError(spec)
			}
			return nil, fmt.Errorf("template: stat %s: %w", spec, err)
		}
		return os.DirFS(spec), nil
	}

	// 2. Built-in name (must precede git-URL parse so plain names win).
	if names, err := Available(); err == nil {
		for _, n := range names {
			if n == spec {
				bfs, berr := BuiltIn()
				if berr != nil {
					return nil, berr
				}
				return fs.Sub(bfs, spec)
			}
		}
	}

	// 3. @org/name → registry index lookup.
	if strings.HasPrefix(spec, "@") {
		gitURL, ref, err := r.lookupIndex(ctx, spec)
		if err != nil {
			return nil, err
		}
		return r.cloneOrCache(ctx, gitURL, ref)
	}

	// 4. Direct git URL: host/owner/repo[@ref].
	if strings.Contains(spec, "/") {
		gitURL, ref := splitRef(spec)
		return r.cloneOrCache(ctx, gitURL, ref)
	}

	return nil, NewTemplateNotFoundError(spec)
}

// indexEntry mirrors a single entry under "templates" in the index JSON.
type indexEntry struct {
	Git        string `json:"git"`
	DefaultRef string `json:"default_ref"`
	// Description is informational; ignored by Resolve.
	Description string `json:"description,omitempty"`
}

type indexDoc struct {
	Schema    int                   `json:"schema"`
	Templates map[string]indexEntry `json:"templates"`
}

// lookupIndex fetches indexURL and resolves spec ("@org/name") to
// (gitURL, ref). Returns *TemplateNotFoundError when missing.
func (r *Registry) lookupIndex(ctx context.Context, spec string) (string, string, error) {
	if r.indexURL == "" {
		return "", "", NewTemplateNotFoundError(spec)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.indexURL, nil)
	if err != nil {
		return "", "", fmt.Errorf("template: build index req: %w", err)
	}
	client := r.client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("template: fetch index %s: %w", r.indexURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("template: index %s: http %d", r.indexURL, resp.StatusCode)
	}
	var doc indexDoc
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return "", "", fmt.Errorf("template: decode index: %w", err)
	}
	entry, ok := doc.Templates[spec]
	if !ok || entry.Git == "" {
		return "", "", NewTemplateNotFoundError(spec)
	}
	return entry.Git, entry.DefaultRef, nil
}

// splitRef parses "host/owner/repo[@ref]" into (httpsURL, ref).
// A bare URL with no scheme gets "https://" prepended; an existing
// scheme (http://, https://, git@, ssh://) is preserved.
func splitRef(spec string) (string, string) {
	url, ref := spec, ""
	if i := strings.LastIndex(spec, "@"); i > 0 {
		// Avoid false positives on "git@host:owner/repo" (the @ comes
		// before any "/"); only treat trailing "@ref" as a ref split.
		if !strings.Contains(spec[i+1:], "/") && !strings.Contains(spec[i+1:], ":") {
			url = spec[:i]
			ref = spec[i+1:]
		}
	}
	if !strings.Contains(url, "://") && !strings.HasPrefix(url, "git@") {
		url = "https://" + url
	}
	return url, ref
}

// cloneOrCache returns fs.FS for gitURL@ref, using cache if present,
// otherwise running `git clone --depth 1 [--branch ref] url tmp` and
// atomically renaming tmp → final cache dir on success.
func (r *Registry) cloneOrCache(ctx context.Context, gitURL, ref string) (fs.FS, error) {
	key := cacheKey(gitURL, ref)
	dest := filepath.Join(r.cache, key)

	if r.cache != "" {
		if info, err := os.Stat(dest); err == nil && info.IsDir() {
			return os.DirFS(dest), nil
		}
		if err := os.MkdirAll(r.cache, 0o755); err != nil {
			return nil, fmt.Errorf("template: mkdir cache: %w", err)
		}
	} else {
		// No cache configured → clone into a fresh temp dir.
		tmp, err := os.MkdirTemp("", "kit-tpl-*")
		if err != nil {
			return nil, fmt.Errorf("template: mkdtemp: %w", err)
		}
		dest = tmp
	}

	tmp := dest + ".tmp"
	_ = os.RemoveAll(tmp)

	args := []string{"clone", "--depth", "1"}
	if ref != "" {
		args = append(args, "--branch", ref)
	}
	args = append(args, gitURL, tmp)

	cmd := exec.CommandContext(ctx, "git", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.RemoveAll(tmp)
		return nil, fmt.Errorf("template: git clone %s: %w: %s",
			gitURL, err, strings.TrimSpace(string(out)))
	}

	if r.cache != "" {
		if err := os.Rename(tmp, dest); err != nil {
			_ = os.RemoveAll(tmp)
			return nil, fmt.Errorf("template: rename cache entry: %w", err)
		}
	} else {
		dest = tmp
	}
	return os.DirFS(dest), nil
}

// cacheKey returns hex(sha256(gitURL + "@" + ref)).
func cacheKey(gitURL, ref string) string {
	sum := sha256.Sum256([]byte(gitURL + "@" + ref))
	return hex.EncodeToString(sum[:])
}
