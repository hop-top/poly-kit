package gitlab

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/drone/go-scm/scm"

	"hop.top/kit/go/integrations/repohost"
)

// Host is the GitLab driver implementation of [repohost.MutableHost],
// implemented as a thin facade over github.com/drone/go-scm.
type Host struct {
	cfg     repohost.Config
	client  *scm.Client
	baseURL string
}

// Static interface check.
var _ repohost.MutableHost = (*Host)(nil)

// ListPullRequests returns merge requests matching f for the project
// identified by repo ("owner/name", optionally with sub-groups).
//
// State filter: open-only → Open=true, closed-only → Closed=true,
// both → Open=true Closed=true. Author and label filters are
// applied client-side after fetch (go-scm's GitLab driver does not
// expose AuthorUsername/Labels filters on the list endpoint).
func (h *Host) ListPullRequests(ctx context.Context, repo string, f repohost.Filter) ([]repohost.PullRequest, error) {
	opts := pullListOptions(f)
	mrs, resp, err := h.client.PullRequests.List(ctx, repo, opts)
	if err != nil {
		return nil, wrapErr("list prs", err, resp, repohost.ErrRepoNotFound)
	}
	out := make([]repohost.PullRequest, 0, len(mrs))
	for _, mr := range mrs {
		mapped := mapPullRequest(mr)
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

// ListIssues returns issues matching f for repo. Author and label
// filters are applied client-side for consistency with the other
// drivers.
func (h *Host) ListIssues(ctx context.Context, repo string, f repohost.Filter) ([]repohost.Issue, error) {
	opts := issueListOptions(f)
	issues, resp, err := h.client.Issues.List(ctx, repo, opts)
	if err != nil {
		return nil, wrapErr("list issues", err, resp, repohost.ErrRepoNotFound)
	}
	out := make([]repohost.Issue, 0, len(issues))
	for _, is := range issues {
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
	c, resp, err := h.client.Git.FindCommit(ctx, repo, sha)
	if err != nil {
		return repohost.Commit{}, wrapErr("get commit", err, resp, repohost.ErrCommitNotFound)
	}
	return mapCommit(c), nil
}

// GetRepo returns repository metadata for repo.
func (h *Host) GetRepo(ctx context.Context, repo string) (repohost.Repo, error) {
	r, resp, err := h.client.Repositories.Find(ctx, repo)
	if err != nil {
		return repohost.Repo{}, wrapErr("get repo", err, resp, repohost.ErrRepoNotFound)
	}
	return mapRepo(r), nil
}

// PostComment posts a note on the merge request or issue identified
// by number. Tries the MR endpoint first; on 404 falls back to the
// issue endpoint. Both 404 → ErrRepoNotFound.
func (h *Host) PostComment(ctx context.Context, repo string, number int, body string) (repohost.Comment, error) {
	if body == "" {
		return repohost.Comment{}, errors.New("gitlab: comment body must not be empty")
	}
	in := &scm.CommentInput{Body: body}

	c, resp, err := h.client.PullRequests.CreateComment(ctx, repo, number, in)
	if err == nil {
		return mapComment(c, h.commentURL(repo, number, "merge_requests"), c.ID), nil
	}
	if !is404(resp) && !errors.Is(err, scm.ErrNotSupported) {
		return repohost.Comment{}, fmt.Errorf("gitlab: post comment: %w", err)
	}

	c, resp, err = h.client.Issues.CreateComment(ctx, repo, number, in)
	if err != nil {
		return repohost.Comment{}, wrapErr("post comment", err, resp, repohost.ErrRepoNotFound)
	}
	return mapComment(c, h.commentURL(repo, number, "issues"), c.ID), nil
}

// commentURL constructs the canonical web URL for a note. GitLab
// notes don't expose per-comment URLs in the API response, so we
// build one from BaseURL + repo + kind + number + "#note_<id>".
func (h *Host) commentURL(repo string, number int, kind string) string {
	return fmt.Sprintf("%s/%s/-/%s/%d", h.baseURL, repo, kind, number)
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

func is404(resp *scm.Response) bool {
	return resp != nil && resp.Status == http.StatusNotFound
}

func wrapErr(op string, err error, resp *scm.Response, sentinel error) error {
	if is404(resp) || errors.Is(err, scm.ErrNotFound) {
		return fmt.Errorf("gitlab: %s: %w", op, sentinel)
	}
	return fmt.Errorf("gitlab: %s: %w", op, err)
}

func mapPullRequest(pr *scm.PullRequest) repohost.PullRequest {
	out := repohost.PullRequest{Labels: []string{}, Raw: map[string]any{}}
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
	if pr.Draft {
		out.Raw["draft"] = true
	}
	out.Raw["state_raw"] = stateRaw(pr.Closed, pr.Merged)
	return out
}

func mapIssue(is *scm.Issue) repohost.Issue {
	out := repohost.Issue{Labels: []string{}, Raw: map[string]any{}}
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
	}
	if c.Committer.Login != "" || c.Committer.Email != "" {
		out.Raw["committer"] = map[string]any{
			"name":  c.Committer.Name,
			"email": c.Committer.Email,
		}
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
	return out
}

// mapComment translates a go-scm Comment into the unified shape and
// synthesizes a per-note URL from the repo + kind path. id is passed
// in separately to keep the signature stable when c is nil.
func mapComment(c *scm.Comment, baseURL string, id int) repohost.Comment {
	out := repohost.Comment{Raw: map[string]any{}}
	if c == nil {
		out.URL = baseURL
		return out
	}
	out.ID = int64(c.ID)
	out.Author = c.Author.Login
	out.Body = c.Body
	out.CreatedAt = c.Created
	out.URL = fmt.Sprintf("%s#note_%d", baseURL, id)
	return out
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
		return "merged"
	case closed:
		return "closed"
	default:
		return "opened"
	}
}

func issueStateRaw(closed bool) string {
	if closed {
		return "closed"
	}
	return "opened"
}
