package pkl

import (
	"regexp"
	"strconv"
	"strings"
)

// --- regex patterns ---

var (
	reModule    = regexp.MustCompile(`(?m)^module\s+(\S+)`)
	reField     = regexp.MustCompile(`(?m)^(\w+)\s*:\s*(.+)$`)
	reClassDecl = regexp.MustCompile(`(?m)^class\s+(\w+)\s*\{`)
	reUnionVal  = regexp.MustCompile(`"([^"]+)"`)
	reDefault   = regexp.MustCompile(`^(.+?)\s*=\s*(.+)$`)
	reBetween   = regexp.MustCompile(`isBetween\((\d+),\s*(\d+)\)`)
	rePattern   = regexp.MustCompile(`matches\((.+)\)`)
	reMinLen    = regexp.MustCompile(`length\s*>=\s*(\d+)`)
	reMaxLen    = regexp.MustCompile(`length\s*<=\s*(\d+)`)
	reLenBtw    = regexp.MustCompile(`length\.isBetween\((\d+),\s*(\d+)\)`)
	reGroupAnn  = regexp.MustCompile(`@wizard\.group\s+"([^"]+)"`)
	reWhenAnn   = regexp.MustCompile(`@wizard\.when\s+(.+)`)
	reInterp    = regexp.MustCompile(`#\{`)
	reConstType = regexp.MustCompile(`^(\w+)\((.+)\)$`)
	reListing   = regexp.MustCompile(`^Listing<(\w+)>(\?)?$`)
)

// typeName returns the raw type name stored temporarily in Enum[0]
// for unresolved class refs set by parseField.
func typeName(fd *FieldDef) string {
	if fd.Type == TypeString && len(fd.Enum) == 1 && fd.Enum[0] != "" &&
		fd.Enum[0][0] >= 'A' && fd.Enum[0][0] <= 'Z' {
		return fd.Enum[0]
	}
	return ""
}

func parseField(line string, comments []string) *FieldDef {
	m := reField.FindStringSubmatch(line)
	if m == nil {
		return nil
	}

	fd := &FieldDef{Path: m[1], Required: true}
	typeExpr := strings.TrimSpace(m[2])

	// Separate default value.
	var defaultStr string
	if dm := reDefault.FindStringSubmatch(typeExpr); dm != nil {
		typeExpr = strings.TrimSpace(dm[1])
		defaultStr = strings.TrimSpace(dm[2])
	}

	// Nullable?
	if strings.HasSuffix(typeExpr, "?") {
		fd.Required = false
		typeExpr = strings.TrimSuffix(typeExpr, "?")
	}

	// Union type: "a"|"b"|"c"
	if vals, ok := parseUnionType(typeExpr); ok {
		fd.Type = TypeStringEnum
		fd.Enum = vals
		if defaultStr == "" {
			fd.Default = vals[0]
		} else {
			fd.Default, fd.Computed = parseDefault(defaultStr, TypeStringEnum)
		}
		applyAnnotations(fd, comments)
		return fd
	}

	// Listing<String>
	if lm := reListing.FindStringSubmatch(typeExpr); lm != nil {
		fd.Type = TypeStringList
		if lm[2] == "?" {
			fd.Required = false
		}
		if defaultStr != "" {
			fd.Default, fd.Computed = parseDefault(defaultStr, TypeStringList)
		}
		applyAnnotations(fd, comments)
		return fd
	}

	// Constrained type: Type(constraint)
	if cm := reConstType.FindStringSubmatch(typeExpr); cm != nil {
		fd.Type = resolveBaseType(cm[1])
		fd.Constraints = parseConstraints(cm[2])
		if defaultStr != "" {
			fd.Default, fd.Computed = parseDefault(defaultStr, fd.Type)
		}
		applyAnnotations(fd, comments)
		return fd
	}

	// Simple type.
	fd.Type = resolveBaseType(typeExpr)

	// Unknown uppercase word => class reference; stash in Enum.
	if fd.Type == TypeString && len(typeExpr) > 0 &&
		typeExpr[0] >= 'A' && typeExpr[0] <= 'Z' && typeExpr != "String" {
		fd.Enum = []string{typeExpr}
	}

	if defaultStr != "" {
		fd.Default, fd.Computed = parseDefault(defaultStr, fd.Type)
	}

	applyAnnotations(fd, comments)
	return fd
}

func resolveBaseType(name string) FieldType {
	switch name {
	case "String":
		return TypeString
	case "Int":
		return TypeInt
	case "Float":
		return TypeFloat
	case "Boolean":
		return TypeBool
	case "Duration":
		return TypeDuration
	default:
		return TypeString
	}
}

func parseUnionType(typeStr string) ([]string, bool) {
	if !strings.Contains(typeStr, "|") || !strings.Contains(typeStr, `"`) {
		return nil, false
	}
	vals := reUnionVal.FindAllStringSubmatch(typeStr, -1)
	if len(vals) < 2 {
		return nil, false
	}
	out := make([]string, len(vals))
	for i, v := range vals {
		out[i] = v[1]
	}
	return out, true
}

func parseConstraints(expr string) []Constraint {
	var cs []Constraint
	// length.isBetween must emit MinLen+MaxLen (not Between).
	// Check it first so the generic isBetween regex doesn't match.
	if m := reLenBtw.FindStringSubmatch(expr); m != nil {
		lo, _ := strconv.Atoi(m[1])
		hi, _ := strconv.Atoi(m[2])
		cs = append(cs,
			Constraint{Kind: ConstraintMinLen, Value: lo},
			Constraint{Kind: ConstraintMaxLen, Value: hi},
		)
	} else if m := reBetween.FindStringSubmatch(expr); m != nil {
		lo, _ := strconv.Atoi(m[1])
		hi, _ := strconv.Atoi(m[2])
		cs = append(cs, Constraint{Kind: ConstraintBetween, Value: [2]int{lo, hi}})
	}
	if m := rePattern.FindStringSubmatch(expr); m != nil {
		cs = append(cs, Constraint{Kind: ConstraintPattern, Value: strings.Trim(m[1], `"`)})
	}
	if m := reMinLen.FindStringSubmatch(expr); m != nil {
		v, _ := strconv.Atoi(m[1])
		cs = append(cs, Constraint{Kind: ConstraintMinLen, Value: v})
	}
	if m := reMaxLen.FindStringSubmatch(expr); m != nil {
		v, _ := strconv.Atoi(m[1])
		cs = append(cs, Constraint{Kind: ConstraintMaxLen, Value: v})
	}
	return cs
}

func parseAnnotations(comments []string) (group string, when *FieldCondition, desc string) {
	var descParts []string
	for _, c := range comments {
		text := strings.TrimSpace(strings.TrimPrefix(c, "///"))
		if m := reGroupAnn.FindStringSubmatch(text); m != nil {
			group = m[1]
			continue
		}
		if m := reWhenAnn.FindStringSubmatch(text); m != nil {
			expr := strings.TrimSpace(m[1])
			when = &FieldCondition{
				Expression: expr,
				DependsOn:  extractFieldRefs(expr),
			}
			continue
		}
		if text != "" {
			descParts = append(descParts, text)
		}
	}
	desc = strings.Join(descParts, " ")
	return
}

func applyAnnotations(fd *FieldDef, comments []string) {
	fd.Group, fd.When, fd.Description = parseAnnotations(comments)
}

func extractFieldRefs(expr string) []string {
	re := regexp.MustCompile(`\b([a-z_]\w*)\b`)
	keywords := map[string]bool{
		"true": true, "false": true, "null": true,
		"and": true, "or": true, "not": true,
		"is": true, "if": true, "else": true,
	}
	seen := map[string]bool{}
	var refs []string
	for _, m := range re.FindAllStringSubmatch(expr, -1) {
		id := m[1]
		if !keywords[id] && !seen[id] {
			seen[id] = true
			refs = append(refs, id)
		}
	}
	return refs
}

func parseDefault(defaultStr string, ft FieldType) (any, bool) {
	computed := reInterp.MatchString(defaultStr)
	switch ft {
	case TypeString, TypeStringEnum:
		return strings.Trim(defaultStr, `"`), computed
	case TypeInt:
		if v, err := strconv.Atoi(defaultStr); err == nil {
			return v, computed
		}
		return defaultStr, computed
	case TypeFloat:
		if v, err := strconv.ParseFloat(defaultStr, 64); err == nil {
			return v, computed
		}
		return defaultStr, computed
	case TypeBool:
		return defaultStr == "true", computed
	default:
		return defaultStr, computed
	}
}

func parseClasses(source string) map[string]string {
	classes := map[string]string{}
	lines := strings.Split(source, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		m := reClassDecl.FindStringSubmatch(trimmed)
		if m == nil {
			continue
		}
		name := m[1]
		depth := strings.Count(trimmed, "{") - strings.Count(trimmed, "}")
		var body []string
		for j := i + 1; j < len(lines) && depth > 0; j++ {
			depth += strings.Count(lines[j], "{") - strings.Count(lines[j], "}")
			if depth > 0 {
				body = append(body, lines[j])
			}
		}
		classes[name] = strings.Join(body, "\n")
	}
	return classes
}

func parseNestedClass(body string, _ string) []FieldDef {
	lines := strings.Split(body, "\n")
	var fields []FieldDef
	var comments []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "///") {
			comments = append(comments, trimmed)
			continue
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			comments = nil
			continue
		}
		fd := parseField(trimmed, comments)
		comments = nil
		if fd != nil {
			fields = append(fields, *fd)
		}
	}
	return fields
}
