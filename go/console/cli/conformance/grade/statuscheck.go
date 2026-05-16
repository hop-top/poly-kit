package grade

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"hop.top/kit/go/conformance/client"
)

// CheckRunName is the name attribute on the Checks API check-run we
// create. Adopters can require this name in branch-protection
// settings.
const CheckRunName = "kit conformance grade"

// postStatusCheck posts a Checks API check-run for the current SHA.
// Idempotent: looks for an existing check-run with CheckRunName +
// GITHUB_SHA and PATCHes it; otherwise POSTs new.
func postStatusCheck(ctx context.Context, res *client.Result) error {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return fmt.Errorf("GITHUB_TOKEN unset; skipping status-check")
	}
	repo := os.Getenv("GITHUB_REPOSITORY")
	if repo == "" {
		return fmt.Errorf("GITHUB_REPOSITORY unset; skipping status-check")
	}
	sha := os.Getenv("GITHUB_SHA")
	if sha == "" {
		return fmt.Errorf("GITHUB_SHA unset; skipping status-check")
	}

	g := &ghClient{baseURL: GitHubAPIBase, token: token, repo: repo, http: http.DefaultClient}
	body := buildCheckRunPayload(sha, res)
	existing, err := g.findCheckRunID(ctx, sha)
	if err != nil {
		return err
	}
	if existing != 0 {
		return g.patchCheckRun(ctx, existing, body)
	}
	return g.postCheckRun(ctx, body)
}

// buildCheckRunPayload assembles the Checks API JSON. Conclusion
// mapping follows design.md §8: pass→success, fail→failure,
// ungradable→neutral.
func buildCheckRunPayload(sha string, r *client.Result) map[string]any {
	conclusion := "neutral"
	title := "verdict: " + r.Verdict
	switch r.Verdict {
	case client.VerdictPass:
		conclusion = "success"
	case client.VerdictFail:
		conclusion = "failure"
	case client.VerdictUngradable:
		conclusion = "neutral"
	}
	summary := r.Reason
	if summary == "" {
		summary = "scenario " + r.ScenarioID
	}
	payload := map[string]any{
		"name":       CheckRunName,
		"head_sha":   sha,
		"status":     "completed",
		"conclusion": conclusion,
		"output": map[string]any{
			"title":   title,
			"summary": summary,
		},
	}
	if du := detailsURL(); du != "" {
		payload["details_url"] = du
	}
	return payload
}

func (c *ghClient) findCheckRunID(ctx context.Context, sha string) (int64, error) {
	u := fmt.Sprintf("%s/repos/%s/commits/%s/check-runs?check_name=%s",
		c.baseURL, c.repo, sha, url.QueryEscape(CheckRunName))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, err
	}
	c.setHeaders(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("list check-runs returned %d: %s",
			resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var env struct {
		CheckRuns []struct {
			ID int64 `json:"id"`
		} `json:"check_runs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return 0, err
	}
	if len(env.CheckRuns) == 0 {
		return 0, nil
	}
	return env.CheckRuns[0].ID, nil
}

func (c *ghClient) postCheckRun(ctx context.Context, payload map[string]any) error {
	u := fmt.Sprintf("%s/repos/%s/check-runs", c.baseURL, c.repo)
	raw, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(raw))
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
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("post check-run returned %d: %s",
			resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (c *ghClient) patchCheckRun(ctx context.Context, id int64, payload map[string]any) error {
	u := fmt.Sprintf("%s/repos/%s/check-runs/%d", c.baseURL, c.repo, id)
	raw, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, u, bytes.NewReader(raw))
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
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("patch check-run returned %d: %s",
			resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}
