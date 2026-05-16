// Package ghsecrets writes secrets to GitHub Actions repository
// secrets via the `gh` CLI.
//
// GitHub secrets are write-only by design (secrets cannot be read
// back through the API), so Get falls back to environment variables —
// useful when the same key is exported during workflow runs.
package ghsecrets

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"hop.top/kit/go/storage/secret"
)

// Store wraps the gh CLI for secret management.
type Store struct {
	repo string // "owner/repo" or "" for the current repository
}

// New creates a Store for the given repo. Pass an empty string to
// use the current repository (gh auto-detects from the working dir).
func New(repo string) *Store {
	return &Store{repo: repo}
}

func (s *Store) repoArgs() []string {
	if s.repo == "" {
		return nil
	}
	return []string{"--repo", s.repo}
}

// Get falls back to environment variables. GitHub secrets are not
// readable via the API; in workflow contexts the secret is exported
// as an env var of the same name.
func (s *Store) Get(_ context.Context, key string) (*secret.Secret, error) {
	if v := os.Getenv(key); v != "" {
		return &secret.Secret{Key: key, Value: []byte(v)}, nil
	}
	return nil, secret.ErrNotFound
}

// List returns all secret names visible to the current gh token.
func (s *Store) List(ctx context.Context, prefix string) ([]string, error) {
	args := append([]string{"secret", "list", "--json", "name", "--jq", ".[].name"}, s.repoArgs()...)
	out, err := exec.CommandContext(ctx, "gh", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("ghsecrets: gh secret list: %w", err)
	}
	var keys []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			if prefix == "" || strings.HasPrefix(line, prefix) {
				keys = append(keys, line)
			}
		}
	}
	return keys, nil
}

// Exists checks the repo's secret list for key.
func (s *Store) Exists(ctx context.Context, key string) (bool, error) {
	keys, err := s.List(ctx, "")
	if err != nil {
		return false, err
	}
	for _, k := range keys {
		if k == key {
			return true, nil
		}
	}
	return false, nil
}

// Set writes the secret to the repo via `gh secret set`.
func (s *Store) Set(ctx context.Context, key string, value []byte) error {
	args := append([]string{"secret", "set", key, "--body", string(value)}, s.repoArgs()...)
	if out, err := exec.CommandContext(ctx, "gh", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("ghsecrets: gh secret set %q: %w — %s", key, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Delete removes the secret from the repo via `gh secret delete`.
func (s *Store) Delete(ctx context.Context, key string) error {
	args := append([]string{"secret", "delete", key}, s.repoArgs()...)
	if out, err := exec.CommandContext(ctx, "gh", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("ghsecrets: gh secret delete %q: %w — %s", key, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// secretListItem is the subset of `gh secret list --json ...` we care
// about. updatedAt and visibility/selected_repositories are exposed by
// the GitHub Actions secrets API; we surface the former as UpdatedAt
// and the latter as Scopes.
type secretListItem struct {
	Name                    string    `json:"name"`
	UpdatedAt               time.Time `json:"updatedAt"`
	Visibility              string    `json:"visibility"`
	SelectedRepositoriesURL string    `json:"selectedRepositoriesUrl,omitempty"`
}

// Metadata reports name, updated_at, and visibility scope from
// `gh secret list`. The GitHub Actions secrets API does not return
// the secret value, so this is purely metadata. Returns ErrNotFound
// when the named secret is not in the repo's secret list.
func (s *Store) Metadata(ctx context.Context, key string) (secret.StoredMeta, error) {
	args := append([]string{"secret", "list", "--json", "name,updatedAt,visibility"}, s.repoArgs()...)
	out, err := exec.CommandContext(ctx, "gh", args...).Output()
	if err != nil {
		return secret.StoredMeta{}, fmt.Errorf("ghsecrets: gh secret list: %w", err)
	}
	var items []secretListItem
	if err := json.Unmarshal(out, &items); err != nil {
		return secret.StoredMeta{}, fmt.Errorf("ghsecrets: parse list: %w", err)
	}
	for _, it := range items {
		if it.Name != key {
			continue
		}
		repo := s.repo
		if repo == "" {
			repo = "current"
		}
		meta := secret.StoredMeta{
			Key:       key,
			Source:    "ghsecrets/" + repo,
			Backend:   "ghsecrets",
			UpdatedAt: it.UpdatedAt,
		}
		if it.Visibility != "" {
			meta.Scopes = []string{"visibility:" + it.Visibility}
		}
		return meta, nil
	}
	return secret.StoredMeta{}, secret.ErrNotFound
}

var _ secret.Store = (*Store)(nil)
var _ secret.MutableStore = (*Store)(nil)
var _ secret.MetadataReader = (*Store)(nil)
