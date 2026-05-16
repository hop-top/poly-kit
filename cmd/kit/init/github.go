// Package kitinit — github.go wraps the `gh` CLI for repository creation
// and branch protection. AccountType="none" is a no-op. Personal accounts
// pass the bare name (gh resolves to authenticated user); org accounts
// prefix with the org name. ProtectMain is intended for org repos only;
// callers gate the invocation.
package kitinit

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// RepoConfig describes the gh repo-create invocation.
type RepoConfig struct {
	AccountType string // "personal" | "org" | "none"
	Owner       string // username for personal; org name for org
	Name        string
	Visibility  string // "public" | "private" | "internal"
	NoPush      bool
}

// RepoInfo summarizes the created repository.
type RepoInfo struct {
	Repo       string // "<owner>/<name>"
	URL        string // https://github.com/<owner>/<name>
	Visibility string
}

// Create runs `gh repo create` per cfg. Returns RepoInfo on success.
// AccountType="none" returns RepoInfo{}, nil (no-op).
// gh missing on PATH returns clear error with install hint.
func Create(ctx context.Context, dir string, cfg RepoConfig) (RepoInfo, error) {
	if cfg.AccountType == "none" {
		return RepoInfo{}, nil
	}
	if _, err := exec.LookPath("gh"); err != nil {
		return RepoInfo{}, fmt.Errorf("gh not found on PATH: install from https://cli.github.com")
	}

	var repoArg string
	if cfg.AccountType == "org" {
		if cfg.Owner == "" {
			return RepoInfo{}, fmt.Errorf("org account-type requires Owner (org name)")
		}
		repoArg = cfg.Owner + "/" + cfg.Name
	} else {
		// personal: bare name; gh resolves to authenticated user
		repoArg = cfg.Name
	}

	args := []string{"repo", "create", repoArg, "--" + cfg.Visibility, "--source=" + sourceArg(dir), "--remote=origin"}
	if !cfg.NoPush {
		args = append(args, "--push")
	}

	cmd := exec.CommandContext(ctx, "gh", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return RepoInfo{}, fmt.Errorf("gh repo create: %s: %w", strings.TrimSpace(string(out)), err)
	}

	return parseGhCreateOutput(string(out), cfg), nil
}

// sourceArg returns the value for `--source=` on `gh repo create`. When
// dir resolves to the current working directory we prefer `.` for parity
// with the documented `gh repo create --source . --push --private` flow;
// otherwise the absolute path is returned. Any error in cwd resolution
// (or in absolutising dir) falls back to the original dir so the caller
// never breaks on an inability to compare paths.
func sourceArg(dir string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return dir
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	absCwd, err := filepath.Abs(cwd)
	if err != nil {
		return dir
	}
	if absDir == absCwd {
		return "."
	}
	return dir
}

// parseGhCreateOutput extracts the repo URL from gh's output (typically
// printed on the last non-empty line). Falls back to constructing the
// "owner/name" form from cfg when the URL is absent.
func parseGhCreateOutput(out string, cfg RepoConfig) RepoInfo {
	info := RepoInfo{Visibility: cfg.Visibility}
	re := regexp.MustCompile(`https://github\.com/[\w.-]+/[\w.-]+`)
	if m := re.FindString(out); m != "" {
		info.URL = m
		parts := strings.Split(strings.TrimPrefix(m, "https://github.com/"), "/")
		if len(parts) == 2 {
			info.Repo = parts[0] + "/" + parts[1]
		}
	}
	if info.Repo == "" {
		if cfg.AccountType == "org" {
			info.Repo = cfg.Owner + "/" + cfg.Name
		} else {
			info.Repo = cfg.Name
		}
	}
	return info
}

// ProtectMain enables branch protection on main:
//   - Require PRs (1+ review)
//   - Disallow force pushes / deletions
//   - Enforce admins
//
// Only call when AccountType=org (caller gates).
// fullName format: "<owner>/<name>".
func ProtectMain(ctx context.Context, fullName string) error {
	if _, err := exec.LookPath("gh"); err != nil {
		return fmt.Errorf("gh not found on PATH")
	}

	args := []string{
		"api", "-X", "PUT",
		"/repos/" + fullName + "/branches/main/protection",
		"-F", "required_status_checks=null",
		"-F", "enforce_admins=true",
		"-F", "required_pull_request_reviews[required_approving_review_count]=1",
		"-F", "restrictions=null",
		"-F", "allow_force_pushes=false",
		"-F", "allow_deletions=false",
	}
	cmd := exec.CommandContext(ctx, "gh", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("gh api branch protection: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}
