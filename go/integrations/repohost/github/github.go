package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/drone/go-scm/scm"

	"hop.top/kit/go/integrations/repohost"
)

// Host is the GitHub driver implementation of [repohost.MutableHost],
// implemented as a thin facade over github.com/drone/go-scm.
type Host struct {
	cfg    repohost.Config
	client *scm.Client
}

// Static interface check.
var _ repohost.MutableHost = (*Host)(nil)

// ListPullRequests returns pull requests matching f for repo
// ("owner/name"). State filter: open-only → Open=true, closed-only
// → Closed=true, both → Open=true Closed=true. Author and label
// filters are applied client-side after the call.
func (h *Host) ListPullRequests(ctx context.Context, repo string, f repohost.Filter) ([]repohost.PullRequest, error) {
	if _, _, err := splitRepo(repo); err != nil {
		return nil, fmt.Errorf("github: list prs: %w", err)
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

// ListIssues returns issues for repo. GitHub's REST API treats every
// PR as an issue; this method filters out PRs (issues whose go-scm
// PullRequest.Link is non-empty) to match the unified
// repohost.Issue contract. Author and label filters are applied
// client-side.
func (h *Host) ListIssues(ctx context.Context, repo string, f repohost.Filter) ([]repohost.Issue, error) {
	if _, _, err := splitRepo(repo); err != nil {
		return nil, fmt.Errorf("github: list issues: %w", err)
	}
	opts := issueListOptions(f)
	issues, resp, err := h.client.Issues.List(ctx, repo, opts)
	if err != nil {
		return nil, wrapErr("list issues", err, resp, repohost.ErrRepoNotFound)
	}
	out := make([]repohost.Issue, 0, len(issues))
	for _, is := range issues {
		if isPullRequest(is) {
			continue
		}
		mapped := mapIssue(is)
		if !matchAuthorLabelIssue(mapped, f) {
			continue
		}
		out = append(out, mapped)
		if f.Limit > 0 && len(out) >= f.Limit {
			break
		}
	}
	return out, nil
}

// GetCommit fetches commit metadata for sha in repo.
func (h *Host) GetCommit(ctx context.Context, repo, sha string) (repohost.Commit, error) {
	if _, _, err := splitRepo(repo); err != nil {
		return repohost.Commit{}, fmt.Errorf("github: get commit: %w", err)
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
		return repohost.Repo{}, fmt.Errorf("github: get repo: %w", err)
	}
	r, resp, err := h.client.Repositories.Find(ctx, repo)
	if err != nil {
		return repohost.Repo{}, wrapErr("get repo", err, resp, repohost.ErrRepoNotFound)
	}
	return mapRepo(r), nil
}

// PostComment posts a comment on the PR or issue identified by
// number. GitHub uses the same /repos/{owner}/{repo}/issues/{n}/comments
// endpoint for both PR and issue comments, so the issue endpoint is
// the safe single-shot path. The PR endpoint is tried first to honor
// the documented translation rule (PR-first, fall back to issue on
// 404); when go-scm's PR CreateComment surfaces ErrNotSupported (its
// PR comments call hits a review endpoint) we fall back transparently.
func (h *Host) PostComment(ctx context.Context, repo string, number int, body string) (repohost.Comment, error) {
	if body == "" {
		return repohost.Comment{}, errors.New("github: comment body must not be empty")
	}
	if _, _, err := splitRepo(repo); err != nil {
		return repohost.Comment{}, fmt.Errorf("github: post comment: %w", err)
	}
	in := &scm.CommentInput{Body: body}

	c, resp, err := h.client.PullRequests.CreateComment(ctx, repo, number, in)
	if err == nil {
		return mapComment(c), nil
	}
	// Fall back to the issue endpoint on 404 or on go-scm's
	// "not supported" sentinel (some drivers route PR comments
	// through review endpoints). All other errors propagate.
	if !is404(resp) && !errors.Is(err, scm.ErrNotSupported) {
		return repohost.Comment{}, fmt.Errorf("github: post comment: %w", err)
	}
	c, resp, err = h.client.Issues.CreateComment(ctx, repo, number, in)
	if err != nil {
		return repohost.Comment{}, wrapErr("post comment", err, resp, repohost.ErrRepoNotFound)
	}
	return mapComment(c), nil
}

// splitRepo validates "owner/name". GitHub has no sub-groups so the
// path must be exactly two non-empty segments.
func splitRepo(repo string) (string, string, error) {
	parts := strings.Split(repo, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("repo must be 'owner/name', got %q", repo)
	}
	return parts[0], parts[1], nil
}

// pullListOptions translates a [repohost.Filter] into go-scm's
// PullRequestListOptions. Zero-value filter is treated as open-only.
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

// issueListOptions mirrors pullListOptions for issues.
func issueListOptions(f repohost.Filter) scm.IssueListOptions {
	opts := scm.IssueListOptions{}
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

// matchAuthorLabel filters a PullRequest by Filter.Author and
// Filter.Label client-side.
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

// matchAuthorLabelIssue is the issue counterpart of matchAuthorLabel.
func matchAuthorLabelIssue(is repohost.Issue, f repohost.Filter) bool {
	if f.Author != "" && is.Author != f.Author {
		return false
	}
	if f.Label != "" {
		for _, l := range is.Labels {
			if l == f.Label {
				return true
			}
		}
		return false
	}
	return true
}

// isPullRequest reports whether a go-scm Issue actually represents a
// pull request. GitHub's /issues endpoint returns PRs alongside
// issues, with the embedded PullRequest field populated.
func isPullRequest(is *scm.Issue) bool {
	if is == nil {
		return false
	}
	return is.PullRequest.Link != "" || is.PullRequest.Diff != ""
}

// is404 reports whether a go-scm response carries a 404 status.
func is404(resp *scm.Response) bool {
	return resp != nil && resp.Status == http.StatusNotFound
}

// wrapErr maps a go-scm error into the kit-unified sentinel when the
// response status is 404 (or the error is scm.ErrNotFound); otherwise
// it wraps the original error with a "github: <op>: ..." prefix.
func wrapErr(op string, err error, resp *scm.Response, sentinel error) error {
	if is404(resp) || errors.Is(err, scm.ErrNotFound) {
		return fmt.Errorf("github: %s: %w", op, sentinel)
	}
	return fmt.Errorf("github: %s: %w", op, err)
}

// mapPullRequest converts go-scm's *scm.PullRequest into the unified
// repohost.PullRequest shape.
func mapPullRequest(pr *scm.PullRequest) repohost.PullRequest {
	out := repohost.PullRequest{
		Labels: []string{},
		Raw:    map[string]any{},
	}
	if pr == nil {
		return out
	}
	out.Number = pr.Number
	out.Title = pr.Title
	out.Author = pr.Author.Login
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
	if pr.Draft {
		out.Raw["draft"] = true
	}
	if pr.Sha != "" {
		out.Raw["head_sha"] = pr.Sha
	}
	out.Raw["state_raw"] = stateRaw(pr.Closed, pr.Merged)
	return out
}

func mapIssue(is *scm.Issue) repohost.Issue {
	out := repohost.Issue{
		Labels: []string{},
		Raw:    map[string]any{},
	}
	if is == nil {
		return out
	}
	out.Number = is.Number
	out.Title = is.Title
	out.Author = is.Author.Login
	if is.Closed {
		out.State = "closed"
	} else {
		out.State = "open"
	}
	out.URL = is.Link
	out.CreatedAt = is.Created
	out.UpdatedAt = is.Updated
	out.Labels = append(out.Labels, is.Labels...)
	if is.Body != "" {
		out.Raw["body"] = is.Body
	}
	if is.Locked {
		out.Raw["locked"] = true
	}
	out.Raw["state_raw"] = issueStateRaw(is.Closed)
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
	out.Author = c.Author.Name
	out.Email = c.Author.Email
	if !c.Author.Date.IsZero() {
		out.CreatedAt = c.Author.Date
	} else if !c.Committer.Date.IsZero() {
		out.CreatedAt = c.Committer.Date
	}
	if c.Committer.Login != "" || c.Committer.Email != "" {
		out.Raw["committer"] = map[string]any{
			"name":  c.Committer.Name,
			"email": c.Committer.Email,
			"login": c.Committer.Login,
		}
	}
	if len(c.Files) > 0 {
		files := make([]string, 0, len(c.Files))
		for _, f := range c.Files {
			files = append(files, f.FileName)
		}
		out.Raw["files"] = files
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
	if r.Visibility != 0 {
		out.Raw["visibility"] = r.Visibility.String()
	}
	if r.Archived {
		out.Raw["archived"] = true
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
	out.Author = c.Author.Login
	out.Body = c.Body
	out.CreatedAt = c.Created
	if !c.Updated.IsZero() && c.Updated != c.Created {
		out.Raw["updated_at"] = c.Updated
	}
	return out
}

// prState collapses go-scm's Closed+Merged booleans to the kit closed
// enum. Merged wins over closed; absence of both means open.
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

// stateRaw reproduces GitHub's native state string from go-scm's
// boolean flags so adopters that read Raw["state_raw"] keep getting
// the wire-shape value.
func stateRaw(closed, merged bool) string {
	if closed || merged {
		return "closed"
	}
	return "open"
}

func issueStateRaw(closed bool) string {
	if closed {
		return "closed"
	}
	return "open"
}
