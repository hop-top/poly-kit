// Package classifier maps xrr cassette interactions (one per
// adapter) onto a closed kit-blessed Class enum.
//
// The harness uses these per-adapter classifiers to decide whether
// an interaction recorded under --dry-run is a contract violation
// (anything not Read), and whether a destructive-gated leaf's
// recorded calls cross the destructive threshold.
//
// The taxonomy is intentionally narrow:
//
//   - Read         pure observation, no externally visible mutation
//   - Write        mutation without irreversible loss
//   - Destructive  mutation with potential irreversible loss
//   - Unknown      classifier could not decide; treated conservatively
//     by callers (i.e. as a violation under --dry-run)
//
// The per-adapter rules live in sibling files: exec.go, http.go,
// grpc.go, redis.go, sql.go, fs.go. Each exports a top-level
// `Classify<Adapter>(req)` function that returns a Class.
//
// The dispatcher Class(adapterID, req) routes by adapter name,
// returning Unknown for adapters this package does not recognize.
// Adopters can extend behavior by passing per-adapter overrides
// through harness.Option (e.g. harness.WithExecClassifier).
package classifier

// Class is the kit-blessed mutation classification.
type Class string

const (
	// ClassRead marks an interaction that has no externally visible
	// mutation. Safe under --dry-run.
	ClassRead Class = "read"
	// ClassWrite marks a non-destructive mutation (INSERT, POST,
	// SET, mkdir, etc.).
	ClassWrite Class = "write"
	// ClassDestructive marks an irreversible mutation (DELETE,
	// DROP, rm, FLUSHALL, etc.).
	ClassDestructive Class = "destructive"
	// ClassUnknown marks an interaction the classifier could not
	// place. Conservative callers should treat this as a mutating
	// violation.
	ClassUnknown Class = "unknown"
)

// IsMutating reports whether c is a non-Read class. Unknown is
// considered mutating — callers under --dry-run want a fail-closed
// posture.
func (c Class) IsMutating() bool {
	return c != ClassRead
}
