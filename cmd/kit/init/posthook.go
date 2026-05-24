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
// Cross-shell support: the generator emits two files in lockstep —
// .githooks/post-pr-open (bash, POSIX shells incl. Git-Bash on Windows)
// and .githooks/post-pr-open.ps1 (native PowerShell). Both implement
// the same flow (port — not refactor); adopters wire whichever matches
// their shell. The .ps1 is mode 0644 since Windows runs by extension,
// not by exec bit. Both files share the augment-conflict policy: a
// user-edited file yields a .kit-suggested sibling, never an overwrite.
//
// The bash script itself contains no OS-conditional path logic (it does
// HTTP probes via curl and shells out to gh/tlc), so MSYS/Cygwin/MINGW
// detection is unnecessary inside the script — the choice of which
// file to wire (.sh vs .ps1) lives in the user's hook configuration.
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

// postPROpenHookPath is the repo-relative POSIX path where the bash
// hook is generated. Pinned here so tests and Summary integration
// share a single source of truth.
const postPROpenHookPath = ".githooks/post-pr-open"

// postPROpenHookPS1Path is the PowerShell companion. Native Windows
// shells run this file; POSIX shells (incl. Git-Bash) run the .sh.
// Both are kept behaviorally aligned per the contract.
const postPROpenHookPS1Path = ".githooks/post-pr-open.ps1"

//go:embed posthook_template.sh
var postPROpenHookScript []byte

//go:embed posthook_template.ps1
var postPROpenHookPS1Script []byte

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

// GeneratePostPROpenHook materializes BOTH .githooks/post-pr-open and
// .githooks/post-pr-open.ps1 under target. enabled=false short-circuits
// to PostHookActionSkippedFlag for both (used by
// --without-githook-post-pr-open).
//
// The returned PostHookResult describes the primary (bash) file's
// action; the .ps1 companion follows the same per-file augment-conflict
// policy but its on-disk side-effects are not surfaced through this
// return value — callers wanting per-file granularity can re-stat the
// .ps1 path. We keep the single-result shape so existing Summary
// integration stays stable; the .ps1 is a behavioral mirror, not a
// distinct artifact.
//
// Non-destructive guarantees per contract Section 6 (applied to each
// file independently):
//   - identical existing file → PostHookActionSkipUnchanged, no write
//   - differing existing file → write <path>.kit-suggested sibling,
//     leave the original untouched (PostHookActionSuggestSibling)
//   - missing file → write fresh (PostHookActionWrite)
//
// Suggestion-sibling cleanup: when the current on-disk file matches a
// previously-suggested sibling, the sibling is auto-removed so the
// working tree doesn't accumulate stale .kit-suggested files (Section 6).
//
// dryRun=true suppresses bytes on disk but mirrors the result of a real
// run, matching the engine semantics in internal/template.
func GeneratePostPROpenHook(target string, enabled, dryRun bool) (PostHookResult, error) {
	if !enabled {
		// Surface the bash path for the skipped-flag result so the
		// Summary still references a stable identifier; the .ps1 is
		// suppressed in lockstep.
		absPath := filepath.Join(target, filepath.FromSlash(postPROpenHookPath))
		sum := sha256.Sum256(postPROpenHookScript)
		return PostHookResult{
			Path:   absPath,
			SHA256: hex.EncodeToString(sum[:]),
			Action: PostHookActionSkippedFlag,
			Reason: "skipped",
		}, nil
	}

	// Bash hook: 0o755 so POSIX shells (incl. Git-Bash on Windows) can
	// chmod-execute it. This is the primary result returned to callers.
	primary, err := writePostHookFile(target, postPROpenHookPath, postPROpenHookScript, 0o755, dryRun)
	if err != nil {
		return primary, err
	}

	// PowerShell companion: 0o644 — Windows runs by extension, not by
	// exec bit. Any error here is surfaced; per-file action is observable
	// on disk (the .ps1 sibling exists or not). We intentionally do not
	// fold the .ps1 action into the primary result — keeping the return
	// shape stable preserves Summary integration. The bash + .ps1 must
	// stay in lockstep, so a .ps1 write/skip mirrors the bash decision
	// in the common (refresh) case.
	if _, err := writePostHookFile(target, postPROpenHookPS1Path, postPROpenHookPS1Script, 0o644, dryRun); err != nil {
		return primary, err
	}

	return primary, nil
}

// writePostHookFile applies the augment-conflict policy to a single
// generator-owned file. Extracted from GeneratePostPROpenHook so the
// bash + .ps1 paths share the contract Section 6 behavior verbatim.
func writePostHookFile(target, relPath string, content []byte, mode os.FileMode, dryRun bool) (PostHookResult, error) {
	absPath := filepath.Join(target, filepath.FromSlash(relPath))
	sum := sha256.Sum256(content)
	res := PostHookResult{
		Path:   absPath,
		SHA256: hex.EncodeToString(sum[:]),
	}

	existing, err := os.ReadFile(absPath)
	switch {
	case err == nil:
		// Auto-cleanup stale .kit-suggested when the user has converged
		// with what kit would write (Section 6).
		if string(existing) == string(content) {
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
		// Sibling carries the same mode as the would-be file so adopters
		// who swap the names get a working hook without a chmod.
		suggested := absPath + ".kit-suggested"
		if !dryRun {
			if err := os.MkdirAll(filepath.Dir(suggested), 0o750); err != nil {
				return res, fmt.Errorf("kit init: post-pr-open: mkdir %q: %w", filepath.Dir(suggested), err)
			}
			if err := os.WriteFile(suggested, content, mode); err != nil {
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
			if err := os.WriteFile(absPath, content, mode); err != nil {
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

// PostPROpenHookContent returns the embedded bash hook script. Exposed
// for tests that need to invoke the script directly (via /bin/sh)
// without going through GeneratePostPROpenHook's write semantics.
func PostPROpenHookContent() []byte {
	out := make([]byte, len(postPROpenHookScript))
	copy(out, postPROpenHookScript)
	return out
}

// PostPROpenPS1Content returns the embedded PowerShell hook script.
// Exposed for tests that assert static properties of the .ps1
// (canonical-topic map, env-var names, fail-open contract) without
// requiring pwsh on the host. Symmetric with PostPROpenHookContent.
func PostPROpenPS1Content() []byte {
	out := make([]byte, len(postPROpenHookPS1Script))
	copy(out, postPROpenHookPS1Script)
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
