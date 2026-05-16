package mock

import (
	"context"
	"fmt"
	"sync"

	"hop.top/kit/go/integrations/repohost"
)

// state holds the configurable knob values for the mock driver.
// Module-level state is guarded by mu so parallel test goroutines do
// not corrupt each other.
type state struct {
	mu       sync.Mutex
	prs      *[]repohost.PullRequest
	issues   *[]repohost.Issue
	commits  map[string]repohost.Commit
	repos    map[string]repohost.Repo
	comment  *repohost.Comment
	errorMap map[string]error
}

var s = &state{
	commits:  map[string]repohost.Commit{},
	repos:    map[string]repohost.Repo{},
	errorMap: map[string]error{},
}

func init() {
	repohost.RegisterDriver("gitee-mock", openMock)
}

// openMock is the [repohost.Opener] for the gitee-mock provider.
func openMock(cfg repohost.Config) (repohost.MutableHost, error) {
	if cfg.Provider != "gitee-mock" {
		return nil, fmt.Errorf("gitee-mock: provider mismatch: got %q", cfg.Provider)
	}
	return &Host{}, nil
}

// SetPullRequests configures the slice returned by ListPullRequests.
func SetPullRequests(prs []repohost.PullRequest) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]repohost.PullRequest, len(prs))
	copy(cp, prs)
	s.prs = &cp
}

// SetIssues configures the slice returned by ListIssues.
func SetIssues(issues []repohost.Issue) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]repohost.Issue, len(issues))
	copy(cp, issues)
	s.issues = &cp
}

// SetCommit configures the commit returned by GetCommit for sha.
func SetCommit(sha string, c repohost.Commit) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commits[sha] = c
}

// SetRepo configures the repo returned by GetRepo for name.
func SetRepo(name string, r repohost.Repo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.repos[name] = r
}

// SetComment configures the comment returned by PostComment.
func SetComment(c repohost.Comment) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := c
	s.comment = &cp
}

// SetError makes the named method return err on its next call.
// Method names: "ListPullRequests", "ListIssues", "GetCommit",
// "GetRepo", "PostComment".
func SetError(method string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil {
		delete(s.errorMap, method)
		return
	}
	s.errorMap[method] = err
}

// Reset clears all knob state back to defaults so each test starts
// clean.
func Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prs = nil
	s.issues = nil
	s.commits = map[string]repohost.Commit{}
	s.repos = map[string]repohost.Repo{}
	s.comment = nil
	s.errorMap = map[string]error{}
}

// Host implements [repohost.MutableHost] for tests. Methods read
// knob state under a mutex; defaults come from [repohost.Baseline].
type Host struct{}

var _ repohost.MutableHost = (*Host)(nil)

// ListPullRequests returns the configured PR slice or a single
// Baseline PR when no knob is set.
func (h *Host) ListPullRequests(_ context.Context, _ string, _ repohost.Filter) ([]repohost.PullRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err, ok := s.errorMap["ListPullRequests"]; ok {
		return nil, err
	}
	if s.prs != nil {
		out := make([]repohost.PullRequest, len(*s.prs))
		copy(out, *s.prs)
		return out, nil
	}
	_, pr, _, _, _ := repohost.Baseline()
	return []repohost.PullRequest{pr}, nil
}

// ListIssues returns the configured issue slice or a single Baseline
// issue when no knob is set.
func (h *Host) ListIssues(_ context.Context, _ string, _ repohost.Filter) ([]repohost.Issue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err, ok := s.errorMap["ListIssues"]; ok {
		return nil, err
	}
	if s.issues != nil {
		out := make([]repohost.Issue, len(*s.issues))
		copy(out, *s.issues)
		return out, nil
	}
	_, _, is, _, _ := repohost.Baseline()
	return []repohost.Issue{is}, nil
}

// GetCommit returns the configured commit for sha, or the Baseline
// commit when no knob is set for that SHA.
func (h *Host) GetCommit(_ context.Context, _ string, sha string) (repohost.Commit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err, ok := s.errorMap["GetCommit"]; ok {
		return repohost.Commit{}, err
	}
	if c, ok := s.commits[sha]; ok {
		return c, nil
	}
	_, _, _, c, _ := repohost.Baseline()
	return c, nil
}

// GetRepo returns the configured repo metadata for name or the
// Baseline repo when no knob is set for that name.
func (h *Host) GetRepo(_ context.Context, name string) (repohost.Repo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err, ok := s.errorMap["GetRepo"]; ok {
		return repohost.Repo{}, err
	}
	if r, ok := s.repos[name]; ok {
		return r, nil
	}
	r, _, _, _, _ := repohost.Baseline()
	return r, nil
}

// PostComment returns the configured comment or the Baseline comment
// when no knob is set.
func (h *Host) PostComment(_ context.Context, _ string, _ int, _ string) (repohost.Comment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err, ok := s.errorMap["PostComment"]; ok {
		return repohost.Comment{}, err
	}
	if s.comment != nil {
		return *s.comment, nil
	}
	_, _, _, _, c := repohost.Baseline()
	return c, nil
}
