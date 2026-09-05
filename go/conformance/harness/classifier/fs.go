package classifier

// FS op constants. These mirror the xrr fs adapter's Op* constants
// but are kept here as plain strings so the classifier compiles
// against xrr releases that have not yet shipped the fs adapter
// (xrr <= v0.1.0-alpha.3). When the fs adapter is available the
// adopter wraps the call site themselves and threads the same
// op-string into the cassette; the classifier looks up by string.
const (
	FSOpWrite    = "write"
	FSOpMkdir    = "mkdir"
	FSOpRemove   = "remove"
	FSOpRename   = "rename"
	FSOpChmod    = "chmod"
	FSOpChown    = "chown"
	FSOpSymlink  = "symlink"
	FSOpHardlink = "hardlink"
	FSOpTruncate = "truncate"
)

// fsClass is the static op → Class table. The xrr fs adapter does
// not capture reads (the package doc literally says "Reads are
// intentionally not supported"), so every entry is at least a Write.
//
// Rename is conservatively classified as Destructive: when the
// destination pre-existed it is irrevocably overwritten. A future
// refinement could re-classify Rename → Write when adopters opt
// into a "destination-known-new" tag, but the conservative default
// matches survey §3.6.
var fsClass = map[string]Class{
	FSOpWrite:    ClassWrite,
	FSOpMkdir:    ClassWrite,
	FSOpChmod:    ClassWrite,
	FSOpChown:    ClassWrite,
	FSOpSymlink:  ClassWrite,
	FSOpHardlink: ClassWrite,
	FSOpRemove:   ClassDestructive,
	FSOpRename:   ClassDestructive,
	FSOpTruncate: ClassDestructive,
}

// FSRequest is the minimal fs request shape the classifier reads.
// Adopters using the xrr fs adapter (when published) can supply a
// *xrrfs.Request via type assertion; for now the classifier accepts
// either FSRequest or a map[string]any cassette payload.
type FSRequest struct {
	Op   string
	Path string
}

// ClassifyFS returns the Class for an fs mutation. Unknown ops fall
// to Write (conservative — fs cassettes never contain Reads).
func ClassifyFS(req *FSRequest) Class {
	if req == nil {
		return ClassUnknown
	}
	return ClassifyFSOp(req.Op)
}

// ClassifyFSOp exposes the op-only lookup for adopter tests.
func ClassifyFSOp(op string) Class {
	if op == "" {
		return ClassUnknown
	}
	if c, ok := fsClass[op]; ok {
		return c
	}
	// Any fs entry the harness doesn't know about is still a
	// mutation; default to Write rather than Unknown so adopters
	// can extend the op set without tripping the conservative
	// fail-closed paths.
	return ClassWrite
}
