package mock_test

import (
	"context"
	"errors"
	"testing"

	"hop.top/kit/go/integrations/repohost"
	"hop.top/kit/go/integrations/repohost/gitee/mock"
)

func openHost(t *testing.T) repohost.MutableHost {
	t.Helper()
	t.Cleanup(mock.Reset)
	host, err := repohost.Open(context.Background(), repohost.Config{Provider: "gitee-mock"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return host
}

func TestMock_BaselineDefaults(t *testing.T) {
	host := openHost(t)
	ctx := context.Background()

	prs, err := host.ListPullRequests(ctx, "any", repohost.Filter{})
	if err != nil {
		t.Fatalf("ListPullRequests: %v", err)
	}
	if len(prs) != 1 || prs[0].Number != 1 || prs[0].State != "open" {
		t.Errorf("unexpected baseline PR: %+v", prs)
	}

	issues, err := host.ListIssues(ctx, "any", repohost.Filter{})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(issues) != 1 || issues[0].Number != 1 {
		t.Errorf("unexpected baseline issue: %+v", issues)
	}

	c, err := host.GetCommit(ctx, "any", "any")
	if err != nil {
		t.Fatalf("GetCommit: %v", err)
	}
	if c.SHA == "" || c.Author == "" {
		t.Errorf("baseline commit empty: %+v", c)
	}

	r, err := host.GetRepo(ctx, "any")
	if err != nil {
		t.Fatalf("GetRepo: %v", err)
	}
	if r.Owner == "" || r.Name == "" {
		t.Errorf("baseline repo empty: %+v", r)
	}

	cm, err := host.PostComment(ctx, "any", 1, "body")
	if err != nil {
		t.Fatalf("PostComment: %v", err)
	}
	if cm.ID == 0 {
		t.Errorf("baseline comment empty: %+v", cm)
	}
}

func TestMock_SetPullRequests(t *testing.T) {
	host := openHost(t)
	mock.SetPullRequests([]repohost.PullRequest{{Number: 7, Title: "custom"}})

	prs, err := host.ListPullRequests(context.Background(), "any", repohost.Filter{})
	if err != nil {
		t.Fatalf("ListPullRequests: %v", err)
	}
	if len(prs) != 1 || prs[0].Number != 7 || prs[0].Title != "custom" {
		t.Errorf("knob not applied: %+v", prs)
	}
}

func TestMock_SetIssues(t *testing.T) {
	host := openHost(t)
	mock.SetIssues([]repohost.Issue{{Number: 8, Title: "issue"}})

	issues, err := host.ListIssues(context.Background(), "any", repohost.Filter{})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(issues) != 1 || issues[0].Number != 8 {
		t.Errorf("knob not applied: %+v", issues)
	}
}

func TestMock_SetCommit(t *testing.T) {
	host := openHost(t)
	mock.SetCommit("deadbeef", repohost.Commit{SHA: "deadbeef", Message: "knobbed"})

	c, err := host.GetCommit(context.Background(), "any", "deadbeef")
	if err != nil {
		t.Fatalf("GetCommit: %v", err)
	}
	if c.SHA != "deadbeef" || c.Message != "knobbed" {
		t.Errorf("knob not applied: %+v", c)
	}

	c2, _ := host.GetCommit(context.Background(), "any", "other")
	if c2.SHA == "deadbeef" {
		t.Errorf("default leaked from set sha: %+v", c2)
	}
}

func TestMock_SetRepo(t *testing.T) {
	host := openHost(t)
	mock.SetRepo("foo/bar", repohost.Repo{Owner: "foo", Name: "bar", Private: true})

	r, err := host.GetRepo(context.Background(), "foo/bar")
	if err != nil {
		t.Fatalf("GetRepo: %v", err)
	}
	if !r.Private {
		t.Errorf("knob not applied: %+v", r)
	}
}

func TestMock_SetComment(t *testing.T) {
	host := openHost(t)
	mock.SetComment(repohost.Comment{ID: 42, Body: "knobbed"})

	c, err := host.PostComment(context.Background(), "any", 1, "body")
	if err != nil {
		t.Fatalf("PostComment: %v", err)
	}
	if c.ID != 42 || c.Body != "knobbed" {
		t.Errorf("knob not applied: %+v", c)
	}
}

func TestMock_SetError(t *testing.T) {
	host := openHost(t)
	want := errors.New("boom")
	mock.SetError("ListPullRequests", want)

	_, err := host.ListPullRequests(context.Background(), "any", repohost.Filter{})
	if !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}

	mock.SetError("ListIssues", want)
	_, err = host.ListIssues(context.Background(), "any", repohost.Filter{})
	if !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}

	mock.SetError("GetCommit", want)
	_, err = host.GetCommit(context.Background(), "any", "any")
	if !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}

	mock.SetError("GetRepo", want)
	_, err = host.GetRepo(context.Background(), "any")
	if !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}

	mock.SetError("PostComment", want)
	_, err = host.PostComment(context.Background(), "any", 1, "x")
	if !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}

	mock.SetError("ListPullRequests", nil)
	if _, err := host.ListPullRequests(context.Background(), "any", repohost.Filter{}); err != nil {
		t.Errorf("after clearing error knob: %v", err)
	}
}

func TestMock_Reset(t *testing.T) {
	mock.SetPullRequests([]repohost.PullRequest{{Number: 99}})
	mock.SetIssues([]repohost.Issue{{Number: 99}})
	mock.SetCommit("x", repohost.Commit{SHA: "x"})
	mock.SetRepo("x", repohost.Repo{Name: "x"})
	mock.SetComment(repohost.Comment{ID: 99})
	mock.SetError("ListPullRequests", errors.New("e"))

	mock.Reset()

	host, err := repohost.Open(context.Background(), repohost.Config{Provider: "gitee-mock"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	prs, err := host.ListPullRequests(context.Background(), "any", repohost.Filter{})
	if err != nil {
		t.Fatalf("after reset: %v", err)
	}
	if len(prs) != 1 || prs[0].Number != 1 {
		t.Errorf("Reset did not restore baseline: %+v", prs)
	}
}

func TestMock_ProviderMismatch(t *testing.T) {
	_, err := repohost.Open(context.Background(), repohost.Config{Provider: "gitee-mock"})
	if err != nil {
		t.Fatalf("expected success for gitee-mock: %v", err)
	}
}
