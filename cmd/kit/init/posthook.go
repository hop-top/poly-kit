// Package kitinit — posthook.go generates the after-PR-open git hook
// scaffolded by `kit init` (T-0774, contract: docs/contracts/kit-init-pr-wiring.md
// sections 2, 3, 5, 6, 8).
//
// The hook script (.githooks/post-pr-open) is invoked by adopters
// after `gh pr create` succeeds (or any equivalent PR-open path). It
// runs a synchronous liveness probe against ${KIT_BUS_INGRESS_URL}/healthz
// with a 5-second timeout. When the bus is enabled (vars.KIT_BUS_ENABLED ==
// "true") AND the probe returns 2xx within 5s, the hook trusts a remote
// bus host to deliver follow-up events — no local task is scheduled.
// Otherwise, the hook schedules a local `tlc` task due 10 minutes after
// PR open with kit:pr-followup tagging, event family tagging, head SHA,
// branch, PR URL, and the originating track/task link when discoverable
// from the branch name (matching the t-NNNN-* convention).
//
// Why a portable shell script: hops adopters target Linux + macOS today;
// Windows support is best-effort and TODO'd inline. The script is
// non-destructive — never overwrites an existing file — and pairs with
// the existing .githooks/ convention (cf. .githooks/pre-push).
package kitinit

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// postPROpenHookPath is the repo-relative POSIX path where the hook is
// generated. Pinned here so tests and Summary integration share a single
// source of truth.
const postPROpenHookPath = ".githooks/post-pr-open"

//go:embed posthook_template.sh
var postPROpenHookScript []byte

// PostHookAction enumerates the per-file action a posthook generation
// run takes. Mirrors the action-shape pinned in the contract (Section 6).
type PostHookAction string

const (
	// PostHookActionWrite — a fresh file written (or would be in dry-run).
	PostHookActionWrite PostHookAction = "write"
	// PostHookActionSkipUnchanged — the on-disk file is byte-identical to
	// what kit would write; silent no-op.
	PostHookActionSkipUnchanged PostHookAction = "skip-unchanged"
	// PostHookActionSuggestSibling — the on-disk file differs from what
	// kit would write; emit <path>.kit-suggested with the would-be contents.
	PostHookActionSuggestSibling PostHookAction = "suggest-sibling"
	// PostHookActionSkippedFlag — the generator was disabled by the
	// adopter (--without-githook-post-pr-open). No on-disk side effect.
	PostHookActionSkippedFlag PostHookAction = "skipped-flag"
)

// PostHookResult records the side effect of one
// GeneratePostPROpenHook call. Returned to bootstrap/augment so the
// final Summary can surface the outcome via the existing Written /
// Suggested slices.
type PostHookResult struct {
	// Path is the absolute path to the would-be hook script under target.
	Path string
	// Action is the resolved per-spec action (Section 6).
	Action PostHookAction
	// SuggestedPath is the absolute path to a <path>.kit-suggested
	// sibling, set only when Action == PostHookActionSuggestSibling.
	SuggestedPath string
	// Reason mirrors the contract's reason taxonomy (Section 6) for the
	// JSON dry-run output: "new", "refresh", "user-edited", "skipped".
	Reason string
	// SHA256 is the hex-encoded SHA-256 of the generated content. Used
	// downstream by .kit/generated.json (Section 6) — surfaced here so a
	// future generator pass can fold this into the manifest without
	// recomputing.
	SHA256 string
}

// GeneratePostPROpenHook materialises .githooks/post-pr-open under
// target. enabled=false short-circuits to PostHookActionSkippedFlag
// (used by --without-githook-post-pr-open).
//
// Non-destructive guarantees per contract Section 6:
//   - identical existing file → PostHookActionSkipUnchanged, no write
//   - differing existing file → write <path>.kit-suggested sibling,
//     leave the original untouched (PostHookActionSuggestSibling)
//   - missing file → write fresh (PostHookActionWrite)
//
// Suggestion-sibling cleanup: when the current on-disk file matches a
// previously-suggested sibling, the sibling is auto-removed so the
// working tree doesn't accumulate stale .kit-suggested files (Section 6).
// We perform that cleanup before computing the result so callers see a
// stable view.
//
// dryRun=true suppresses bytes on disk but mirrors the result of a real
// run, matching the engine semantics in internal/template.
func GeneratePostPROpenHook(target string, enabled, dryRun bool) (PostHookResult, error) {
	absPath := filepath.Join(target, filepath.FromSlash(postPROpenHookPath))
	sum := sha256.Sum256(postPROpenHookScript)
	digest := hex.EncodeToString(sum[:])

	res := PostHookResult{
		Path:   absPath,
		SHA256: digest,
	}
	if !enabled {
		res.Action = PostHookActionSkippedFlag
		res.Reason = "skipped"
		return res, nil
	}

	existing, err := os.ReadFile(absPath)
	switch {
	case err == nil:
		// Auto-cleanup stale .kit-suggested when the user has converged
		// with what kit would write (Section 6).
		if string(existing) == string(postPROpenHookScript) {
			suggested := absPath + ".kit-suggested"
			if !dryRun {
				if _, statErr := os.Stat(suggested); statErr == nil {
					_ = os.Remove(suggested)
				}
			}
			res.Action = PostHookActionSkipUnchanged
			res.Reason = "refresh"
			return res, nil
		}
		// Differs from kit's content → suggest sibling, never overwrite.
		suggested := absPath + ".kit-suggested"
		if !dryRun {
			if err := os.MkdirAll(filepath.Dir(suggested), 0o750); err != nil {
				return res, fmt.Errorf("kit init: post-pr-open: mkdir %q: %w", filepath.Dir(suggested), err)
			}
			if err := os.WriteFile(suggested, postPROpenHookScript, 0o755); err != nil {
				return res, fmt.Errorf("kit init: post-pr-open: write %q: %w", suggested, err)
			}
		}
		res.Action = PostHookActionSuggestSibling
		res.SuggestedPath = suggested
		res.Reason = "user-edited"
		return res, nil
	case os.IsNotExist(err):
		if !dryRun {
			if err := os.MkdirAll(filepath.Dir(absPath), 0o750); err != nil {
				return res, fmt.Errorf("kit init: post-pr-open: mkdir %q: %w", filepath.Dir(absPath), err)
			}
			// 0o755 — git hooks must be executable. mode bits are
			// honored on POSIX; Windows ignores the +x bit (TODO:
			// Windows requires a .bat companion or a git config
			// core.hooksPath that resolves the script via sh.exe).
			if err := os.WriteFile(absPath, postPROpenHookScript, 0o755); err != nil {
				return res, fmt.Errorf("kit init: post-pr-open: write %q: %w", absPath, err)
			}
		}
		res.Action = PostHookActionWrite
		res.Reason = "new"
		return res, nil
	default:
		return res, fmt.Errorf("kit init: post-pr-open: stat %q: %w", absPath, err)
	}
}

// branchTaskIDRegex matches the t-NNNN-* (or T-NNNN-*) branch convention
// used in this repo and the broader hop-top ecosystem. Captures the
// numeric portion so the hook can format a canonical T-NNNN identifier.
//
// Anchoring is intentional:
//   - The id must start the branch (no leading prefix like
//     "feat/t-0774-..." would resolve, by design — branch-prefix
//     conventions vary too much across teams to encode here).
//   - The terminator is "-" or end-of-string; "t-0774" or "t-0774-foo"
//     resolve, "t-0774foo" does not (the digit-then-letter sequence
//     would otherwise risk merging two distinct IDs).
//
// 3..6 digit width covers historical (T-001) and current (T-0774,
// T-12345) numbering without being a free-for-all.
var branchTaskIDRegex = regexp.MustCompile(`^[tT]-(\d{3,6})(?:-|$)`)

// ResolveTaskIDFromBranch returns a canonical "T-NNNN" identifier when
// branch starts with a t-NNNN convention, or "" when the branch shape
// doesn't match.
//
// Examples:
//
//	"t-0774-post-pr-hook"   → "T-0774"
//	"T-12345-something"     → "T-12345"
//	"feat/t-0774-fix"       → ""   (prefixed; opted out — see regex doc)
//	"main"                  → ""
//	"t-77"                  → ""   (below 3-digit minimum)
//
// Pure: side-effect-free; safe to call from both the Go-side dry-run
// and the shell hook (which mirrors the algorithm — see posthook_template.sh).
func ResolveTaskIDFromBranch(branch string) string {
	m := branchTaskIDRegex.FindStringSubmatch(strings.TrimSpace(branch))
	if len(m) != 2 {
		return ""
	}
	return "T-" + m[1]
}

// PostPROpenHookContent returns the embedded hook script. Exposed for
// tests that need to invoke the script directly (via /bin/sh) without
// going through GeneratePostPROpenHook's write semantics.
func PostPROpenHookContent() []byte {
	out := make([]byte, len(postPROpenHookScript))
	copy(out, postPROpenHookScript)
	return out
}

// applyPostHookToSummary folds res into the engine.Result-shaped slices
// that drive the Summary. Skipped-flag is reported as a Skipped entry
// so it shows up in the summary counters without polluting Written.
//
// Why fold into engine.Result rather than a sibling field: the existing
// Summary surface already routes Written / Suggested into the human and
// JSON renderers; piggybacking on those keeps the dry-run output stable
// for adopters of the contract Section 6 action taxonomy.
func applyPostHookToSummary(s *Summary, res PostHookResult) {
	switch res.Action {
	case PostHookActionWrite:
		s.Result.Written = append(s.Result.Written, res.Path)
	case PostHookActionSuggestSibling:
		s.Result.Suggested = append(s.Result.Suggested, res.SuggestedPath)
	case PostHookActionSkipUnchanged:
		s.Result.Skipped = append(s.Result.Skipped, res.Path)
	case PostHookActionSkippedFlag:
		s.Result.Skipped = append(s.Result.Skipped, res.Path)
	}
}
