// Package tldr extracts ToolSpec workflows from tldr-pages markdown.
package tldr

import (
	"strings"

	"hop.top/kit/go/ai/toolspec"
)

// ParseTldrPage parses a tldr-pages markdown document and returns a
// ToolSpec with Workflows populated from the example blocks.
//
// tldr format:
//
//	# tool
//	> Description line(s)
//	- Step name
//	`command`
func ParseTldrPage(name, markdown string) *toolspec.ToolSpec {
	spec := &toolspec.ToolSpec{Name: name}

	lines := strings.Split(markdown, "\n")
	var stepName string

	for _, raw := range lines {
		line := strings.TrimRight(raw, " \t\r")

		switch {
		case strings.HasPrefix(line, "- "):
			stepName = strings.TrimPrefix(line, "- ")
			// Remove trailing colon common in tldr pages.
			stepName = strings.TrimSuffix(stepName, ":")

		case strings.HasPrefix(line, "`") && strings.HasSuffix(line, "`"):
			cmd := strings.Trim(line, "`")
			if stepName == "" {
				stepName = cmd
			}
			spec.Workflows = append(spec.Workflows, toolspec.Workflow{
				Name:  stepName,
				Steps: []string{cmd},
			})
			stepName = ""
		}
	}

	return spec
}
