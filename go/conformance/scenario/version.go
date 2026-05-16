package scenario

// SchemaVersion is the current scenario DSL schema_version. v1 only
// accepts the literal string "1". A bump to "2" requires a new kit
// binary; the parser refuses unknown schema versions.
const SchemaVersion = "1"

// GraderVersion is the semver of the grader implementation. Per the
// design Q5 resolution, this tracks the kit binary version. v1 ships
// "1.0.0" as a stable starting point; releases past Wave 2 bump it.
const GraderVersion = "1.0.0"

// SupportedSchemaVersions is the closed set of scenario schema
// versions the current binary can grade. Parsers gate on this; a
// scenario declaring a schema_version not in the set returns
// SCENARIO_SCHEMA_UNSUPPORTED.
var SupportedSchemaVersions = []string{SchemaVersion}

// IsSupportedSchemaVersion reports whether v is in
// SupportedSchemaVersions. O(n) on a 1-element slice; acceptable.
func IsSupportedSchemaVersion(v string) bool {
	for _, s := range SupportedSchemaVersions {
		if s == v {
			return true
		}
	}
	return false
}
