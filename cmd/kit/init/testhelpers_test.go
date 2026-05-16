// Shared test doubles for white-box tests in package kitinit (T-0952).
// Recording runners satisfy HookRunner / GitRunner / GitHubRunner; capture
// invocation arguments without shelling out. Single source replaces the
// per-file stubHookRunner / noopHookRunner / stubGitRunner / stubGitHubRunner
// duplicates that bootstrap_test.go and augment_test.go used to carry.
package kitinit

import (
	"context"
	"io"

	tmpl "hop.top/kit/internal/template"
)

// hookCall captures one HookRunner.Run invocation.
type hookCall struct {
	phase   string
	scripts []string
}

// recordingHookRunner satisfies HookRunner; records phase + scripts; never errors.
type recordingHookRunner struct {
	calls []hookCall
}

func (r *recordingHookRunner) Run(_ context.Context, phase string, scripts []string,
	_ string, _ tmpl.HookContext, _ io.Writer,
) error {
	r.calls = append(r.calls, hookCall{phase: phase, scripts: scripts})
	return nil
}

// recordingGitRunner satisfies GitRunner; records call dirs; never invokes real git.
type recordingGitRunner struct {
	initCalls   []string
	commitCalls []string
	pushCalls   []string
}

func (r *recordingGitRunner) Init(_ context.Context, dir string, _ bool, _ string) (bool, error) {
	r.initCalls = append(r.initCalls, dir)
	return false, nil
}

func (r *recordingGitRunner) InitialCommit(_ context.Context, dir, _ string) error {
	r.commitCalls = append(r.commitCalls, dir)
	return nil
}

func (r *recordingGitRunner) Push(_ context.Context, dir string) error {
	r.pushCalls = append(r.pushCalls, dir)
	return nil
}

// recordingGitHubRunner satisfies GitHubRunner; records calls; returns synthetic
// info; never shells out.
type recordingGitHubRunner struct {
	createCalls  []RepoConfig
	protectCalls []string
}

func (r *recordingGitHubRunner) Create(_ context.Context, _ string, cfg RepoConfig) (RepoInfo, error) {
	r.createCalls = append(r.createCalls, cfg)
	return RepoInfo{
		Repo:       cfg.Owner + "/" + cfg.Name,
		URL:        "https://github.com/" + cfg.Owner + "/" + cfg.Name,
		Visibility: cfg.Visibility,
	}, nil
}

func (r *recordingGitHubRunner) ProtectMain(_ context.Context, fullName string) error {
	r.protectCalls = append(r.protectCalls, fullName)
	return nil
}
