package bitbucket

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/drone/go-scm/scm"

	"hop.top/kit/go/integrations/repohost"
)

// Host is the Bitbucket Cloud driver implementation of
// [repohost.MutableHost], implemented as a thin facade over
// github.com/drone/go-scm.
type Host struct {
	cfg     repohost.Config
	client  *scm.Client
	baseURL string
}

// Static interface check.
var _ repohost.MutableHost = (*Host)(nil)

// ListPullRequests returns pull requests matching f for repo
// ("workspace/repo-slug").
func (h *Host) ListPullRequests(ctx context.Context, repo string, f repohost.Filter) ([]repohost.PullRequest, error) {
	if _, _, err := splitRepo(repo); err != nil {
		return nil, fmt.Errorf("bitbucket: list prs: %w", err)
	}
	opts := pullListOptions(f)
	prs, resp, err := h.client.PullRequests.List(ctx, repo, opts)
	if err != nil {
		return nil, wrapErr("list prs", err, resp, repohost.ErrRepoNotFound)
	}
	out := make([]repohost.PullRequest, 0, len(prs))
	for _, pr := range prs {
		mapped := mapPullRequest(pr)
		if !matchAuthorLabel(mapped, f) {
			continue
		}
		out = append(out, mapped)
		if f.Limit > 0 && len(out) >= f.Limit {
			break
		}
	}
	return out, nil
}

// ListIssues returns issues for repo. Bitbucket Cloud's issue
// tracker is opt-in per repository, and go-scm's bitbucket driver
// does not expose issue endpoints (returns scm.ErrNotSupported).
// The kit driver translates that into an empty slice + nil error
// (graceful degradation — adopters can still call GetRepo to detect
// the repo's existence). 404s also yield empty + nil for the same
// reason. The Filter argument is accepted for surface symmetry but
// has no effect on Bitbucket Cloud.
func (h *Host) ListIssues(ctx context.Context, repo string, _ repohost.Filter) ([]repohost.Issue, error) {
	if _, _, err := splitRepo(repo); err != nil {
		return nil, fmt.Errorf("bitbucket: list issues: %w", err)
	}
	_, resp, err := h.client.Issues.List(ctx, repo, scm.IssueListOptions{Open: true})
	if err == nil {
		return []repohost.Issue{}, nil
	}
	if errors.Is(err, scm.ErrNotSupported) || is404(resp) {
		return []repohost.Issue{}, nil
	}
	return nil, fmt.Errorf("bitbucket: list issues: %w", err)
}

// GetCommit fetches commit metadata for sha in repo.
func (h *Host) GetCommit(ctx context.Context, repo, sha string) (repohost.Commit, error) {
	if _, _, err := splitRepo(repo); err != nil {
		return repohost.Commit{}, fmt.Errorf("bitbucket: get commit: %w", err)
	}
	c, resp, err := h.client.Git.FindCommit(ctx, repo, sha)
	if err != nil {
		return repohost.Commit{}, wrapErr("get commit", err, resp, repohost.ErrCommitNotFound)
	}
	return mapCommit(c), nil
}

// GetRepo returns repository metadata for repo.
func (h *Host) GetRepo(ctx context.Context, repo string) (repohost.Repo, error) {
	if _, _, err := splitRepo(repo); err != nil {
		return repohost.Repo{}, fmt.Errorf("bitbucket: get repo: %w", err)
	}
	r, resp, err := h.client.Repositories.Find(ctx, repo)
	if err != nil {
		return repohost.Repo{}, wrapErr("get repo", err, resp, repohost.ErrRepoNotFound)
	}
	return mapRepo(r), nil
}

// PostComment posts a comment on a pull request identified by
// number. Bitbucket Cloud's go-scm driver does not implement issue
// comments, so the unified surface only routes to the PR endpoint;
// 404 from PR endpoint surfaces as ErrRepoNotFound.
func (h *Host) PostComment(ctx context.Context, repo string, number int, body string) (repohost.Comment, error) {
	if body == "" {
		return repohost.Comment{}, errors.New("bitbucket: comment body must not be empty")
	}
	if _, _, err := splitRepo(repo); err != nil {
		return repohost.Comment{}, fmt.Errorf("bitbucket: post comment: %w", err)
	}
	in := &scm.CommentInput{Body: body}

	c, resp, err := h.client.PullRequests.CreateComment(ctx, repo, number, in)
	if err == nil {
		return mapComment(c), nil
	}
	// PR endpoint failed. If 404, the repo or PR isn't there — surface
	// the kit sentinel rather than the implementation-detail message.
	// All other failures (auth, server, etc.) propagate verbatim.
	if is404(resp) || errors.Is(err, scm.ErrNotFound) {
		return repohost.Comment{}, fmt.Errorf("bitbucket: post comment: %w", repohost.ErrRepoNotFound)
	}
	return repohost.Comment{}, fmt.Errorf("bitbucket: post comment: %w", err)
}

func splitRepo(repo string) (string, string, error) {
	parts := strings.Split(repo, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("repo must be 'workspace/repo-slug', got %q", repo)
	}
	return parts[0], parts[1], nil
}

func pullListOptions(f repohost.Filter) scm.PullRequestListOptions {
	opts := scm.PullRequestListOptions{}
	switch {
	case f.Open && f.Closed:
		opts.Open, opts.Closed = true, true
	case f.Closed && !f.Open:
		opts.Closed = true
	default:
		opts.Open = true
	}
	if f.Limit > 0 {
		opts.Size = f.Limit
	}
	return opts
}

func matchAuthorLabel(pr repohost.PullRequest, f repohost.Filter) bool {
	if f.Author != "" && pr.Author != f.Author {
		return false
	}
	if f.Label != "" {
		for _, l := range pr.Labels {
			if l == f.Label {
				return true
			}
		}
		return false
	}
	return true
}

func is404(resp *scm.Response) bool {
	return resp != nil && resp.Status == http.StatusNotFound
}

func wrapErr(op string, err error, resp *scm.Response, sentinel error) error {
	if is404(resp) || errors.Is(err, scm.ErrNotFound) {
		return fmt.Errorf("bitbucket: %s: %w", op, sentinel)
	}
	return fmt.Errorf("bitbucket: %s: %w", op, err)
}

func mapPullRequest(pr *scm.PullRequest) repohost.PullRequest {
	out := repohost.PullRequest{Labels: []string{}, Raw: map[string]any{}}
	if pr == nil {
		return out
	}
	out.Number = pr.Number
	out.Title = pr.Title
	out.Author = bitbucketAuthor(pr.Author)
	out.State = prState(pr)
	out.HeadRef = pr.Source
	out.BaseRef = pr.Target
	out.URL = pr.Link
	out.CreatedAt = pr.Created
	out.UpdatedAt = pr.Updated
	for _, l := range pr.Labels {
		out.Labels = append(out.Labels, l.Name)
	}
	if pr.Body != "" {
		out.Raw["body"] = pr.Body
	}
	if pr.Diff != "" {
		out.Raw["diff_link"] = pr.Diff
	}
	if pr.Merge != "" {
		out.Raw["merge_commit_sha"] = pr.Merge
	}
	out.Raw["state_raw"] = stateRaw(pr.Closed, pr.Merged)
	return out
}

func mapCommit(c *scm.Commit) repohost.Commit {
	out := repohost.Commit{Raw: map[string]any{}}
	if c == nil {
		return out
	}
	out.SHA = c.Sha
	out.Message = c.Message
	out.URL = c.Link
	out.Author = bitbucketAuthor(scm.User{
		Login: c.Author.Login,
		Name:  c.Author.Name,
	})
	out.Email = c.Author.Email
	if !c.Author.Date.IsZero() {
		out.CreatedAt = c.Author.Date
	}
	return out
}

func mapRepo(r *scm.Repository) repohost.Repo {
	out := repohost.Repo{Raw: map[string]any{}}
	if r == nil {
		return out
	}
	out.Owner = r.Namespace
	out.Name = r.Name
	out.DefaultBranch = r.Branch
	out.Private = r.Private
	out.HTMLURL = r.Link
	if r.ID != "" {
		out.Raw["id"] = r.ID
	}
	if r.Clone != "" {
		out.Raw["clone_url"] = r.Clone
	}
	if r.CloneSSH != "" {
		out.Raw["ssh_url"] = r.CloneSSH
	}
	return out
}

func mapComment(c *scm.Comment) repohost.Comment {
	out := repohost.Comment{Raw: map[string]any{}}
	if c == nil {
		return out
	}
	out.ID = int64(c.ID)
	out.Author = bitbucketAuthor(c.Author)
	out.Body = c.Body
	out.CreatedAt = c.Created
	if !c.Updated.IsZero() && c.Updated != c.Created {
		out.Raw["updated_at"] = c.Updated
	}
	return out
}

// bitbucketAuthor returns Login when set; otherwise the human Name.
// Bitbucket Cloud doesn't expose a stable login on every endpoint —
// some routes only carry display name.
func bitbucketAuthor(u scm.User) string {
	if u.Login != "" {
		return u.Login
	}
	return u.Name
}

func prState(pr *scm.PullRequest) string {
	switch {
	case pr.Merged:
		return "merged"
	case pr.Closed:
		return "closed"
	default:
		return "open"
	}
}

func stateRaw(closed, merged bool) string {
	switch {
	case merged:
		return "MERGED"
	case closed:
		return "DECLINED"
	default:
		return "OPEN"
	}
}
