// Tier gate for the cli-go template's agent-facing fragment.
//
// AGENTS.md describes the served surface to a caller that has no
// terminal, so it belongs wherever `serve` does and nowhere else: the
// tiers that ship the kit root (3 and 4), not the lint-only and
// CI-only tiers. These tests render the real template — not the
// synthetic fixture augment_test.go uses — because the claim is about
// this template's own tiers.yaml.
package kitinit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// augmentCLIGoAt renders the real cli-go template at the given tier
// into a fresh dir and returns it. Vars mirror runBootstrapFor's, so
// the rendered text carries the same tool name the serve test drives.
func augmentCLIGoAt(t *testing.T, tier int) string {
	t.Helper()
	if !builtinAvailable(t, "cli-go") {
		t.Skip("template not yet shipped: cli-go")
	}
	cwd := t.TempDir()
	initGitDir(t, cwd)

	deps, _ := fixtureDeps()
	in := baseInputs("cli-go", "demo", tier)
	in.Vars = map[string]any{
		"Name": "demo", "name": "demo",
		"Module": "github.com/example/demo", "module": "github.com/example/demo",
		"License": "MIT", "license": "MIT",
		"Author": "Test User", "author": "Test User",
		"Email": "test@example.com", "email": "test@example.com",
		"NameUpper": "DEMO", "Year": 2026,
		"Description": "A demo CLI", "description": "A demo CLI",
		"Org": "example", "org": "example",
		"DefaultBranch": "main",
	}

	_, err := runAugment(context.Background(), deps, in, cwd)
	require.NoError(t, err)
	return cwd
}

func TestBootstrap_CLIGo_AgentsFragment(t *testing.T) {
	t.Run("tier 3 ships it with the tool's own name", func(t *testing.T) {
		dir := augmentCLIGoAt(t, 3)

		body, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
		require.NoError(t, err, "tier 3 ships the kit root, so it must ship the fragment")
		got := string(body)

		// The fragment is only true for the tool it ships with, so
		// every tool-specific value must be substituted, not generic.
		assert.Contains(t, got, "# demo for agents")
		assert.Contains(t, got, "demo serve api --addr 127.0.0.1:8080")
		assert.Contains(t, got, "demo serve socket --socket /tmp/demo.sock")
		// Each selector blocks in the foreground, so the opener must
		// offer the both-surfaces form rather than listing two
		// selectors an agent would run in sequence and hang on.
		assert.Contains(t, got,
			"demo serve --addr 127.0.0.1:8080 --enable socket --socket /tmp/demo.sock")
		assert.Contains(t, got, "pick ONE per process")
		assert.Contains(t, got, "a bare `serve` gives you REST alone",
			"socket is registered but off, so the doc must not promise both")
		assert.NotContains(t, got, "{{", "no unrendered template action may survive")
		assert.NotContains(t, got, ".Name", "the variable must be substituted, not named")

		// The claims the serve test pins on the running binary. If a
		// reason leaves the projection's vocabulary, this goes red
		// beside the test that observes it.
		for _, reason := range []string{
			"not-runnable", "builtin", "hidden-internal", "deprecated",
			"malformed-schema", "self-hosting", "management-only",
			"interactive", "unauthorized-destructive", "withheld-by-config",
		} {
			assert.Contains(t, got, reason, "the refusal vocabulary must be complete")
		}
		for _, code := range []string{
			"NOT_FOUND", "NOT_ENABLED", "BLOCKED", "NOT_INVOCABLE",
			"DENIED", "UNAUTHENTICATED", "INVALID", "INTERNAL",
		} {
			assert.Contains(t, got, code, "every socket wire code must be documented")
		}
		assert.Contains(t, got, "/v1/commands", "discovery is the entry point")
		assert.Contains(t, got, "/openapi.json")
		assert.Contains(t, got, "confirm-token")
	})

	t.Run("tier 4 ships it too", func(t *testing.T) {
		dir := augmentCLIGoAt(t, 4)
		assert.FileExists(t, filepath.Join(dir, "AGENTS.md"))
	})

	for _, tier := range []int{1, 2} {
		t.Run("no fragment below the served root", func(t *testing.T) {
			dir := augmentCLIGoAt(t, tier)
			_, err := os.Stat(filepath.Join(dir, "AGENTS.md"))
			assert.True(t, os.IsNotExist(err),
				"tier %d ships no kit root, so it must ship no agent doc; stat err = %v",
				tier, err)
		})
	}
}

// TestBootstrap_CLIGo_AgentsFragmentDocumentsTheRealSurface keeps the
// fragment honest about the surface the template actually ships: it
// tells an agent to read `route` and `method` off discovery, so it
// must not hard-code a route for a command this template has.
func TestBootstrap_CLIGo_AgentsFragmentDocumentsTheRealSurface(t *testing.T) {
	dir := augmentCLIGoAt(t, 3)
	body, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	require.NoError(t, err)
	got := string(body)

	// GET for reads, POST otherwise — the method rule the projection
	// applies, stated as a rule rather than a per-command table.
	assert.Contains(t, got, "`read` is `GET`")
	assert.Regexp(t, `(?i)undeclared \*\*query\*\* parameter is silently dropped`, got,
		"the GET/POST asymmetry on unknown flags is the one trap worth naming")

	// The socket answers in an envelope; REST does not. An agent that
	// confuses them parses the wrong shape.
	assert.Contains(t, got, `{"ok":true,"result":`)
	assert.Contains(t, got, `{"exit_code":0,"data":`)

	// Exit codes are shared across transports; the HTTP column is the
	// projection's mapping.
	for _, row := range []string{"| 5 | 403 |", "| 6 | 503 |", "| 64 | 429 |", "| 65 | 422 |"} {
		assert.Contains(t, strings.ReplaceAll(got, "  ", " "), row)
	}
}
