package pkl

import (
	"fmt"
	"os"
	"strings"
)

// LoadSchema parses a PKL source file and evaluates it to extract
// config schema metadata.
func LoadSchema(path string) (*Schema, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("pkl: read %s: %w", path, err)
	}
	return parseSource(string(data))
}

func parseSource(source string) (*Schema, error) {
	s := &Schema{}

	if m := reModule.FindStringSubmatch(source); m != nil {
		s.ModuleName = m[1]
	}

	// Collect nested class bodies keyed by name.
	classes := parseClasses(source)

	// Walk top-level lines and collect fields.
	lines := strings.Split(source, "\n")
	var comments []string
	inClass := false
	braceDepth := 0

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Track class blocks to skip them at top level.
		if reClassDecl.MatchString(trimmed) {
			inClass = true
			braceDepth = strings.Count(trimmed, "{") - strings.Count(trimmed, "}")
			comments = nil
			continue
		}
		if inClass {
			braceDepth += strings.Count(trimmed, "{") - strings.Count(trimmed, "}")
			if braceDepth <= 0 {
				inClass = false
			}
			continue
		}

		// Accumulate doc comments.
		if strings.HasPrefix(trimmed, "///") {
			comments = append(comments, trimmed)
			continue
		}

		// Skip module line, blank, or non-field lines.
		if trimmed == "" || strings.HasPrefix(trimmed, "module ") ||
			strings.HasPrefix(trimmed, "import ") ||
			strings.HasPrefix(trimmed, "//") {
			comments = nil
			continue
		}

		fd := parseField(trimmed, comments)
		comments = nil
		if fd == nil {
			continue
		}

		// If the type is a class reference, expand nested fields.
		if body, ok := classes[typeName(fd)]; ok {
			nested := parseNestedClass(body, typeName(fd))
			for i := range nested {
				nested[i].Path = fd.Path + "." + nested[i].Path
			}
			s.Fields = append(s.Fields, nested...)
			continue
		}

		s.Fields = append(s.Fields, *fd)
	}

	return s, nil
}
