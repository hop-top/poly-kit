package main

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Frontmatter holds the SKILL.md keys channel metadata is derived from.
type Frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	License     string `yaml:"license"`
	Metadata    struct {
		Version    string `yaml:"version"`
		MinVersion string `yaml:"min_version"`
	} `yaml:"metadata"`
}

// splitFrontmatter returns the YAML text between the opening and closing ---
// lines of a SKILL.md.
func splitFrontmatter(src []byte) (string, error) {
	lines := strings.Split(string(src), "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], "\r") != "---" {
		return "", fmt.Errorf("no frontmatter: the first line must be ---")
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\r") == "---" {
			return strings.Join(lines[1:i], "\n"), nil
		}
	}
	return "", fmt.Errorf("frontmatter is not closed by a --- line")
}

// parseFrontmatter reads the YAML block at the top of a SKILL.md.
func parseFrontmatter(src []byte) (Frontmatter, error) {
	var fm Frontmatter
	text, err := splitFrontmatter(src)
	if err != nil {
		return fm, err
	}
	if err := yaml.Unmarshal([]byte(text), &fm); err != nil {
		return fm, fmt.Errorf("frontmatter: %w", err)
	}
	fm.Description = strings.TrimSpace(fm.Description)
	return fm, nil
}

// frontmatterProblem is the one check that a SKILL.md names its skill and
// carries a description and a license. The generator and the lint both call
// it. want is the skill's name; it returns "" when the frontmatter is fine.
func frontmatterProblem(want string, fm Frontmatter) string {
	switch {
	case fm.Name != want:
		return fmt.Sprintf("frontmatter name %q must equal the skill name %q", fm.Name, want)
	case fm.Description == "":
		return "frontmatter has no description"
	case fm.License == "":
		return "frontmatter has no license"
	}
	return ""
}

func beginMarker(id string) string { return "<!-- skills-sync:begin " + id + " -->" }
func endMarker(id string) string   { return "<!-- skills-sync:end " + id + " -->" }

// replaceBlock returns src with the lines between the begin and end markers
// of block id replaced by body. Each marker must appear on a line of its
// own, exactly once, begin before end.
func replaceBlock(src []byte, id, body string) ([]byte, error) {
	begin, end := []byte(beginMarker(id)), []byte(endMarker(id))
	lines := bytes.SplitAfter(src, []byte("\n"))
	b, e := -1, -1
	for i, l := range lines {
		t := bytes.TrimRight(l, "\r\n")
		switch {
		case bytes.Equal(t, begin):
			if b >= 0 {
				return nil, fmt.Errorf("%s appears twice", begin)
			}
			b = i
		case bytes.Equal(t, end):
			if e >= 0 {
				return nil, fmt.Errorf("%s appears twice", end)
			}
			e = i
		}
	}
	if b < 0 {
		return nil, fmt.Errorf("missing marker line %s", begin)
	}
	if e < 0 {
		return nil, fmt.Errorf("missing marker line %s", end)
	}
	if e < b {
		return nil, fmt.Errorf("%s comes before %s", end, begin)
	}
	var out bytes.Buffer
	for _, l := range lines[:b+1] {
		out.Write(l)
	}
	out.WriteString(strings.TrimRight(body, "\n") + "\n")
	for _, l := range lines[e:] {
		out.Write(l)
	}
	return out.Bytes(), nil
}

// minVersionLine is the frontmatter line holding the minimum release, written
// by sync from ports.yaml and never by release tooling.
func minVersionLine(indent, min string) string {
	return indent + "min_version: " + strconv.Quote(min)
}

var metaKeyRe = regexp.MustCompile(`^(\s+)(version|min_version):`)

// setMinVersion returns src with metadata.min_version set to min: the line is
// replaced when present, else inserted right after metadata.version.
func setMinVersion(src []byte, min string) ([]byte, error) {
	lines := strings.SplitAfter(string(src), "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], "\r\n") != "---" {
		return nil, fmt.Errorf("no frontmatter: the first line must be ---")
	}
	verAt, minAt, indent, inMeta := -1, -1, "", false
	for i := 1; i < len(lines); i++ {
		t := strings.TrimRight(lines[i], "\r\n")
		if t == "---" {
			break
		}
		if strings.HasPrefix(t, "metadata:") {
			inMeta = true
			continue
		}
		if inMeta && t != "" && !strings.HasPrefix(t, " ") && !strings.HasPrefix(t, "\t") {
			inMeta = false
		}
		if !inMeta {
			continue
		}
		if m := metaKeyRe.FindStringSubmatch(t); m != nil {
			indent = m[1]
			if m[2] == "version" {
				verAt = i
			} else {
				minAt = i
			}
		}
	}
	nl := "\n"
	switch {
	case minAt >= 0:
		lines[minAt] = minVersionLine(indent, min) + nl
	case verAt >= 0:
		lines = append(lines[:verAt+1], append([]string{minVersionLine(indent, min) + nl}, lines[verAt+1:]...)...)
	default:
		return nil, fmt.Errorf("frontmatter has no metadata.version to put metadata.min_version after")
	}
	return []byte(strings.Join(lines, "")), nil
}
