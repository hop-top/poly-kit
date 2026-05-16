package grade

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"hop.top/kit/go/conformance/client"
)

// MarkerComment is the magic string the upsert algorithm looks for
// to identify the kit-conformance-grade comment to update. Adopters
// must not edit this line out of any comment we post.
const MarkerComment = "<!-- kit-conformance-grade -->"

// GitHubAPIBase is the GitHub REST root. Overridable for tests.
var GitHubAPIBase = "https://api.github.com"

// postPRComment posts (or updates) the verdict comment on the
// currently-active GitHub PR. Discovers the PR number from
// GITHUB_REF (refs/pull/N/merge). Missing GITHUB_TOKEN or
// GITHUB_REPOSITORY is a warn-only soft-fail.
func postPRComment(ctx context.Context, res *client.Result) error {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return fmt.Errorf("GITHUB_TOKEN unset; skipping pr-comment")
	}
	repo := os.Getenv("GITHUB_REPOSITORY")
	if repo == "" {
		return fmt.Errorf("GITHUB_REPOSITORY unset; skipping pr-comment")
	}
	prNumber, err := parsePRNumberFromRef(os.Getenv("GITHUB_REF"))
	if err != nil {
		return fmt.Errorf("cannot derive PR number: %w", err)
	}

	body := buildCommentBody(res)
	g := &ghClient{baseURL: GitHubAPIBase, token: token, repo: repo, http: http.DefaultClient}
	return g.UpsertPRComment(ctx, prNumber, body)
}

// parsePRNumberFromRef parses "refs/pull/N/merge" or
// "refs/pull/N/head". Returns 0 for any other shape.
func parsePRNumberFromRef(ref string) (int, error) {
	if !strings.HasPrefix(ref, "refs/pull/") {
		return 0, fmt.Errorf("GITHUB_REF %q is not a pull-request ref", ref)
	}
	rest := strings.TrimPrefix(ref, "refs/pull/")
	idx := strings.Index(rest, "/")
	if idx < 0 {
		return 0, fmt.Errorf("malformed pull ref %q", ref)
	}
	n, err := strconv.Atoi(rest[:idx])
	if err != nil {
		return 0, fmt.Errorf("PR number is not an integer in %q", ref)
	}
	return n, nil
}

// buildCommentBody returns the markdown body for the PR comment. The
// first line is always the marker so future upserts can find it.
func buildCommentBody(r *client.Result) string {
	var b strings.Builder
	b.WriteString(MarkerComment)
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "## Conformance grade — verdict: **%s**\n\n", strings.ToUpper(r.Verdict))
	b.WriteString("| Field | Value |\n|-------|-------|\n")
	if r.ScenarioID != "" {
		fmt.Fprintf(&b, "| Scenario | `%s` |\n", r.ScenarioID)
	}
	if r.Tier > 0 {
		fmt.Fprintf(&b, "| Tier | %d |\n", r.Tier)
	}
	if r.ScoredAt != "" {
		fmt.Fprintf(&b, "| Graded at | %s |\n", r.ScoredAt)
	}
	if r.GraderVersion != "" {
		fmt.Fprintf(&b, "| Grader | %s (rules %s) |\n", r.GraderVersion, r.RulesVersion)
	}
	if r.Reason != "" {
		fmt.Fprintf(&b, "| Reason | %s |\n", r.Reason)
	}
	if len(r.Facets) > 0 {
		b.WriteString("\n### Factor coverage\n\n")
		for _, f := range r.Facets {
			fmt.Fprintf(&b, "- factor %d: %s\n", f.Factor, f.Status)
		}
	}
	if du := detailsURL(); du != "" {
		fmt.Fprintf(&b, "\n[full result](%s)\n", du)
	}
	return b.String()
}

// detailsURL builds a link to the current Actions run if the CI
// supplies the right env vars. Empty otherwise.
func detailsURL() string {
	server := os.Getenv("GITHUB_SERVER_URL")
	repo := os.Getenv("GITHUB_REPOSITORY")
	runID := os.Getenv("GITHUB_RUN_ID")
	if server == "" || repo == "" || runID == "" {
		return ""
	}
	return fmt.Sprintf("%s/%s/actions/runs/%s", server, repo, runID)
}

// ghClient is a minimal REST client for the GitHub API. It handles
// the three endpoints the PR-comment upsert needs and the Checks API
// poster in statuscheck.go.
type ghClient struct {
	baseURL string
	token   string
	repo    string
	http    *http.Client
}

// UpsertPRComment finds the marker-bearing comment on the PR and
// PATCHes it; if none exists, POSTs a new one. Returns the first
// non-nil error encountered.
func (c *ghClient) UpsertPRComment(ctx context.Context, prNumber int, body string) error {
	existing, err := c.findCommentWithMarker(ctx, prNumber)
	if err != nil {
		return err
	}
	if existing != 0 {
		return c.patchComment(ctx, existing, body)
	}
	return c.postComment(ctx, prNumber, body)
}

type ghComment struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
}

// findCommentWithMarker walks the PR's comments looking for one whose
// body begins with MarkerComment. Returns 0 if none found.
func (c *ghClient) findCommentWithMarker(ctx context.Context, prNumber int) (int64, error) {
	page := 1
	for {
		u := fmt.Sprintf("%s/repos/%s/issues/%d/comments?per_page=100&page=%d",
			c.baseURL, c.repo, prNumber, page)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return 0, err
		}
		c.setHeaders(req)
		resp, err := c.http.Do(req)
		if err != nil {
			return 0, err
		}
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return 0, fmt.Errorf("list comments returned %d: %s",
				resp.StatusCode, strings.TrimSpace(string(body)))
		}
		var comments []ghComment
		err = json.NewDecoder(resp.Body).Decode(&comments)
		resp.Body.Close()
		if err != nil {
			return 0, err
		}
		for _, cm := range comments {
			if strings.HasPrefix(strings.TrimSpace(cm.Body), MarkerComment) {
				return cm.ID, nil
			}
		}
		if len(comments) < 100 {
			return 0, nil
		}
		page++
	}
}

func (c *ghClient) postComment(ctx context.Context, prNumber int, body string) error {
	u := fmt.Sprintf("%s/repos/%s/issues/%d/comments", c.baseURL, c.repo, prNumber)
	payload, _ := json.Marshal(map[string]string{"body": body})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	c.setHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("post comment returned %d: %s",
			resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

func (c *ghClient) patchComment(ctx context.Context, commentID int64, body string) error {
	u := fmt.Sprintf("%s/repos/%s/issues/comments/%d", c.baseURL, c.repo, commentID)
	payload, _ := json.Marshal(map[string]string{"body": body})
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, u, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	c.setHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("patch comment returned %d: %s",
			resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

func (c *ghClient) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "kit-conformance-client/"+client.ClientVersion)
}
