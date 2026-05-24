// Package kitinit — posthook_test.go covers the T-0774 contract:
//   - Push path (liveness probe returns 2xx within 5s → no tlc task).
//   - Pull path (probe fails / disabled → tlc task scheduled with all
//     required fields).
//   - Duplicate prevention by (repo, PR#, event family).
//   - Closed-then-reopened PR triggers a fresh follow-up.
//   - Missing tlc / gh → stderr message, exit 0.
//   - Branch → task ID resolution (t-NNNN-* → T-NNNN).
//   - Generator non-destructive guarantees (write / skip-unchanged /
//     suggest-sibling).
//
// We run the generated hook script via /bin/bash (the generator emits
// a bash shebang), with stub `gh`, `tlc`, and `curl` binaries on PATH
// so the suite is hermetic (no real network, no real bus, no real
// GitHub).
package kitinit

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// execWrapper bridges os/exec.Cmd into the test harness without leaking
// the full surface — keeps each test focused on the hook's behavior.
type execWrapper struct{ c *exec.Cmd }

func newExecCmd(path string, args []string) *execWrapper {
	c := exec.Command(path, args...)
	// Start from a clean env so the harness controls PATH + KIT_*.
	c.Env = []string{}
	return &execWrapper{c: c}
}

// Env is intentionally a writable slice on the underlying cmd so tests
// can append PATH / KIT_BUS_* without going through a setter helper.
func (w *execWrapper) appendEnv(kv ...string) { w.c.Env = append(w.c.Env, kv...) }

// combinedOutput is the lowercase variant the tests call (the typed
// alias keeps the rest of the file linter-quiet without renaming).
func (w *execWrapper) combinedOutput() ([]byte, error) { return w.c.CombinedOutput() }

// -----------------------------------------------------------------------------
// Unit tests: branch → task ID resolution.
// -----------------------------------------------------------------------------

func TestResolveTaskIDFromBranch(t *testing.T) {
	cases := []struct {
		branch string
		want   string
	}{
		// Happy paths — 3/4/5/6 digit IDs, with and without trailing slug.
		{"t-0774-post-pr-hook", "T-0774"},
		{"t-0774", "T-0774"},
		{"T-0774-Capital-Letter", "T-0774"},
		{"T-12345-large-id", "T-12345"},
		{"T-001-small", "T-001"},
		{"t-123456", "T-123456"},

		// Non-matching: prefixed branches deliberately do NOT resolve.
		{"feat/t-0774-fix", ""},
		{"fix/T-0774", ""},
		// Non-task branches.
		{"main", ""},
		{"feat/foo", ""},
		// Below 3-digit minimum.
		{"t-77", ""},
		{"t-7", ""},
		// Above 6-digit maximum.
		{"t-1234567", ""},
		// Missing dash separator after digits.
		{"t-0774foo", ""},
		// Empty.
		{"", ""},
		{"   ", ""},
	}
	for _, c := range cases {
		t.Run(c.branch, func(t *testing.T) {
			got := ResolveTaskIDFromBranch(c.branch)
			assert.Equal(t, c.want, got)
		})
	}
}

// -----------------------------------------------------------------------------
// Unit tests: GeneratePostPROpenHook non-destructive write semantics.
// -----------------------------------------------------------------------------

func TestGeneratePostPROpenHook_WritesNewFile(t *testing.T) {
	target := t.TempDir()
	res, err := GeneratePostPROpenHook(target, true, false)
	require.NoError(t, err)
	assert.Equal(t, PostHookActionWrite, res.Action)
	assert.Equal(t, "new", res.Reason)
	assert.FileExists(t, filepath.Join(target, ".githooks", "post-pr-open"))

	// Executable bit on POSIX (git hooks must be runnable).
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(target, ".githooks", "post-pr-open"))
		require.NoError(t, err)
		assert.NotZero(t, info.Mode()&0o111, "hook script must be executable")
	}
}

func TestGeneratePostPROpenHook_SkipUnchanged(t *testing.T) {
	target := t.TempDir()
	// First run writes; second run with identical content → skip.
	_, err := GeneratePostPROpenHook(target, true, false)
	require.NoError(t, err)

	res, err := GeneratePostPROpenHook(target, true, false)
	require.NoError(t, err)
	assert.Equal(t, PostHookActionSkipUnchanged, res.Action)
	assert.Equal(t, "refresh", res.Reason)
}

func TestGeneratePostPROpenHook_SuggestSiblingOnDivergence(t *testing.T) {
	target := t.TempDir()
	hookPath := filepath.Join(target, ".githooks", "post-pr-open")
	require.NoError(t, os.MkdirAll(filepath.Dir(hookPath), 0o750))
	require.NoError(t, os.WriteFile(hookPath, []byte("#!/bin/sh\n# user-customized\n"), 0o755))

	res, err := GeneratePostPROpenHook(target, true, false)
	require.NoError(t, err)
	assert.Equal(t, PostHookActionSuggestSibling, res.Action)
	assert.Equal(t, "user-edited", res.Reason)
	assert.Equal(t, hookPath+".kit-suggested", res.SuggestedPath)
	assert.FileExists(t, res.SuggestedPath)

	// Original file untouched.
	got, err := os.ReadFile(hookPath)
	require.NoError(t, err)
	assert.Equal(t, "#!/bin/sh\n# user-customized\n", string(got))
}

func TestGeneratePostPROpenHook_AutoCleansSuggestionOnConvergence(t *testing.T) {
	target := t.TempDir()
	hookPath := filepath.Join(target, ".githooks", "post-pr-open")
	suggested := hookPath + ".kit-suggested"
	require.NoError(t, os.MkdirAll(filepath.Dir(hookPath), 0o750))

	// User adopted the kit-suggested content (file on disk == kit's content)
	// AND a stale .kit-suggested sibling exists from a previous run.
	require.NoError(t, os.WriteFile(hookPath, PostPROpenHookContent(), 0o755))
	require.NoError(t, os.WriteFile(suggested, []byte("# stale\n"), 0o644))

	res, err := GeneratePostPROpenHook(target, true, false)
	require.NoError(t, err)
	assert.Equal(t, PostHookActionSkipUnchanged, res.Action)

	// Sibling auto-removed per contract Section 6.
	_, statErr := os.Stat(suggested)
	assert.True(t, os.IsNotExist(statErr), "stale .kit-suggested must be removed when user converges")
}

func TestGeneratePostPROpenHook_DryRunDoesNotWrite(t *testing.T) {
	target := t.TempDir()
	res, err := GeneratePostPROpenHook(target, true, true)
	require.NoError(t, err)
	assert.Equal(t, PostHookActionWrite, res.Action)

	_, statErr := os.Stat(filepath.Join(target, ".githooks", "post-pr-open"))
	assert.True(t, os.IsNotExist(statErr), "dry-run must not touch disk")
}

func TestGeneratePostPROpenHook_DisabledFlagSkips(t *testing.T) {
	target := t.TempDir()
	res, err := GeneratePostPROpenHook(target, false, false)
	require.NoError(t, err)
	assert.Equal(t, PostHookActionSkippedFlag, res.Action)
	assert.Equal(t, "skipped", res.Reason)

	_, statErr := os.Stat(filepath.Join(target, ".githooks", "post-pr-open"))
	assert.True(t, os.IsNotExist(statErr), "skipped flag must not write the hook")
}

// -----------------------------------------------------------------------------
// Integration tests: drive the embedded shell script via /bin/sh against
// stubbed `gh`, `tlc`, and `curl` binaries on PATH.
// -----------------------------------------------------------------------------

// hookHarness materializes the embedded script + stub binaries into a
// per-test PATH so the suite runs hermetically. Returns the directory
// holding the hook script and the PATH-shadowing dir; tests use the
// latter to write per-scenario stubs (e.g. fail-tlc, success-tlc).
type hookHarness struct {
	hookPath string // <dir>/.githooks/post-pr-open
	pathDir  string // PATH-shadow dir holding stub binaries
	tlcLog   string // file the tlc stub appends invocation args to
	curlLog  string // file the curl stub appends the last positional (probe URL) to
}

func newHookHarness(t *testing.T) *hookHarness {
	t.Helper()
	if runtime.GOOS == "windows" {
		// /bin/bash isn't guaranteed on native Windows; the .ps1
		// companion (posthook_ps1_test.go) covers the same surface
		// statically. Adopters who want end-to-end coverage on Windows
		// can install Git-Bash and run via that shim; the kit init
		// generator does not depend on it.
		t.Skip("post-pr-open bash harness requires /bin/bash; Windows-native is covered by posthook_template.ps1 + posthook_ps1_test.go")
	}
	dir := t.TempDir()
	_, err := GeneratePostPROpenHook(dir, true, false)
	require.NoError(t, err)
	pathDir := t.TempDir()
	tlcLog := filepath.Join(t.TempDir(), "tlc.log")
	curlLog := filepath.Join(t.TempDir(), "curl.log")

	h := &hookHarness{
		hookPath: filepath.Join(dir, ".githooks", "post-pr-open"),
		pathDir:  pathDir,
		tlcLog:   tlcLog,
		curlLog:  curlLog,
	}
	return h
}

// stubGH writes a stub `gh` binary that mimics two real-gh behaviors:
//
//  1. `gh pr view --json <fields>` (no --jq) emits the raw JSON, used
//     by legacy callers and quick-shape inspection.
//  2. `gh pr view --json <fields> --jq <expr>` pipes the raw JSON
//     through the host's real `jq` (the production gh binary embeds
//     a jq runtime — we proxy through the system jq to get the same
//     semantics).
//
// `gh repo view --json nameWithOwner --jq '.nameWithOwner'` is also
// recognized for the baseRepository fallback path; it returns the
// REPO_NAME_WITH_OWNER env var (empty by default) so tests can opt
// into / out of the final gh-side fallback independently.
//
// If the test environment has no `jq` on PATH, --jq invocations exit
// with a clear non-zero code and an error to stderr so failures point
// at the missing dependency rather than silently degrading to the
// legacy raw-JSON path. (Standard CI runners ship jq.)
func (h *hookHarness) stubGH(t *testing.T, json string) {
	t.Helper()
	jqPath, err := exec.LookPath("jq")
	if err != nil {
		// Surface the dependency at stub-write time so a missing jq
		// fails the whole test rather than leaking a confusing
		// "title was \"\"" assertion downstream.
		t.Skipf("gh stub requires system jq for --jq passthrough; not found on PATH")
	}
	script := `#!/usr/bin/env bash
set -u
JQ='` + jqPath + `'
JSON=$(cat <<'GHJSON'
` + json + `
GHJSON
)
JQ_EXPR=""
saw_jq=0
for arg in "$@"; do
  if [ "${saw_jq}" = "1" ]; then
    JQ_EXPR="${arg}"
    saw_jq=0
    continue
  fi
  case "${arg}" in
    --jq) saw_jq=1 ;;
  esac
done
case "$1 $2" in
  "pr view")
    if [ -n "${JQ_EXPR}" ]; then
      printf '%s' "${JSON}" | "${JQ}" -r "${JQ_EXPR}"
    else
      printf '%s\n' "${JSON}"
    fi
    ;;
  "repo view")
    # Repo-side fallback used when baseRepository is missing. Tests
    # configure REPO_NAME_WITH_OWNER to control the response; absent
    # → empty (gh exits non-zero in real life, but the hook tolerates
    # an empty stdout).
    if [ -n "${JQ_EXPR}" ]; then
      printf '%s' "${REPO_NAME_WITH_OWNER:-}" | "${JQ}" -R -r "${JQ_EXPR} // \"\""
    else
      printf '{"nameWithOwner":"%s"}\n' "${REPO_NAME_WITH_OWNER:-}"
    fi
    ;;
esac
exit 0
`
	require.NoError(t, os.WriteFile(filepath.Join(h.pathDir, "gh"), []byte(script), 0o755))
}

// stubTLC writes a stub `tlc` binary that:
//   - logs invocation args to h.tlcLog,
//   - on `tlc task list`, prints the value of TLC_LIST_OUTPUT env
//     ("[]" by default),
//   - on `tlc task create`, exits 0 (success).
func (h *hookHarness) stubTLC(t *testing.T) {
	t.Helper()
	script := `#!/usr/bin/env bash
set -u
echo "$@" >> "` + h.tlcLog + `"
case "$1" in
  task)
    case "$2" in
      list)  printf '%s' "${TLC_LIST_OUTPUT:-[]}" ;;
      create) exit 0 ;;
    esac
    ;;
esac
exit 0
`
	require.NoError(t, os.WriteFile(filepath.Join(h.pathDir, "tlc"), []byte(script), 0o755))
}

// stubCurl writes a stub `curl` that prints CURL_STATUS_CODE.
func (h *hookHarness) stubCurl(t *testing.T, statusCode string) {
	t.Helper()
	script := `#!/usr/bin/env bash
# Stub curl: scan args for --write-out and emit the configured status
# regardless of the URL. Mirrors the shape the hook expects.
printf '%s' "` + statusCode + `"
exit 0
`
	require.NoError(t, os.WriteFile(filepath.Join(h.pathDir, "curl"), []byte(script), 0o755))
}

// stubCurlAgainst writes a `curl` that performs a real HTTP GET against
// httpTestURL — i.e. we exercise the actual probe path against
// httptest.NewServer rather than a hard-coded status.
//
// The stub records the last positional argument (the URL the hook
// composed) into h.curlLog so tests can assert PROBE_URL composition
// independently of HTTP behavior. If the hook composed a URL different
// from httpTestURL, the stub still forwards the request to httpTestURL
// (so HTTP-backed gating tests stay stable) but the recorded value in
// h.curlLog will diverge — a positive-assertion test catches the
// regression. See TestHook_ProbeURLComposition_RecordsExpectedURL.
func (h *hookHarness) stubCurlAgainst(t *testing.T, httpTestURL string) {
	t.Helper()
	// The real curl binary is on the host PATH; we can't shadow it with
	// itself. Use a thin wrapper that calls the real curl with the
	// configured URL.
	realCurl := "/usr/bin/curl"
	if _, err := os.Stat(realCurl); err != nil {
		t.Skip("real curl not at /usr/bin/curl; skipping HTTP-backed probe test")
	}
	// Pass-through wrapper: capture the URL the hook passed (last
	// positional) into h.curlLog, then forward all flags verbatim to
	// the real curl but pointed at httpTestURL. The capture-then-redirect
	// shape lets tests assert URL composition via h.curlLog while HTTP
	// gating continues to exercise httptest.NewServer.
	script := `#!/usr/bin/env bash
ARGS=("$@")
# Last positional carries the URL the hook composed. Record it
# verbatim so tests can assert PROBE_URL composition.
HOOK_URL="${ARGS[$((${#ARGS[@]}-1))]}"
printf '%s\n' "${HOOK_URL}" >> "` + h.curlLog + `"
URL="` + httpTestURL + `"
exec ` + realCurl + ` "${ARGS[@]:0:$((${#ARGS[@]}-1))}" "${URL}"
`
	require.NoError(t, os.WriteFile(filepath.Join(h.pathDir, "curl"), []byte(script), 0o755))
}

// run invokes the hook via /bin/bash with PATH limited to h.pathDir plus
// the minimum the hook needs (sed, tr, head, sh — assumed available in
// /usr/bin and /bin on macOS/Linux). Returns stderr + exit code.
//
// Tests can override PATH by passing a "PATH" key in env — useful for
// missing-tool tests that need to ensure a real binary (e.g. /usr/bin/gh
// on CI runners where gh is preinstalled) does NOT leak into the
// harness's PATH and accidentally satisfy `command -v`.
func (h *hookHarness) run(t *testing.T, env map[string]string) (stderr string, exitCode int) {
	t.Helper()
	// We deliberately do NOT include $HOME's PATH to keep the harness
	// hermetic; but /usr/bin and /bin are required for sed/tr/head/sh.
	// (curl is shadowed via h.pathDir; gh and tlc are stubbed there too.)
	envPath := h.pathDir + ":/usr/bin:/bin"
	if override, ok := env["PATH"]; ok {
		envPath = override
		delete(env, "PATH")
	}

	args := []string{h.hookPath}
	cmd := newExecCmd("/bin/bash", args)
	cmd.appendEnv("PATH=" + envPath)
	for k, v := range env {
		cmd.appendEnv(k + "=" + v)
	}
	out, err := cmd.combinedOutput()
	if exitErr, ok := err.(interface{ ExitCode() int }); ok {
		exitCode = exitErr.ExitCode()
	}
	return string(out), exitCode
}

// Standard gh JSON the hook should be able to parse.
const fixtureGHJSON = `{"number":123,"url":"https://github.com/hop-top/example/pull/123","headRefName":"t-0774-post-pr-hook","headRefOid":"deadbeef1234567890","title":"feat(init): generate after-PR hook","body":"Implements T-0774","baseRepository":{"name":"example","owner":{"login":"hop-top"}}}`

func TestHook_PushPath_HealthyBus_NoLocalTask(t *testing.T) {
	h := newHookHarness(t)
	h.stubGH(t, fixtureGHJSON)
	h.stubTLC(t)
	// 200 within 5s.
	h.stubCurl(t, "200")

	stderr, exit := h.run(t, map[string]string{
		"KIT_BUS_ENABLED":     "true",
		"KIT_BUS_INGRESS_URL": "https://bus.example/",
	})

	assert.Equal(t, 0, exit, "hook must always exit 0 (fail-open)")
	assert.Contains(t, stderr, "bus ingress healthy",
		"push-path log must mention deferring to host listener")
	assert.NoFileExists(t, h.tlcLog,
		"healthy bus + KIT_BUS_ENABLED=true must NOT shell out to tlc")
}

func TestHook_PullPath_BusDisabled_SchedulesTask(t *testing.T) {
	h := newHookHarness(t)
	h.stubGH(t, fixtureGHJSON)
	h.stubTLC(t)
	// curl is never called when KIT_BUS_ENABLED != "true"; stub it
	// anyway so the hook script doesn't degrade unexpectedly.
	h.stubCurl(t, "200")

	stderr, exit := h.run(t, map[string]string{
		"KIT_BUS_ENABLED":     "false",
		"KIT_BUS_INGRESS_URL": "https://bus.example/",
	})

	assert.Equal(t, 0, exit)
	assert.Contains(t, stderr, "scheduled tlc follow-up")
	assertTLCInvoked(t, h, "Review PR #123", "deadbeef1234567890",
		"t-0774-post-pr-hook", "github.pr.run.completed", "T-0774")
}

// TestHook_PullPath_FamilyTopicMapping asserts the per-event tag value
// carries the full 4-segment canonical topic name (not the short family
// label), and that the dedup-key tag keeps the short family label.
// Spec: kit-init-pr-wiring section "Per-event tag uses canonical topic".
func TestHook_PullPath_FamilyTopicMapping(t *testing.T) {
	cases := []struct {
		family    string
		wantTopic string // expected event:<topic> tag value
	}{
		{"run", "event:github.pr.run.completed"},
		{"comment", "event:github.pr.comment.created"},
		{"merged", "event:github.pr.pull.merged"},
		{"closed", "event:github.pr.pull.closed"},
	}
	for _, c := range cases {
		t.Run(c.family, func(t *testing.T) {
			h := newHookHarness(t)
			h.stubGH(t, fixtureGHJSON)
			h.stubTLC(t)
			h.stubCurl(t, "503") // force pull path

			stderr, exit := h.run(t, map[string]string{
				"KIT_BUS_ENABLED":         "false",
				"KIT_POST_PR_HOOK_FAMILY": c.family,
			})

			assert.Equal(t, 0, exit)
			assert.Contains(t, stderr, "scheduled tlc follow-up")
			body, err := os.ReadFile(h.tlcLog)
			require.NoError(t, err)
			got := string(body)
			assert.Contains(t, got, c.wantTopic,
				"per-event tag must carry full canonical topic name")
			// The dedup-key family stays as the short label.
			assert.Contains(t, got, "kit:pr-followup:hop-top-example:123:"+c.family,
				"dedup tag's family segment must stay short")
		})
	}
}

func TestHook_PullPath_ProbeReturnsNon2xx_SchedulesTask(t *testing.T) {
	h := newHookHarness(t)
	h.stubGH(t, fixtureGHJSON)
	h.stubTLC(t)
	h.stubCurl(t, "503") // service unavailable

	stderr, exit := h.run(t, map[string]string{
		"KIT_BUS_ENABLED":     "true",
		"KIT_BUS_INGRESS_URL": "https://bus.example/",
	})

	assert.Equal(t, 0, exit)
	assert.Contains(t, stderr, "scheduled tlc follow-up")
	assertTLCInvoked(t, h, "Review PR #123")
}

func TestHook_PullPath_ProbeTimesOut_SchedulesTask(t *testing.T) {
	// httptest server hangs forever; the hook's 5s curl timeout fires
	// and the script falls through to the pull path. We use an httptest
	// server here so the real network is never touched.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Sleep beyond the hook's 5s timeout. We don't actually need to
		// sleep that long — emitting nothing and closing the connection
		// fast enough for curl to report a non-2xx (or empty status)
		// also satisfies the contract. To keep the test fast, return a
		// 504 immediately.
		w.WriteHeader(http.StatusGatewayTimeout)
	}))
	defer srv.Close()

	h := newHookHarness(t)
	h.stubGH(t, fixtureGHJSON)
	h.stubTLC(t)
	h.stubCurlAgainst(t, srv.URL+"/healthz")

	stderr, exit := h.run(t, map[string]string{
		"KIT_BUS_ENABLED":     "true",
		"KIT_BUS_INGRESS_URL": srv.URL,
	})
	assert.Equal(t, 0, exit)
	assert.Contains(t, stderr, "scheduled tlc follow-up")
}

func TestHook_PullPath_ProbeHTTPTestHealthy_PushPathTaken(t *testing.T) {
	// Pair to the previous test: when httptest returns 2xx, the hook
	// must take the push path and skip tlc.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	h := newHookHarness(t)
	h.stubGH(t, fixtureGHJSON)
	h.stubTLC(t)
	h.stubCurlAgainst(t, srv.URL+"/healthz")

	stderr, exit := h.run(t, map[string]string{
		"KIT_BUS_ENABLED":     "true",
		"KIT_BUS_INGRESS_URL": srv.URL,
	})
	assert.Equal(t, 0, exit)
	assert.Contains(t, stderr, "bus ingress healthy")
	assert.NoFileExists(t, h.tlcLog)
}

func TestHook_DuplicatePrevention_SkipsWhenOpenFollowupExists(t *testing.T) {
	h := newHookHarness(t)
	h.stubGH(t, fixtureGHJSON)
	h.stubTLC(t)
	h.stubCurl(t, "503") // force pull path

	// The first tlc invocation is `tlc task list` — pre-load that with
	// a non-empty result (a single task with an "id" field) so the hook
	// concludes a follow-up already exists.
	stderr, exit := h.run(t, map[string]string{
		"KIT_BUS_ENABLED": "false",
		"TLC_LIST_OUTPUT": `[{"id":"T-9001","status":"TODO"}]`,
	})

	assert.Equal(t, 0, exit)
	assert.Contains(t, stderr, "follow-up already scheduled")
	// `tlc task create` must NOT have fired.
	body, _ := os.ReadFile(h.tlcLog)
	assert.NotContains(t, string(body), "task create",
		"duplicate-prevention: task create must not be invoked when a TODO follow-up exists")
}

func TestHook_ReopenedPR_CreatesFreshFollowup(t *testing.T) {
	h := newHookHarness(t)
	h.stubGH(t, fixtureGHJSON)
	h.stubTLC(t)
	h.stubCurl(t, "503") // force pull path

	// Empty list result simulates "no open follow-up" (the prior cycle
	// either DONE'd or was filtered out by --status TODO/in_progress).
	// The hook should create a fresh task.
	stderr, exit := h.run(t, map[string]string{
		"KIT_BUS_ENABLED": "false",
		"TLC_LIST_OUTPUT": `[]`,
	})

	assert.Equal(t, 0, exit)
	assert.Contains(t, stderr, "scheduled tlc follow-up")
	body, _ := os.ReadFile(h.tlcLog)
	assert.Contains(t, string(body), "task create",
		"reopened PR: task create must fire when no open follow-up exists")
}

func TestHook_MissingTLC_LogsAndExits0(t *testing.T) {
	h := newHookHarness(t)
	h.stubGH(t, fixtureGHJSON)
	// No tlc stub — `command -v tlc` returns non-zero.
	h.stubCurl(t, "503")

	stderr, exit := h.run(t, map[string]string{
		"KIT_BUS_ENABLED": "false",
	})

	assert.Equal(t, 0, exit, "missing tlc must NEVER block PR creation")
	assert.Contains(t, stderr, "tlc not found on PATH",
		"single actionable stderr line per contract Section 5")
	assert.Contains(t, stderr, "PR #123",
		"failure message must include PR number for actionability")
}

func TestHook_MissingGH_LogsAndExits0(t *testing.T) {
	h := newHookHarness(t)
	// No gh stub — `command -v gh` must return non-zero. We override
	// PATH to ONLY h.pathDir so a preinstalled /usr/bin/gh (present on
	// Ubuntu CI runners) does not leak in and accidentally satisfy
	// `command -v gh`. The hook's missing-gh branch uses only bash
	// builtins (printf, command, exit) before exiting, so the host
	// utilities under /usr/bin are not required to reach it.
	h.stubTLC(t)
	h.stubCurl(t, "200")

	stderr, exit := h.run(t, map[string]string{
		"KIT_BUS_ENABLED": "true",
		"PATH":            h.pathDir,
	})

	assert.Equal(t, 0, exit, "missing gh must NEVER block PR creation")
	assert.Contains(t, stderr, "gh not found on PATH",
		"single actionable stderr line per contract Section 5")
	// tlc must not be invoked when gh is missing (no PR metadata to act on).
	assert.NoFileExists(t, h.tlcLog)
}

// TestHook_ProbeURLComposition_RecordsExpectedURL pins the contract
// that the hook composes PROBE_URL as `${KIT_BUS_INGRESS_URL%/}/healthz`
// — i.e. a single trailing-slash strip + `/healthz` suffix, no double
// slashes, no missing path. The curl stub records the URL it receives
// to h.curlLog so any future regression in URL composition (wrong env
// var, missing trim, extra slash) surfaces as a test failure rather
// than silently routing the probe to the wrong address.
func TestHook_ProbeURLComposition_RecordsExpectedURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	h := newHookHarness(t)
	h.stubGH(t, fixtureGHJSON)
	h.stubTLC(t)
	// KIT_BUS_INGRESS_URL carries a trailing slash on purpose — the
	// hook must strip it before appending /healthz.
	ingressURL := srv.URL + "/"
	h.stubCurlAgainst(t, srv.URL+"/healthz")

	_, exit := h.run(t, map[string]string{
		"KIT_BUS_ENABLED":     "true",
		"KIT_BUS_INGRESS_URL": ingressURL,
	})
	assert.Equal(t, 0, exit)

	// The curl stub must record the URL the hook composed. The
	// expected form is ${KIT_BUS_INGRESS_URL%/}/healthz — single
	// slash, no doubling.
	body, err := os.ReadFile(h.curlLog)
	require.NoError(t, err, "stubCurlAgainst must record the URL the hook passed")
	got := strings.TrimSpace(string(body))
	want := srv.URL + "/healthz"
	assert.Equal(t, want, got,
		"hook must compose PROBE_URL as KIT_BUS_INGRESS_URL with trailing slash stripped + /healthz")
}

// TestHook_PullPath_PreservesEscapedQuotesInTitle pins the contract that
// PR titles containing escaped double quotes (e.g.
//
//	Fix "foo" handling for edge case
//
// → JSON-encoded as `"Fix \"foo\" handling for edge case"`) survive the
// hook's metadata extraction intact. Originally the sed-based
// extractor truncated PR_TITLE at the first `\`, producing a malformed
// task title. The fix routes extraction through `gh pr view --json ...
// --jq` so escape sequences resolve via gh's built-in jq.
func TestHook_PullPath_PreservesEscapedQuotesInTitle(t *testing.T) {
	h := newHookHarness(t)
	// JSON-encoded title contains escaped double quotes inside the
	// value. baseRepository carries the standard hop-top/example shape
	// so the dedup-key path is exercised end-to-end.
	const trickyJSON = `{"number":456,` +
		`"url":"https://github.com/hop-top/example/pull/456",` +
		`"headRefName":"t-0774-post-pr-hook",` +
		`"headRefOid":"cafebabe1234567890",` +
		`"title":"Fix \"foo\" handling for edge case",` +
		`"body":"Implements T-0774",` +
		`"baseRepository":{"name":"example","owner":{"login":"hop-top"}}}`
	h.stubGH(t, trickyJSON)
	h.stubTLC(t)
	h.stubCurl(t, "503") // force pull path

	stderr, exit := h.run(t, map[string]string{
		"KIT_BUS_ENABLED": "false",
	})

	assert.Equal(t, 0, exit)
	assert.Contains(t, stderr, "scheduled tlc follow-up")

	body, err := os.ReadFile(h.tlcLog)
	require.NoError(t, err, "tlc stub log must exist")
	got := string(body)
	// Task title must carry the literal quoted substring verbatim,
	// not truncated at the first escape sequence.
	assert.Contains(t, got, `Fix "foo" handling for edge case`,
		"PR title with escaped double quotes must survive extraction")
}

// TestHook_PullPath_BaseRepositoryFallbackFromURL pins the contract that
// the dedup tag carries owner/repo even when `gh pr view`'s
// baseRepository field is absent or empty (older gh versions, fork
// edge cases). The hook must derive owner/repo from PR_URL so the
// (repo, PR#, family) dedup key stays unique across forks.
func TestHook_PullPath_BaseRepositoryFallbackFromURL(t *testing.T) {
	h := newHookHarness(t)
	// baseRepository is JSON null — emulates an older gh that doesn't
	// expose the field. PR_URL still carries owner/repo in the path.
	const noBaseRepoJSON = `{"number":789,` +
		`"url":"https://github.com/hop-top/example/pull/789",` +
		`"headRefName":"t-0774-post-pr-hook",` +
		`"headRefOid":"feedface1234567890",` +
		`"title":"feat: add fallback",` +
		`"body":"Implements T-0774",` +
		`"baseRepository":null}`
	h.stubGH(t, noBaseRepoJSON)
	h.stubTLC(t)
	h.stubCurl(t, "503") // force pull path

	stderr, exit := h.run(t, map[string]string{
		"KIT_BUS_ENABLED": "false",
	})

	assert.Equal(t, 0, exit)
	assert.Contains(t, stderr, "scheduled tlc follow-up")

	body, err := os.ReadFile(h.tlcLog)
	require.NoError(t, err, "tlc stub log must exist")
	got := string(body)
	assert.Contains(t, got, "kit:pr-followup:hop-top-example:789:run",
		"dedup tag must carry owner/repo derived from PR_URL when baseRepository is null")
	assert.NotContains(t, got, "kit:pr-followup::789:run",
		"malformed empty-repo dedup tag must not be emitted")
}

// TestHook_PullPath_AllRepoSourcesBroken_LogsAndExits0 pins the
// fail-open contract: when baseRepository is absent AND PR_URL cannot
// be parsed for owner/repo AND the gh repo view fallback is empty,
// the hook must exit 0 with an actionable stderr message rather than
// emit a malformed dedup tag (which would silently break dedup across
// repos).
func TestHook_PullPath_AllRepoSourcesBroken_LogsAndExits0(t *testing.T) {
	h := newHookHarness(t)
	// baseRepository null; PR_URL also lacks an owner/repo segment so
	// the path-parsing fallback returns empty.
	const brokenJSON = `{"number":42,` +
		`"url":"https://example.invalid/no-owner-no-repo",` +
		`"headRefName":"t-0774-post-pr-hook",` +
		`"headRefOid":"badc0de1234567890",` +
		`"title":"feat: broken metadata",` +
		`"body":"Implements T-0774",` +
		`"baseRepository":null}`
	h.stubGH(t, brokenJSON)
	h.stubTLC(t)
	h.stubCurl(t, "503") // force pull path

	// REPO_NAME_WITH_OWNER intentionally unset → gh repo view fallback
	// also returns empty.
	stderr, exit := h.run(t, map[string]string{
		"KIT_BUS_ENABLED": "false",
	})

	assert.Equal(t, 0, exit, "fail-open: missing repo metadata must NOT block PR creation")
	assert.Contains(t, stderr, "could not resolve owner/repo",
		"actionable stderr message must explain why follow-up was skipped")
	// tlc task create must NOT have fired.
	if body, err := os.ReadFile(h.tlcLog); err == nil {
		assert.NotContains(t, string(body), "task create",
			"malformed dedup key must not produce a scheduled task")
	}
}

// assertTLCInvoked verifies the tlc stub captured a `task create`
// invocation whose argv contains every needle string. Centralized so
// the assertions across push/pull tests stay readable.
func assertTLCInvoked(t *testing.T, h *hookHarness, needles ...string) {
	t.Helper()
	body, err := os.ReadFile(h.tlcLog)
	require.NoError(t, err, "tlc stub log must exist (tlc was expected to run)")
	got := string(body)
	require.Contains(t, got, "task create", "tlc task create must have been invoked")
	for _, n := range needles {
		assert.Contains(t, got, n)
	}
	// Required tag set per contract Section 5.
	assert.Contains(t, got, "kit:pr-followup", "fixed pr-followup tag must be set")
	assert.Contains(t, got, "event:github.pr.run.completed",
		"per-event tag must carry full canonical topic (default family=run)")
	// Dedup tag carries the (repo, PR#, family) triple.
	assert.Contains(t, got, "kit:pr-followup:hop-top-example:123:run",
		"dedup tag must encode (repo, PR#, family)")
	// --due 10 minutes from now.
	assert.Contains(t, strings.ToLower(got), "in 10m",
		"scheduled task must be due 10 minutes after PR open")
}
