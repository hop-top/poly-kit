// Package kitinit — output.go renders the post-run summary of a `kit
// init` invocation in two flavors: a human-readable section layout for
// terminals and a structured JSON form for tooling. NextSteps generates
// the per-mode follow-up checklist surfaced at the tail of both.
package kitinit

import (
	"encoding/json"
	"fmt"
	"io"

	"hop.top/kit/cmd/kit/init/buswf"
	"hop.top/kit/internal/template"
)

// maxFilesShown bounds the Files-written list in WriteHuman; longer
// lists collapse to the first N entries followed by an ellipsis.
const maxFilesShown = 10

// Summary captures everything renderers need from a completed init run.
//
// HopSkipped reports that --hop was requested but the git-hop binary
// was not on PATH, so the local repo scaffolding step was skipped.
// TLCSkipped reports that the `tlc init` post-step was skipped because
// tlc was not on PATH. Both flags are best-effort signals; downstream
// tooling can use them to decide whether to nudge the user to install
// the missing dependencies.
type Summary struct {
	Mode       string          `json:"mode"`
	Name       string          `json:"name"`
	Target     string          `json:"target"`
	Template   string          `json:"template"`
	Result     template.Result `json:"result"`
	GitHub     *GitHubSummary  `json:"github,omitempty"`
	HopSkipped bool            `json:"hop_skipped,omitempty"`
	TLCSkipped bool            `json:"tlc_skipped,omitempty"`

	PrePrHook    *PrePrResult      `json:"prepr_hook,omitempty"`
	Workflows    []WorkflowAction  `json:"workflows,omitempty"`
	BusWorkflows []buswf.PlanEntry `json:"bus_workflows,omitempty"`

	NextSteps []string `json:"next_steps"`
}

// GitHubSummary is the JSON-friendly subset of github.RepoInfo embedded
// in Summary when the run created (or would create) a remote repo.
type GitHubSummary struct {
	Repo       string `json:"repo"`
	URL        string `json:"url"`
	Visibility string `json:"visibility"`
}

// WriteHuman renders s as a sectioned, terminal-friendly summary.
func WriteHuman(w io.Writer, s Summary) error {
	if _, err := fmt.Fprintf(w, "Created %s at %s from %s\n",
		s.Name, s.Target, s.Template); err != nil {
		return err
	}

	if len(s.Result.Written) > 0 {
		if _, err := fmt.Fprintln(w, "\nFiles written:"); err != nil {
			return err
		}
		shown := s.Result.Written
		if len(shown) > maxFilesShown {
			shown = shown[:maxFilesShown]
		}
		for _, p := range shown {
			if _, err := fmt.Fprintf(w, "  %s\n", p); err != nil {
				return err
			}
		}
		if len(s.Result.Written) > maxFilesShown {
			if _, err := fmt.Fprintf(w, "  ... (%d more)\n",
				len(s.Result.Written)-maxFilesShown); err != nil {
				return err
			}
		}
	}

	if len(s.Result.Suggested) > 0 {
		if _, err := fmt.Fprintln(w, "\nSuggested files:"); err != nil {
			return err
		}
		for _, p := range s.Result.Suggested {
			if _, err := fmt.Fprintf(w, "  %s\n", p); err != nil {
				return err
			}
		}
	}

	if len(s.Result.Skipped) > 0 || len(s.Result.Conditional) > 0 {
		if _, err := fmt.Fprintf(w, "\nSkipped: %d  Conditional: %d\n",
			len(s.Result.Skipped), len(s.Result.Conditional)); err != nil {
			return err
		}
	}

	if s.GitHub != nil && s.GitHub.URL != "" {
		if _, err := fmt.Fprintf(w, "\nGitHub: %s\n", s.GitHub.URL); err != nil {
			return err
		}
	}

	if s.PrePrHook != nil && len(s.PrePrHook.Files) > 0 {
		if _, err := fmt.Fprintln(w, "\nBefore-PR hook:"); err != nil {
			return err
		}
		for _, f := range s.PrePrHook.Files {
			line := fmt.Sprintf("  %s [%s]", f.Path, f.Action)
			if f.SuggestedPath != "" {
				line += " → " + f.SuggestedPath
			}
			if _, err := fmt.Fprintln(w, line); err != nil {
				return err
			}
		}
	}

	if len(s.Workflows) > 0 {
		if _, err := fmt.Fprintln(w, "\nGitHub workflows:"); err != nil {
			return err
		}
		for _, a := range s.Workflows {
			line := fmt.Sprintf("  %s [%s]", a.Path, a.Action)
			if a.SuggestedPath != "" {
				line += " → " + a.SuggestedPath
			}
			if _, err := fmt.Fprintln(w, line); err != nil {
				return err
			}
		}
	}

	if len(s.BusWorkflows) > 0 {
		if _, err := fmt.Fprintln(w, "\nKit bus workflows:"); err != nil {
			return err
		}
		for _, e := range s.BusWorkflows {
			line := fmt.Sprintf("  %-15s %s", e.Action, e.Path)
			if e.SuggestedPath != "" {
				line += " → " + e.SuggestedPath
			}
			if e.Reason != "" {
				line += fmt.Sprintf(" (%s)", e.Reason)
			}
			if _, err := fmt.Fprintln(w, line); err != nil {
				return err
			}
		}
	}

	if len(s.NextSteps) > 0 {
		if _, err := fmt.Fprintln(w, "\nNext steps:"); err != nil {
			return err
		}
		for i, step := range s.NextSteps {
			if _, err := fmt.Fprintf(w, "  %d. %s\n", i+1, step); err != nil {
				return err
			}
		}
	}

	return nil
}

// WriteJSON encodes s as indented JSON to w.
func WriteJSON(w io.Writer, s Summary) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(s)
}

// NextSteps returns the follow-up checklist for the given mode. Modes
// outside {bootstrap, augment} return nil — callers append GitHub-aware
// steps separately.
func NextSteps(mode, name string, github *GitHubSummary) []string {
	switch mode {
	case "bootstrap":
		return []string{
			fmt.Sprintf("cd %s", name),
			"make build",
			fmt.Sprintf("./bin/%s --help", name),
		}
	case "augment":
		return []string{
			"review .kit-suggested.* files",
			"make build",
			"make test",
		}
	default:
		return nil
	}
}
