package toolspec

import (
	"strconv"
	"strings"
)

// SchemaVersionEnv names the environment variable a harness sets to
// the highest manifest schema version it reads (MAJOR.MINOR). Both
// `kit toolspec` and every `<tool> spec` mounted with
// cli.RegisterSpecCommand read it through [NegotiateSchemaVersion].
const SchemaVersionEnv = "KIT_TOOLSPEC_SCHEMA"

// LatestSchemaVersion is the newest manifest schema version kit
// emits. 1.1 adds per-command fields that surface kit's command
// annotations; it is additive over 1.0, so a reader that ignores
// unknown fields reads either.
const LatestSchemaVersion = "1.1"

// knownSchemaVersions lists every manifest schema version kit can
// label a manifest with, oldest first. The manifest builder emits one
// layout (LatestSchemaVersion's); every older entry is a subset of it.
var knownSchemaVersions = []string{"1.0", LatestSchemaVersion}

// NegotiateSchemaVersion resolves the schema_version a manifest
// carries from the version its producer declares and the version a
// harness requested via [SchemaVersionEnv]. One rule serves
// `kit toolspec` and every `<tool> spec`:
//
//   - declared is a floor: the answer is never below it.
//   - An unset, empty or malformed request resolves to declared.
//   - A well-formed request at or below declared resolves to
//     declared: kit never downgrades.
//   - A well-formed request above declared resolves to the highest
//     version kit knows that does not exceed the request, or to
//     declared when no known version lies between the two. A request
//     beyond LatestSchemaVersion therefore gets LatestSchemaVersion.
//
// A declared value that is not MAJOR.MINOR, or that kit does not
// know, is returned unchanged: it is the producer's label and there
// is nothing to compare it against.
//
// Nothing is ever refused: like every other kit environment variable
// that fails to parse, a malformed request is ignored.
func NegotiateSchemaVersion(declared, requested string) string {
	floor, ok := parseSchemaVersion(declared)
	if !ok {
		return declared
	}
	ceiling, ok := parseSchemaVersion(requested)
	if !ok {
		return declared
	}
	resolved, best := declared, floor
	for _, v := range knownSchemaVersions {
		known, _ := parseSchemaVersion(v)
		if known.after(best) && !known.after(ceiling) {
			resolved, best = v, known
		}
	}
	return resolved
}

// schemaVersion is a parsed MAJOR.MINOR pair.
type schemaVersion struct{ major, minor int }

func (v schemaVersion) after(o schemaVersion) bool {
	if v.major != o.major {
		return v.major > o.major
	}
	return v.minor > o.minor
}

// parseSchemaVersion accepts exactly MAJOR.MINOR, both unsigned
// decimal integers, after trimming surrounding whitespace.
func parseSchemaVersion(s string) (schemaVersion, bool) {
	majorStr, minorStr, found := strings.Cut(strings.TrimSpace(s), ".")
	if !found {
		return schemaVersion{}, false
	}
	major, ok := parseUint(majorStr)
	if !ok {
		return schemaVersion{}, false
	}
	minor, ok := parseUint(minorStr)
	if !ok {
		return schemaVersion{}, false
	}
	return schemaVersion{major, minor}, true
}

func parseUint(s string) (int, bool) {
	if s == "" || strings.TrimLeft(s, "0123456789") != "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}
