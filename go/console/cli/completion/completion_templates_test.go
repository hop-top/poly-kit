package completion_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// The Rust SDK writes cobra's completion scripts from templates: the
// scripts of the cobra version in go.mod, rendered for a sentinel
// program name whose derived forms are then replaced by tokens. This
// file is that derivation. TestCompletionTemplatesInSync fails when the
// committed templates drift from it; `-update` (make completion-regen)
// rewrites them.

const rsTemplateDir = "../../../../sdk/experimental/rs/src/cli/completion/scripts"

// templateSentinel holds a '-' so its identifier form differs from it.
const templateSentinel = "zz-kit-zz"

var templateShells = []string{"bash", "zsh", "fish", "powershell"}

// cobraScript is what cobra itself writes for a program called name.
func cobraScript(t *testing.T, shell, name string, desc bool) string {
	t.Helper()
	cmd := &cobra.Command{Use: name}
	var b bytes.Buffer
	var err error
	switch shell {
	case "bash":
		err = cmd.GenBashCompletionV2(&b, desc)
	case "zsh":
		if desc {
			err = cmd.GenZshCompletion(&b)
		} else {
			err = cmd.GenZshCompletionNoDesc(&b)
		}
	case "fish":
		err = cmd.GenFishCompletion(&b, desc)
	case "powershell":
		if desc {
			err = cmd.GenPowerShellCompletionWithDesc(&b)
		} else {
			err = cmd.GenPowerShellCompletion(&b)
		}
	default:
		t.Fatalf("unknown shell %q", shell)
	}
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}

var envVarSubst = regexp.MustCompile(`[^A-Z0-9_]`)

// activeHelpVar is cobra's <PROGRAM>_ACTIVE_HELP variable name.
func activeHelpVar(name string) string {
	return envVarSubst.ReplaceAllString(strings.ToUpper(name+"_ACTIVE_HELP"), "_")
}

func nameVar(name string) string {
	return strings.NewReplacer("-", "_", ":", "_").Replace(name)
}

// deriveTemplate tokenises cobra's no-description script for the
// sentinel. `__completeNoDesc` is the one unambiguous request-command
// spelling, so the no-description script is the source.
func deriveTemplate(t *testing.T, shell string) string {
	t.Helper()
	src := cobraScript(t, shell, templateSentinel, false)
	if strings.Contains(src, "@@") {
		t.Fatalf("%s: cobra's script already holds @@", shell)
	}
	out := strings.NewReplacer(
		fmt.Sprintf("%-36s", templateSentinel), "@@NAME_PAD36@@",
		activeHelpVar(templateSentinel), "@@ACTIVE_HELP_VAR@@",
		cobra.ShellCompNoDescRequestCmd, "@@COMP_CMD@@",
		nameVar(templateSentinel), "@@NAME_VAR@@",
		templateSentinel, "@@NAME@@",
	).Replace(src)
	if strings.Contains(strings.ToLower(out), "kit_zz") || strings.Contains(strings.ToLower(out), "kit-zz") {
		t.Fatalf("%s: a sentinel form survived tokenising", shell)
	}
	return out
}

// renderTemplate mirrors the Rust renderer.
func renderTemplate(tmpl, name string, desc bool) string {
	cmd := cobra.ShellCompNoDescRequestCmd
	if desc {
		cmd = cobra.ShellCompRequestCmd
	}
	return strings.NewReplacer(
		"@@NAME_PAD36@@", fmt.Sprintf("%-36s", name),
		"@@NAME_VAR@@", nameVar(name),
		"@@ACTIVE_HELP_VAR@@", activeHelpVar(name),
		"@@COMP_CMD@@", cmd,
		"@@NAME@@", name,
	).Replace(tmpl)
}

func TestCompletionTemplatesInSync(t *testing.T) {
	for _, shell := range templateShells {
		t.Run(shell, func(t *testing.T) {
			tmpl := deriveTemplate(t, shell)
			// The derivation is only trusted if it reproduces cobra for
			// names it was not derived from.
			for _, name := range []string{"kitapp", "kit-app", "my-tool.x", "a:b-c", "Kit"} {
				for _, desc := range []bool{true, false} {
					if got, want := renderTemplate(tmpl, name, desc), cobraScript(t, shell, name, desc); got != want {
						t.Fatalf("template does not reproduce cobra for %q desc=%v%s", name, desc, firstDiff(got, want))
					}
				}
			}
			path := filepath.Join(filepath.FromSlash(rsTemplateDir), shell+".tmpl")
			if *updateGoldens {
				if err := os.WriteFile(path, []byte(tmpl), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			committed, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(committed) != tmpl {
				t.Errorf("%s drifted from cobra's script%s; run make completion-regen",
					path, firstDiff(string(committed), tmpl))
			}
		})
	}
}
