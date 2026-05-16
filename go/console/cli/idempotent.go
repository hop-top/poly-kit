package cli

import (
	"github.com/spf13/cobra"
	"hop.top/kit/go/console/cli/idemstore"
)

// WithIdempotencyStore returns a Root option that installs store as
// the kit-managed --idempotency-key replay backend. The store is
// retained on r.IdemStore and consumed by WrapRunE; pass nil to
// explicitly disable replay.
//
// Adopters typically call this with idemstore.OpenSQLite(path, ttl)
// where path is xdg.StateFile("<tool>", "idempotency.db"). Tests
// pass idemstore.Memory() for an in-process backend.
func WithIdempotencyStore(store idemstore.Store) func(*Root) {
	return func(r *Root) {
		r.IdemStore = store
	}
}

// Idempotency classifies whether re-running a command with the same
// inputs is observably equivalent to a single invocation. Agents and
// the delegation policy engine read this tag to decide whether
// duplicate-detection (--idempotency-key) is required and whether the
// runtime should auto-record-and-replay results.
//
// See ~/.ops/docs/cli-conventions-with-kit.md §8.5.
type Idempotency string

const (
	// IdempotencyYes marks a command whose effect is naturally
	// idempotent: re-running with the same inputs produces the same
	// observable state (list, show, get, sync, delete-by-id).
	IdempotencyYes Idempotency = "yes"
	// IdempotencyNo marks a command whose effect is not idempotent
	// without further protection (create, add — each call mints new
	// state).
	IdempotencyNo Idempotency = "no"
	// IdempotencyConditional marks a command that is idempotent when
	// the caller provides an idempotency key. Kit auto-registers
	// --idempotency-key=<key> on these commands and replays the
	// recorded result on subsequent invocations with the same key.
	IdempotencyConditional Idempotency = "conditional"
)

// idempotentAnnotation is the cobra annotation key kit reads to
// discover the declared idempotency class. Reserved under the kit/
// prefix per §3.5 of cli-conventions-with-kit.md.
const idempotentAnnotation = "kit/idempotent"

// GetIdempotency returns the declared idempotency class on a command.
// Returns ("", false) when the annotation is missing (which
// Root.Validate refuses for leaf commands once auto-apply has run).
func GetIdempotency(cmd *cobra.Command) (Idempotency, bool) {
	if cmd == nil || cmd.Annotations == nil {
		return "", false
	}
	v, ok := cmd.Annotations[idempotentAnnotation]
	if !ok {
		return "", false
	}
	return Idempotency(v), true
}

// SetIdempotency attaches the kit/idempotent tag in idiomatic form.
// Equivalent to setting cmd.Annotations["kit/idempotent"] = string(i).
func SetIdempotency(cmd *cobra.Command, i Idempotency) {
	if cmd.Annotations == nil {
		cmd.Annotations = make(map[string]string)
	}
	cmd.Annotations[idempotentAnnotation] = string(i)
}

// validIdempotency is the closed set Root.Validate accepts. Adding a
// new class here requires updating §8.5 of the
// cli-conventions-with-kit.md spec first.
var validIdempotency = map[Idempotency]bool{
	IdempotencyYes:         true,
	IdempotencyNo:          true,
	IdempotencyConditional: true,
}

// defaultIdempotency maps verb (cobra command Name) to its kit-default
// idempotency tag. Kit applies these defaults BEFORE Root.Validate
// runs, so adopters who accept the convention need not annotate
// every leaf manually. Adopter-supplied tags are never overwritten.
//
// Locked by §8.5: any change here is a spec change.
var defaultIdempotency = map[string]Idempotency{
	"list":      IdempotencyYes,
	"show":      IdempotencyYes,
	"get":       IdempotencyYes,
	"find":      IdempotencyYes,
	"search":    IdempotencyYes,
	"info":      IdempotencyYes,
	"current":   IdempotencyYes,
	"path":      IdempotencyYes,
	"paths":     IdempotencyYes,
	"doctor":    IdempotencyYes,
	"delete":    IdempotencyYes,
	"edit":      IdempotencyYes,
	"update":    IdempotencyYes,
	"use":       IdempotencyYes,
	"default":   IdempotencyYes,
	"sync":      IdempotencyYes,
	"reprocess": IdempotencyYes,
	"create":    IdempotencyNo,
	"add":       IdempotencyNo,
}

// applyDefaultIdempotency walks cmd's subtree and stamps the kit
// default idempotency tag on every leaf whose verb appears in
// defaultIdempotency. Adopter-supplied annotations are never
// overwritten — auto-apply only fills in the gap.
//
// Built-in commands (completion, help) and non-runnable shells are
// skipped on the same rules as Root.Validate.
func applyDefaultIdempotency(root *cobra.Command) {
	walk(root, func(cmd *cobra.Command) {
		if !isLeaf(cmd) || isBuiltin(cmd) {
			return
		}
		if !cmd.Runnable() {
			return
		}
		if _, ok := GetIdempotency(cmd); ok {
			return
		}
		def, ok := defaultIdempotency[cmd.Name()]
		if !ok {
			return
		}
		SetIdempotency(cmd, def)
	})
}
