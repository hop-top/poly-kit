package kitinit_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	kitinit "hop.top/kit/cmd/kit/init"
	tmpl "hop.top/kit/internal/template"
)

// fakeWizarder records every Ask invocation and returns canned answers
// keyed by var name. Ask falls back to "" when no answer is registered.
type fakeWizarder struct {
	answers map[string]string
	err     error
	calls   []wizardCall
}

type wizardCall struct {
	varName string
	prompt  string
	defVal  string
	choices []string
}

func (f *fakeWizarder) Ask(varName, prompt, defaultValue string, choices []string) (string, error) {
	f.calls = append(f.calls, wizardCall{varName, prompt, defaultValue, choices})
	if f.err != nil {
		return "", f.err
	}
	return f.answers[varName], nil
}

func ptrStr(s string) *string { return &s }
func ptrBool(b bool) *bool    { return &b }

// clearKitEnv strips any KIT_* env vars that could leak into a test from
// the parent shell. t.Setenv handles per-test cleanup.
func clearKitEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"KIT_AUTHOR", "KIT_EMAIL", "KIT_LICENSE", "KIT_ACCOUNT_TYPE",
		"KIT_ORG", "KIT_VISIBILITY", "KIT_THEME", "KIT_TEMPLATE",
		"KIT_DESCRIPTION", "KIT_DEFAULT_BRANCH", "KIT_MODULE", "KIT_NAME",
	} {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
}

// isolateGit points git at a temp HOME so global gitconfig probes by
// inputs.gitConfig return controlled values (or empty when unset).
func isolateGit(t *testing.T, name, email string) {
	t.Helper()
	dir := t.TempDir()
	cfg := filepath.Join(dir, ".gitconfig")
	t.Setenv("HOME", dir)
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	if name != "" {
		require.NoError(t, exec.Command("git", "config", "--global", "user.name", name).Run())
	}
	if email != "" {
		require.NoError(t, exec.Command("git", "config", "--global", "user.email", email).Run())
	}
}

func TestGather_FlagWinsOverEnv(t *testing.T) {
	clearKitEnv(t)
	isolateGit(t, "", "")
	t.Setenv("KIT_AUTHOR", "env")
	flags := &kitinit.FlagSet{Author: ptrStr("flag")}
	in, err := kitinit.Gather(context.Background(), nil, flags, tmpl.Manifest{}, kitinit.Defaults{}, nil)
	require.NoError(t, err)
	assert.Equal(t, "flag", in.Author)
}

func TestGather_EnvWinsOverDefaults(t *testing.T) {
	clearKitEnv(t)
	isolateGit(t, "", "")
	t.Setenv("KIT_AUTHOR", "env")
	defs := kitinit.Defaults{Author: "def"}
	in, err := kitinit.Gather(context.Background(), nil, nil, tmpl.Manifest{}, defs, nil)
	require.NoError(t, err)
	assert.Equal(t, "env", in.Author)
}

func TestGather_DefaultsWinsOverManifest(t *testing.T) {
	clearKitEnv(t)
	isolateGit(t, "", "")
	defs := kitinit.Defaults{Author: "def"}
	m := tmpl.Manifest{Variables: []tmpl.Variable{
		{Name: "author", Default: "manifest-default"},
	}}
	in, err := kitinit.Gather(context.Background(), nil, nil, m, defs, nil)
	require.NoError(t, err)
	assert.Equal(t, "def", in.Author)
	assert.Equal(t, "def", in.Vars["author"])
}

func TestGather_ManifestDefaultRenders(t *testing.T) {
	clearKitEnv(t)
	isolateGit(t, "", "")
	flags := &kitinit.FlagSet{
		Name:   ptrStr("widget"),
		Author: ptrStr("alice"),
	}
	m := tmpl.Manifest{Variables: []tmpl.Variable{
		{Name: "module", Default: "github.com/{{.author}}/{{.name}}"},
	}}
	in, err := kitinit.Gather(context.Background(), nil, flags, m, kitinit.Defaults{}, nil)
	require.NoError(t, err)
	assert.Equal(t, "github.com/alice/widget", in.Vars["module"])
}

func TestGather_BuiltinAuthorFromGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	clearKitEnv(t)
	isolateGit(t, "Jane", "jane@example.com")
	in, err := kitinit.Gather(context.Background(), nil, nil, tmpl.Manifest{}, kitinit.Defaults{}, nil)
	require.NoError(t, err)
	assert.Equal(t, "Jane", in.Author)
	assert.Equal(t, "jane@example.com", in.Email)
}

func TestGather_WizardCalledOnMissing(t *testing.T) {
	clearKitEnv(t)
	isolateGit(t, "", "")
	m := tmpl.Manifest{Variables: []tmpl.Variable{
		{Name: "color", Required: true, Prompt: "Pick a color"},
	}}
	wiz := &fakeWizarder{answers: map[string]string{"color": "blue"}}
	in, err := kitinit.Gather(context.Background(), nil, nil, m, kitinit.Defaults{}, wiz)
	require.NoError(t, err)
	require.Len(t, wiz.calls, 1)
	assert.Equal(t, "color", wiz.calls[0].varName)
	assert.Equal(t, "Pick a color", wiz.calls[0].prompt)
	assert.Equal(t, "blue", in.Vars["color"])
}

func TestGather_YesSkipsWizard(t *testing.T) {
	clearKitEnv(t)
	isolateGit(t, "", "")
	m := tmpl.Manifest{Variables: []tmpl.Variable{
		{Name: "color", Required: true},
	}}
	wiz := &fakeWizarder{}
	flags := &kitinit.FlagSet{Yes: ptrBool(true)}
	_, err := kitinit.Gather(context.Background(), nil, flags, m, kitinit.Defaults{}, wiz)
	require.Error(t, err)
	assert.True(t, errors.Is(err, kitinit.ErrMissingRequired),
		"expected ErrMissingRequired, got: %v", err)
	assert.Empty(t, wiz.calls, "wizard must not be called under --yes")
}

func TestGather_OrgRequiredWhenOrgAccount(t *testing.T) {
	clearKitEnv(t)
	isolateGit(t, "", "")
	flags := &kitinit.FlagSet{AccountType: ptrStr("org")}
	_, err := kitinit.Gather(context.Background(), nil, flags, tmpl.Manifest{}, kitinit.Defaults{}, nil)
	require.Error(t, err)
	assert.True(t, errors.Is(err, kitinit.ErrOrgRequired),
		"expected ErrOrgRequired, got: %v", err)
}

func TestGather_VisibilityDefaultPerAccountType(t *testing.T) {
	t.Run("personal→private", func(t *testing.T) {
		clearKitEnv(t)
		isolateGit(t, "", "")
		flags := &kitinit.FlagSet{AccountType: ptrStr("personal")}
		in, err := kitinit.Gather(context.Background(), nil, flags, tmpl.Manifest{}, kitinit.Defaults{}, nil)
		require.NoError(t, err)
		assert.Equal(t, "private", in.Visibility)
	})
	t.Run("org→private", func(t *testing.T) {
		clearKitEnv(t)
		isolateGit(t, "", "")
		flags := &kitinit.FlagSet{
			AccountType: ptrStr("org"),
			Org:         ptrStr("acme"),
		}
		in, err := kitinit.Gather(context.Background(), nil, flags, tmpl.Manifest{}, kitinit.Defaults{}, nil)
		require.NoError(t, err)
		assert.Equal(t, "private", in.Visibility)
	})
}

func TestGather_HopFlagOverridesDefaultsFalse(t *testing.T) {
	clearKitEnv(t)
	isolateGit(t, "", "")
	defs := kitinit.Defaults{Hop: ptrBool(false)}
	flags := &kitinit.FlagSet{Hop: ptrBool(true)}
	in, err := kitinit.Gather(context.Background(), nil, flags, tmpl.Manifest{}, defs, nil)
	require.NoError(t, err)
	assert.True(t, in.Hop, "flag=true must override defaults.Hop=false")
}

func TestGather_HopDefaultsTrueWhenAllUnset(t *testing.T) {
	clearKitEnv(t)
	isolateGit(t, "", "")
	in, err := kitinit.Gather(context.Background(), nil, nil, tmpl.Manifest{}, kitinit.Defaults{}, nil)
	require.NoError(t, err)
	assert.True(t, in.Hop, "Hop must default to true when nothing set")
}

func TestGather_HopDefaultsFalseHonoured(t *testing.T) {
	clearKitEnv(t)
	isolateGit(t, "", "")
	defs := kitinit.Defaults{Hop: ptrBool(false)}
	in, err := kitinit.Gather(context.Background(), nil, nil, tmpl.Manifest{}, defs, nil)
	require.NoError(t, err)
	assert.False(t, in.Hop, "explicit defaults.Hop=false must be honored")
}

// TestGather_NameFallsBackToBasename asserts that when no positional arg,
// no --name flag, and no KIT_NAME env are set, Gather populates Name (and
// vars["Name"]) from basename(cwd). This is the fix for T-0411 problem A:
// before this, the cli-go manifest's required-Name check would fire under
// --yes and block augment-mode runs that omitted a positional name.
func TestGather_NameFallsBackToBasename(t *testing.T) {
	clearKitEnv(t)
	isolateGit(t, "", "")

	// Chdir into a basename that satisfies the cli-go name regex
	// (^[a-z][a-z0-9-]{0,63}$) so the fallback behaves like a real run.
	parent := t.TempDir()
	work := filepath.Join(parent, "mas")
	require.NoError(t, os.Mkdir(work, 0o750))
	t.Chdir(work)

	m := tmpl.Manifest{Variables: []tmpl.Variable{
		{Name: "Name", Required: true, Validate: "^[a-z][a-z0-9-]{0,63}$"},
	}}
	flags := &kitinit.FlagSet{Yes: ptrBool(true)}

	in, err := kitinit.Gather(context.Background(), nil, flags, m, kitinit.Defaults{}, nil)
	require.NoError(t, err, "augment-mode --yes with no positional name must not fail required-var gather")
	assert.Equal(t, "mas", in.Name)
	assert.Equal(t, "mas", in.Vars["Name"])
	assert.Equal(t, "mas", in.Vars["name"])
}

// TestGather_NameFromKitNameEnv asserts KIT_NAME env populates both the
// scalar in.Name and vars["Name"], so the orchestrator-facing summary and
// the rendered template share a single value (T-0411 problem B). Before
// the fix, env-supplied KIT_NAME landed in vars["Name"] only, while
// in.Name was overwritten by basename(cwd) inside runAugment — leaving
// the human/JSON summary mis-reporting "name: <basename>".
func TestGather_NameFromKitNameEnv(t *testing.T) {
	clearKitEnv(t)
	isolateGit(t, "", "")

	parent := t.TempDir()
	work := filepath.Join(parent, "main") // basename ≠ env value
	require.NoError(t, os.Mkdir(work, 0o750))
	t.Chdir(work)

	t.Setenv("KIT_NAME", "mas")
	flags := &kitinit.FlagSet{Yes: ptrBool(true)}

	in, err := kitinit.Gather(context.Background(), nil, flags, tmpl.Manifest{}, kitinit.Defaults{}, nil)
	require.NoError(t, err)
	assert.Equal(t, "mas", in.Name, "in.Name must equal KIT_NAME, not basename(cwd)")
	assert.Equal(t, "mas", in.Vars["Name"], "vars[\"Name\"] must equal KIT_NAME")
	assert.Equal(t, "mas", in.Vars["name"], "vars[\"name\"] must equal KIT_NAME")
}

// TestGather_PositionalArgWinsOverEnvAndBasename pins down the precedence
// chain: positional name beats KIT_NAME and basename(cwd). Guards against
// regressions where the new fallback layers accidentally take priority.
func TestGather_PositionalArgWinsOverEnvAndBasename(t *testing.T) {
	clearKitEnv(t)
	isolateGit(t, "", "")

	parent := t.TempDir()
	work := filepath.Join(parent, "frombase")
	require.NoError(t, os.Mkdir(work, 0o750))
	t.Chdir(work)

	t.Setenv("KIT_NAME", "fromenv")

	in, err := kitinit.Gather(
		context.Background(),
		[]string{"frompositional"},
		nil,
		tmpl.Manifest{},
		kitinit.Defaults{},
		nil,
	)
	require.NoError(t, err)
	assert.Equal(t, "frompositional", in.Name)
	assert.Equal(t, "frompositional", in.Vars["Name"])
}
