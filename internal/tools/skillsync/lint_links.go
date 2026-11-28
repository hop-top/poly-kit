package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var urlRe = regexp.MustCompile("https?://[^\\s<>\"'`\\])]+")

// ruleS4URLs checks every URL that points into the organisation's
// repositories: it is the canonical URL of a skill or a docs page whose path
// exists, on the one branch ports.yaml names.
func (l *lintSkill) ruleS4URLs() {
	for _, f := range l.textFiles() {
		if !f.isMarkdown() {
			continue
		}
		for i, text := range strings.Split(string(f.Data), "\n") {
			for _, u := range urlRe.FindAllString(text, -1) {
				u = strings.TrimRight(u, ".,;:!?*")
				if msg := l.g.badURL(u); msg != "" {
					l.add(f.Rel, i+1, "S4", "%s: %s", u, msg)
				}
			}
		}
	}
}

// orgs are the first path segments of the documentation repository and of
// every mirror.
func (g *gen) orgs() map[string]bool {
	out := map[string]bool{firstSegment(g.ports.Docs.Repo): true}
	for _, s := range g.skills {
		out[firstSegment(s.Comp.Mirror)] = true
	}
	return out
}

func firstSegment(p string) string {
	s, _, _ := strings.Cut(p, "/")
	return s
}

// badURL explains why u is not an allowed link, or returns "".
func (g *gen) badURL(u string) string {
	pre := g.ports.Host + "/"
	if !strings.HasPrefix(u, pre) {
		return ""
	}
	rest := u[len(pre):]
	if !g.orgs()[firstSegment(rest)] {
		return ""
	}
	if i := strings.IndexAny(rest, "#?"); i >= 0 {
		rest = rest[:i]
	}
	repo, tail, ok := strings.Cut(rest, "/blob/")
	if !ok {
		return "only a skill's canonical URL or a docs page may be linked, not a repository or tree URL"
	}
	branch, p, _ := strings.Cut(tail, "/")
	isDocs := repo == g.ports.Docs.Repo
	isMirror := false
	for _, s := range g.skills {
		if repo != s.Comp.Mirror {
			continue
		}
		isMirror = true
		if branch == g.ports.MirrorBranch && (p == "skills/"+s.Spec.Skill+"/SKILL.md" || g.generatedLink(u)) {
			return ""
		}
	}
	if isDocs && branch == g.ports.Docs.Branch {
		if _, err := os.Stat(filepath.Join(g.o.Root, filepath.FromSlash(strings.TrimRight(p, "/")))); err != nil {
			return fmt.Sprintf("docs path %q does not exist in this repository", p)
		}
		return ""
	}
	switch {
	case isDocs && !isMirror && branch != g.ports.Docs.Branch:
		return fmt.Sprintf("docs links use branch %q, not %q", g.ports.Docs.Branch, branch)
	case isMirror && !isDocs && branch != g.ports.MirrorBranch:
		return fmt.Sprintf("skill links use branch %q, not %q", g.ports.MirrorBranch, branch)
	}
	return "neither the canonical URL of a skill nor a docs page"
}

// generatedLink reports whether u is a link the generator itself writes
// into skills besides skill URLs and docs URLs.
func (g *gen) generatedLink(u string) bool { return u == g.matrixURL() }

// areaTitles maps the reference file slug a port skill may carry to the
// parity page section it describes.
var areaTitles = []struct{ Slug, Title string }{
	{"cli-and-console", "CLI and console"},
	{"config-storage-secrets", "Config, storage and secrets"},
	{"serving-and-transports", "Serving and transports"},
	{"events-jobs-runtime", "Events, jobs and runtime"},
	{"guardrails-and-trust", "Guardrails and trust"},
	{"ai-and-agents", "AI and agents"},
	{"conformance-and-integrations", "Conformance and integrations"},
}

// ruleS5 allows a port skill an area reference only when the port has at
// least one done, partial or unverified capability in that section.
func (l *lintSkill) ruleS5() {
	if l.s.Router || l.s.Col < 0 {
		return
	}
	for _, a := range areaTitles {
		rel := "references/" + a.Slug + ".md"
		if l.byRel[rel] == nil {
			continue
		}
		found, covered := false, false
		for _, sec := range l.g.par.Sections {
			if sec.Title != a.Title {
				continue
			}
			found = true
			for _, c := range sec.Caps {
				if st := c.Status[l.s.Col]; st == stDone || st == stPartial || st == stUnverified {
					covered = true
				}
			}
		}
		switch {
		case !found:
			l.add(rel, 1, "S5", "the parity page has no section %q", a.Title)
		case !covered:
			l.add(rel, 1, "S5", "%s has no done, partial or unverified capability under %q; remove this reference", l.s.Spec.Language, a.Title)
		}
	}
}
