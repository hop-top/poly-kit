package upgrade

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strings"

	msemver "github.com/Masterminds/semver/v3"
)

// githubAPI is the GitHub REST base; tests point it at a local server.
var githubAPI = "https://api.github.com"

const (
	// listPageSize is the releases requested per page (GitHub's maximum).
	listPageSize = 100
	// listMaxPages bounds a listing to this many requests.
	listMaxPages = 3
)

type releaseInfo struct {
	Version     string
	URL         string
	ChecksumURL string
	Notes       string
	Tag         string // set only when releases are listed
}

type ghRelease struct {
	TagName    string    `json:"tag_name"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
	Body       string    `json:"body"`
	Assets     []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func fetchLatest(ctx context.Context, cfg Config) (*releaseInfo, error) {
	if cfg.ReleaseURL != "" {
		return fetchCustomURL(ctx, cfg)
	}
	if cfg.GitHubRepo != "" {
		return fetchGitHub(ctx, cfg)
	}
	return nil, fmt.Errorf("upgrade: no release source configured")
}

func fetchGitHub(ctx context.Context, cfg Config) (*releaseInfo, error) {
	if cfg.TagPrefix != "" || cfg.IncludePrereleases {
		return fetchGitHubList(ctx, cfg)
	}
	url := fmt.Sprintf("%s/repos/%s/releases/latest", githubAPI, cfg.GitHubRepo)
	client := &http.Client{Timeout: cfg.Timeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upgrade: github API returned %d", resp.StatusCode)
	}

	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("upgrade: decode github response: %w", err)
	}

	version := strings.TrimPrefix(rel.TagName, "v")
	assetURL := selectAsset(rel.Assets)
	checksumURL := selectChecksumAsset(rel.Assets)

	notes := rel.Body
	if len(notes) > 1000 {
		notes = notes[:1000] + "…"
	}

	return &releaseInfo{Version: version, URL: assetURL, ChecksumURL: checksumURL, Notes: notes}, nil
}

// selectAsset picks the best asset for current OS/arch.
func selectAsset(assets []ghAsset) string {
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	archAlias := map[string][]string{
		"amd64": {"amd64", "x86_64"},
		"arm64": {"arm64", "aarch64"},
	}

	aliases := archAlias[goarch]
	if aliases == nil {
		aliases = []string{goarch}
	}

	for _, a := range assets {
		name := strings.ToLower(a.Name)
		if !strings.Contains(name, goos) {
			continue
		}
		for _, arch := range aliases {
			if strings.Contains(name, arch) {
				return a.BrowserDownloadURL
			}
		}
	}
	for _, a := range assets {
		if strings.Contains(strings.ToLower(a.Name), goos) {
			return a.BrowserDownloadURL
		}
	}
	return ""
}

// selectChecksumAsset picks the checksums file from release assets.
func selectChecksumAsset(assets []ghAsset) string {
	suffixes := []string{"checksums.txt", "sha256sums", "sha256sums.txt"}
	for _, a := range assets {
		name := strings.ToLower(a.Name)
		for _, s := range suffixes {
			if strings.HasSuffix(name, s) {
				return a.BrowserDownloadURL
			}
		}
	}
	return ""
}

type customRelease struct {
	Version string `json:"version"`
	URL     string `json:"url"`
	Notes   string `json:"notes"`
}

func fetchCustomURL(ctx context.Context, cfg Config) (*releaseInfo, error) {
	client := &http.Client{Timeout: cfg.Timeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.ReleaseURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upgrade: custom URL returned %s", resp.Status)
	}

	var rel customRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("upgrade: decode custom release: %w", err)
	}

	return &releaseInfo{
		Version: strings.TrimPrefix(rel.Version, "v"),
		URL:     rel.URL,
		Notes:   rel.Notes,
	}, nil
}

// isNewer returns true if latest > current (semver, best-effort).
func isNewer(current, latest string) bool {
	if current == "" || latest == "" {
		return false
	}
	c := parseSemver(strings.TrimPrefix(current, "v"))
	l := parseSemver(strings.TrimPrefix(latest, "v"))
	return compareSemver(l, c) > 0
}

type semver struct {
	major, minor, patch int
	pre                 string
}

func parseSemver(s string) semver {
	var v semver

	if idx := strings.Index(s, "+"); idx >= 0 {
		s = s[:idx]
	}
	if idx := strings.Index(s, "-"); idx >= 0 {
		v.pre = s[idx+1:]
		s = s[:idx]
	}

	parts := strings.SplitN(s, ".", 3)
	if len(parts) >= 1 {
		fmt.Sscanf(parts[0], "%d", &v.major)
	}
	if len(parts) >= 2 {
		fmt.Sscanf(parts[1], "%d", &v.minor)
	}
	if len(parts) >= 3 {
		fmt.Sscanf(parts[2], "%d", &v.patch)
	}
	return v
}

func compareSemver(a, b semver) int {
	if d := a.major - b.major; d != 0 {
		return d
	}
	if d := a.minor - b.minor; d != 0 {
		return d
	}
	if d := a.patch - b.patch; d != 0 {
		return d
	}
	switch {
	case a.pre == "" && b.pre == "":
		return 0
	case a.pre == "" && b.pre != "":
		return 1
	case a.pre != "" && b.pre == "":
		return -1
	default:
		return comparePreRelease(a.pre, b.pre)
	}
}

func comparePreRelease(a, b string) int {
	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")
	n := len(aParts)
	if len(bParts) < n {
		n = len(bParts)
	}
	for i := 0; i < n; i++ {
		d := comparePreIdent(aParts[i], bParts[i])
		if d != 0 {
			return d
		}
	}
	return len(aParts) - len(bParts)
}

func comparePreIdent(a, b string) int {
	var ai, bi int
	an, _ := fmt.Sscanf(a, "%d", &ai)
	bn, _ := fmt.Sscanf(b, "%d", &bi)
	aNum := an == 1 && fmt.Sprintf("%d", ai) == a
	bNum := bn == 1 && fmt.Sprintf("%d", bi) == b
	switch {
	case aNum && bNum:
		return ai - bi
	case aNum:
		return -1
	case bNum:
		return 1
	default:
		if a < b {
			return -1
		}
		if a > b {
			return 1
		}
		return 0
	}
}

// tagVersion returns the strict semver a tag names once cfg.TagPrefix and
// one leading "v" are stripped; ok is false for another component's tag
// or a tag that is not semver.
func tagVersion(cfg Config, tag string) (*msemver.Version, bool) {
	if !strings.HasPrefix(tag, cfg.TagPrefix) {
		return nil, false
	}
	v, err := msemver.StrictNewVersion(strings.TrimPrefix(strings.TrimPrefix(tag, cfg.TagPrefix), "v"))
	if err != nil {
		return nil, false
	}
	return v, true
}

// fetchGitHubList lists releases (newest first, at most listMaxPages of
// listPageSize) and returns the highest semver among non-draft releases
// whose tag starts with cfg.TagPrefix; prereleases only when
// cfg.IncludePrereleases. A later page failing keeps earlier pages.
func fetchGitHubList(ctx context.Context, cfg Config) (*releaseInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	client := &http.Client{}

	var (
		best    *msemver.Version
		bestRel ghRelease
	)
	for page := 1; page <= listMaxPages; page++ {
		items, err := fetchReleasePage(ctx, client, cfg, page)
		if err != nil {
			if page == 1 {
				return nil, err
			}
			break
		}
		for _, raw := range items {
			var rel ghRelease
			if json.Unmarshal(raw, &rel) != nil || rel.Draft {
				continue
			}
			v, ok := tagVersion(cfg, rel.TagName)
			if !ok {
				continue
			}
			if !cfg.IncludePrereleases && (rel.Prerelease || v.Prerelease() != "") {
				continue
			}
			if best == nil || v.GreaterThan(best) {
				best, bestRel = v, rel
			}
		}
		if len(items) < listPageSize {
			break
		}
	}
	if best == nil {
		return nil, fmt.Errorf("upgrade: no matching release")
	}

	notes := bestRel.Body
	if len(notes) > 1000 {
		notes = notes[:1000] + "…"
	}
	return &releaseInfo{
		Version:     best.String(),
		URL:         selectAsset(bestRel.Assets),
		ChecksumURL: selectChecksumAsset(bestRel.Assets),
		Notes:       notes,
		Tag:         bestRel.TagName,
	}, nil
}

func fetchReleasePage(ctx context.Context, client *http.Client, cfg Config, page int) ([]json.RawMessage, error) {
	u := fmt.Sprintf("%s/repos/%s/releases?per_page=%d&page=%d", githubAPI, cfg.GitHubRepo, listPageSize, page)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upgrade: github API returned %d", resp.StatusCode)
	}
	var items []json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, fmt.Errorf("upgrade: decode github response: %w", err)
	}
	return items, nil
}

// cachedTagOK reports whether a cached Result may be served. In list mode
// a cached tag is trusted only when it starts with the prefix and names
// the cached version; anything else, a missing tag included (a record
// written in default mode), is a cache miss. (The cache may sit in
// a shared directory, and a tag can end up in a link.)
func (c *Checker) cachedTagOK(r *Result) bool {
	if c.cfg.TagPrefix == "" && !c.cfg.IncludePrereleases {
		return true
	}
	v, ok := tagVersion(c.cfg, r.Tag)
	if !ok {
		return false
	}
	want, err := msemver.StrictNewVersion(strings.TrimPrefix(r.Latest, "v"))
	return err == nil && v.Equal(want)
}
