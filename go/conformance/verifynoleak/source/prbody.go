package source

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// ErrGHNotFound is returned by PRBody when the `gh` binary is not on
// PATH. Callers (verify-no-leak's RunE) map this to IOError with a
// hint about installing GitHub CLI.
var ErrGHNotFound = errors.New("source: gh binary not found on PATH")

// ErrNoOriginRemote is returned by ParseOriginRepo when the cwd does
// not have a `remote.origin.url` configured. PRBody only knows where
// to fetch from via the origin remote.
var ErrNoOriginRemote = errors.New("source: no remote.origin.url configured")

// ErrUnsupportedOriginURL is returned by ParseOriginRepo when the
// origin URL is not a recognized GitHub form (ssh or https).
var ErrUnsupportedOriginURL = errors.New("source: origin URL is not a github.com URL")

// PRBody fetches the body of pull request `n` for the repo whose
// `remote.origin.url` is configured in `cwd`. Returns the body bytes
// suitable for feeding to the markdown scanner.
//
// Wraps `gh api repos/{owner}/{repo}/pulls/{n} --jq .body`. Requires
// `gh` on PATH and a valid auth context (CI: GH_TOKEN; local: gh auth
// login). Errors from `gh` are surfaced verbatim — the caller decides
// the user-facing message.
func PRBody(cwd string, n int) ([]byte, error) {
	if n <= 0 {
		return nil, fmt.Errorf("source: --pr-body requires a positive PR number, got %d", n)
	}
	originURL, err := runGit(cwd, "config", "--get", "remote.origin.url")
	if err != nil {
		return nil, err
	}
	originURL = strings.TrimSpace(originURL)
	if originURL == "" {
		return nil, ErrNoOriginRemote
	}
	owner, repo, err := ParseOriginRepo(originURL)
	if err != nil {
		return nil, err
	}
	if _, lookErr := exec.LookPath("gh"); lookErr != nil {
		return nil, ErrGHNotFound
	}
	endpoint := fmt.Sprintf("repos/%s/%s/pulls/%d", owner, repo, n)
	cmd := exec.Command("gh", "api", endpoint, "--jq", ".body")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("gh api %s: %s", endpoint, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("gh api %s: %w", endpoint, err)
	}
	// `--jq .body` outputs the body followed by a trailing newline.
	// Trim only the trailing newline so the markdown scanner sees the
	// body verbatim.
	out = trimTrailingNewline(out)
	return out, nil
}

// ParseOriginRepo extracts (owner, repo) from a github.com origin URL.
// Supports both forms emitted by `git remote add`:
//
//	git@github.com:owner/repo.git           (ssh)
//	https://github.com/owner/repo.git       (https, with or without .git)
//	ssh://git@github.com/owner/repo.git     (explicit ssh scheme)
//
// Returns ErrUnsupportedOriginURL for any other form (gitlab, bitbucket,
// custom hosts). The verify-no-leak --pr-body flag is github-only by
// design; gitlab support would be a separate flag.
func ParseOriginRepo(url string) (owner, repo string, err error) {
	url = strings.TrimSpace(url)
	if url == "" {
		return "", "", ErrNoOriginRemote
	}
	var path string
	switch {
	case strings.HasPrefix(url, "git@github.com:"):
		path = strings.TrimPrefix(url, "git@github.com:")
	case strings.HasPrefix(url, "ssh://git@github.com/"):
		path = strings.TrimPrefix(url, "ssh://git@github.com/")
	case strings.HasPrefix(url, "https://github.com/"):
		path = strings.TrimPrefix(url, "https://github.com/")
	case strings.HasPrefix(url, "http://github.com/"):
		path = strings.TrimPrefix(url, "http://github.com/")
	default:
		return "", "", fmt.Errorf("%w: %s", ErrUnsupportedOriginURL, url)
	}
	path = strings.TrimSuffix(path, ".git")
	path = strings.TrimSuffix(path, "/")
	parts := strings.SplitN(path, "/", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("%w: cannot extract owner/repo from %q", ErrUnsupportedOriginURL, url)
	}
	return parts[0], parts[1], nil
}

// PRBodyPathLabel returns the synthetic path label used to identify
// PR-body findings in scanner output. Matches the "commit:<sha>"
// convention.
func PRBodyPathLabel(n int) string {
	return "pr:" + strconv.Itoa(n) + ":body"
}

// trimTrailingNewline removes a single trailing "\n" (or "\r\n") from
// the slice. Leaves embedded newlines intact.
func trimTrailingNewline(b []byte) []byte {
	if len(b) == 0 {
		return b
	}
	if b[len(b)-1] == '\n' {
		b = b[:len(b)-1]
	}
	if len(b) > 0 && b[len(b)-1] == '\r' {
		b = b[:len(b)-1]
	}
	return b
}
