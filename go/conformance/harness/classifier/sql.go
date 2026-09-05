package classifier

import (
	"regexp"
	"strings"

	xrrsql "hop.top/xrr/adapters/sql"
)

var sqlWSRe = regexp.MustCompile(`\s+`)

// sqlVerb buckets keyed by the first non-whitespace token of the
// normalized query. WITH-prefixed CTE queries are sniffed for an
// inner write verb; see ClassifySQLQuery.
var (
	sqlReadVerbs = map[string]struct{}{
		"SELECT": {}, "SHOW": {}, "DESCRIBE": {}, "DESC": {},
		"EXPLAIN": {}, "PRAGMA": {}, "VALUES": {},
	}
	sqlWriteVerbs = map[string]struct{}{
		"INSERT": {}, "UPDATE": {}, "MERGE": {}, "UPSERT": {},
		"COPY": {}, "CREATE": {}, "ALTER": {}, "GRANT": {},
		"REVOKE": {}, "SET": {}, "ANALYZE": {}, "VACUUM": {},
		"REINDEX": {}, "REFRESH": {}, "CLUSTER": {}, "COMMENT": {},
	}
	sqlDestructiveVerbs = map[string]struct{}{
		"DELETE": {}, "DROP": {}, "TRUNCATE": {},
		"RESET": {}, "REPLACE": {},
	}
)

// ClassifySQL returns the Class for a SQL request by parsing the
// first verb of the normalized query.
func ClassifySQL(req *xrrsql.Request) Class {
	if req == nil {
		return ClassUnknown
	}
	return ClassifySQLQuery(req.Query)
}

// ClassifySQLQuery exposes the verb-parser for adopters who want to
// classify queries without constructing an xrr Request.
func ClassifySQLQuery(query string) Class {
	if query == "" {
		return ClassUnknown
	}
	// Collapse whitespace and uppercase to match the verb tables.
	q := strings.ToUpper(sqlWSRe.ReplaceAllString(strings.TrimSpace(query), " "))
	first, rest := firstToken(q)
	switch first {
	case "":
		return ClassUnknown
	case "WITH":
		// Peek for the inner verb after the first `AS (`. A regex is
		// good enough for 95% of CTE patterns; a full parser is
		// overkill for a classifier.
		if v := cteInnerVerb(rest); v != "" {
			return classifyVerb(v)
		}
		// WITH ... SELECT is the common shape; assume Read when we
		// couldn't sniff an inner verb.
		return ClassRead
	}
	// Compound destructive operations like "VACUUM FULL" remain Write
	// per the survey: VACUUM is a maintenance op even with FULL —
	// classify by the leading verb only and document the exception.
	return classifyVerb(first)
}

func classifyVerb(v string) Class {
	if _, ok := sqlDestructiveVerbs[v]; ok {
		return ClassDestructive
	}
	if _, ok := sqlWriteVerbs[v]; ok {
		return ClassWrite
	}
	if _, ok := sqlReadVerbs[v]; ok {
		return ClassRead
	}
	return ClassUnknown
}

func firstToken(s string) (head, rest string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", ""
	}
	i := strings.IndexByte(s, ' ')
	if i < 0 {
		return s, ""
	}
	return s[:i], strings.TrimSpace(s[i+1:])
}

// cteInnerVerb returns the first verb after the leading `AS (` of a
// WITH-prefixed query. The input is already uppercased + space-
// collapsed.
func cteInnerVerb(s string) string {
	i := strings.Index(s, " AS (")
	if i < 0 {
		return ""
	}
	tail := strings.TrimSpace(s[i+len(" AS ("):])
	head, _ := firstToken(tail)
	return head
}
