package main

import (
	"fmt"
	"strings"
)

// versionWidth is the column the version block wraps at.
const versionWidth = 100

// writtenForLine and expectsLine are the only generated lines that carry a
// version number. The first is annotated so release tooling bumps it with
// the metadata.version line it copies; the second is the minimum release,
// which no release tooling writes.
func writtenForLine(version string) string {
	return fmt.Sprintf("Written for %s. %s", version, versionAnnotation)
}

func expectsLine(min string) string { return fmt.Sprintf("Expects %s or newer.", min) }

// versionLines are the generated lines that may carry a version number.
func versionLines(version, min string) []string {
	return []string{writtenForLine(version), expectsLine(min)}
}

// wrapMD fills text to width columns: first prefixes the first line, rest every
// later one. A backtick span is never split.
func wrapMD(text, first, rest string, width int) string {
	var words []string
	var cur strings.Builder
	in := false
	for _, r := range text {
		switch {
		case r == '`':
			in = !in
			cur.WriteRune(r)
		case r == ' ' && !in:
			if cur.Len() > 0 {
				words = append(words, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		words = append(words, cur.String())
	}
	var b strings.Builder
	line := first
	empty := true
	for _, w := range words {
		if !empty && len(line)+1+len(w) > width {
			b.WriteString(line + "\n")
			line, empty = rest, true
		}
		if !empty {
			line += " "
		}
		line += w
		empty = false
	}
	b.WriteString(line + "\n")
	return b.String()
}

func codeList(cmds []string) string {
	q := make([]string, len(cmds))
	for i, c := range cmds {
		q[i] = "`" + c + "`"
	}
	return strings.Join(q, " or ")
}

// versionBlock is the body of the `version` managed block of one skill. It is
// the same text in every skill but for the commands ports.yaml gives each
// port, and the router's, which has no package to read.
func (g *gen) versionBlock(s *skill) (string, error) {
	ver := s.FM.Metadata.Version
	if ver == "" {
		return "", fmt.Errorf("%s: frontmatter has no metadata.version for the version block", s.rel("SKILL.md"))
	}
	bin := g.ports.Binary
	var b strings.Builder
	b.WriteString("## Versions\n\n")
	b.WriteString(writtenForLine(ver) + "\n")
	b.WriteString(expectsLine(s.Spec.MinVersion) + "\n\n")
	binary := fmt.Sprintf("The `%[1]s` binary: `%[1]s --version` prints `%[1]s v<version>` (a source build prints `%[1]s vdev`: nothing to compare); older than expected: upgrade it (`%[2]s`; [other routes](%[3]s)); newer than written for: update this skill.",
		bin.Command, bin.Upgrade, g.docsURL(bin.Readme))
	var parts []string
	if s.Router {
		parts = append(parts, binary, "Port skills carry their own Versions block; update them the same way.")
	} else {
		note := ""
		if s.Spec.InstalledNote != "" {
			note = " " + strings.TrimRight(s.Spec.InstalledNote, ".") + "."
		}
		parts = append(parts, fmt.Sprintf("Installed: %s;%s `scripts/detect-port.sh` shows what the manifest pins. Older than expected: tell the user, upgrade (`%s`). Newer than written for: this copy may be stale; update it, then re-read it.",
			codeList(s.Spec.Installed), note, s.Spec.Upgrade))
		if s.Spec.Component == g.ports.Router.Component {
			parts = append(parts, binary)
		}
	}
	m := g.ports.Marketplace
	parts = append(parts, fmt.Sprintf("Update by install channel, then restart the harness: Claude Code plugin `claude plugin marketplace update %[1]s`, then `claude plugin update %[3]s@%[1]s` (marketplace repository `%[2]s`); gym, `npx skills` or by hand: take `skills/%[3]s` again from `%[4]s` at `%[5]s`.",
		m.Name, m.Repo, s.Spec.Skill, s.Comp.Mirror, g.ports.MirrorBranch))
	b.WriteString(wrapMD(strings.Join(parts, " "), "", "", versionWidth))
	return b.String(), nil
}
