// Package telemetry implements the `kit telemetry` subcommand tree.
// This file owns the first-run interactive consent prompt; the
// `status | enable | disable | reset | inspect` verbs land in sibling
// files (T-0666..T-0669).
//
// The prompt is the ONLY code path that may stamp
// consent.SourcePrompt on a persisted Decision. Every other source
// (env, flag, default) is set by the resolver in
// hop.top/kit/go/core/consent.
package telemetry

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mattn/go-isatty"

	"hop.top/kit/go/core/consent"
)

// PromptVersion is the disclosure copy version stamped onto every
// decision the prompt writes. Bump when the prompt copy materially
// changes per ADR-0036 §3 (new data category, new sink, redact
// contract loosens). Cosmetic edits do NOT bump it. The first version
// shipped to users is 1.
const PromptVersion = 1

// promptCopy is the literal disclosure text shown to the user before
// the y/n line. It names the categories collected, the explicit
// non-collected fields, the opt-out paths, and the DO_NOT_TRACK
// escape hatch — together these satisfy GDPR's "informed" prong
// (ADR-0036 §1).
const promptCopy = `kit can send anonymous usage telemetry to help improve the tooling: command names,
exit codes, kit version, OS/arch. No file paths, no arguments, no environment
values. You can opt out anytime with ` + "`kit telemetry disable`" + `, audit shipped events
with ` + "`kit telemetry inspect`" + `, or set DO_NOT_TRACK=1.

Share anonymous telemetry?`

// promptDeps bundles the runtime seams the prompt needs. Production
// code calls Prompt() which fills these from the real process; tests
// drive promptInternal directly with injected stdin/stdout/env/clock
// so neither os.Stdin nor os.Environ has to be mutated to assert
// behavior.
//
// Keeping the seam unexported preserves the public surface (Prompt +
// PromptVersion) at exactly what the task contract pins.
type promptDeps struct {
	// in is the byte source consulted when the prompt actually reads
	// a user response. promptInternal does NOT call isTTY on this — it
	// trusts the caller to gate prompting on isTTY(stdinFD) first.
	in io.Reader

	// out is where the disclosure copy and the "[y/N] " line are
	// written. Errors writing to out are non-fatal: we still attempt
	// to read a response (best-effort prompt UX on a broken stderr).
	out io.Writer

	// env mirrors consent.EnvProvider — same signature, same
	// "unset == empty string" contract. nil falls back to os.Getenv.
	env consent.EnvProvider

	// isTTY answers "can we ask the user?" for the precedence chain
	// step that branches on TTY-ness. Injected so tests can simulate
	// a TTY-attached stdin without actually being attached to one.
	isTTY func() bool

	// now stamps Decision.DecidedAt. Injected for deterministic test
	// assertions; nil falls back to time.Now().UTC().
	now func() time.Time
}

// Prompt drives the first-run consent prompt and persists the
// resulting decision. The returned Decision is the value the caller
// should hand to a ConsentHook; the persisted state on disk matches.
//
// Precedence (per ADR-0036 §5; only the rules relevant to the prompt
// itself appear here — the resolver covers the rest):
//
//  1. KIT_TELEMETRY_MODE=off (or any non-empty AppPrefix override):
//     skip prompt, persist denied/env.
//  2. DO_NOT_TRACK=1: skip prompt, persist denied/env.
//  3. Persisted decision whose prompt_version == PromptVersion: skip
//     prompt, return persisted verbatim (no write).
//  4. Persisted decision whose prompt_version != PromptVersion (stale
//     copy): re-prompt on TTY; on non-TTY, persist denied/config at
//     the current PromptVersion so the next TTY run sees a stale=miss
//     and re-prompts cleanly.
//  5. No persisted decision + non-TTY: persist denied/config.
//  6. No persisted decision + TTY: show prompt, persist
//     granted/prompt or denied/prompt per user's answer.
//
// The caller can inspect d.DecisionSource to disambiguate which
// branch fired. The only branch that returns SourcePrompt is the
// one that actually displayed the disclosure copy to a human.
func Prompt(ctx context.Context, store consent.Store) (consent.Decision, error) {
	return promptInternal(ctx, store, promptDeps{
		in:    os.Stdin,
		out:   os.Stderr,
		env:   consent.OSEnv(),
		isTTY: func() bool { return isatty.IsTerminal(os.Stdin.Fd()) },
		now:   func() time.Time { return time.Now().UTC() },
	})
}

// promptInternal is the testable core. All side-effects (stdin,
// stderr, env, TTY, clock) arrive through deps; the function is
// otherwise straight-line policy. Callers that want a different
// stdin (tests) or a different write target (future "preview"
// command) construct their own promptDeps.
//
//nolint:gocyclo // the precedence chain is intentionally readable as
// a flat switch ladder; collapsing branches into helpers obscures the
// ordering that ADR-0036 §5 fixes.
func promptInternal(ctx context.Context, store consent.Store, deps promptDeps) (consent.Decision, error) {
	if err := ctx.Err(); err != nil {
		return consent.Decision{}, err
	}

	env := deps.env
	if env == nil {
		env = consent.OSEnv()
	}
	now := deps.now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}

	// Step 1: KIT_TELEMETRY_MODE=off short-circuits to denied/env.
	//
	// This prompt-layer check examines KIT_TELEMETRY_MODE only;
	// app-prefix precedence (<APP>_TELEMETRY_MODE) is handled by
	// consent.Resolve at a higher layer (see ADR-0036 §5). The prompt
	// itself doesn't have an embedding-app context, so it consults
	// only the kit prefix. Embedding apps that need their own off
	// switch can set KIT_TELEMETRY_MODE=off in their own startup, or
	// rely on the resolver to honor <APP>_TELEMETRY_MODE.
	if strings.EqualFold(env("KIT_TELEMETRY_MODE"), "off") {
		return persist(ctx, store, consent.Decision{
			State:          consent.StateDenied,
			DecidedAt:      now(),
			PromptVersion:  PromptVersion,
			DecisionSource: consent.SourceEnv,
		})
	}

	// Step 2: DO_NOT_TRACK short-circuits to denied/env. Same
	// non-overridable posture as the resolver; the prompt MUST NOT
	// run for a user who set DO_NOT_TRACK because the prompt's
	// disclosure copy would imply they have a choice they don't.
	// Uses consent.DoNotTrackEnabled to honor the
	// consoledonottrack.com convention (any non-empty value other
	// than "0"/"false" counts as opted-out).
	if consent.DoNotTrackEnabled(env) {
		return persist(ctx, store, consent.Decision{
			State:          consent.StateDenied,
			DecidedAt:      now(),
			PromptVersion:  PromptVersion,
			DecisionSource: consent.SourceEnv,
		})
	}

	// Step 3 & 4: persisted decision. Fresh prompt_version → no-op.
	// Stale prompt_version → re-prompt (TTY) or auto-deny at the
	// current version (non-TTY).
	persisted, err := store.Get(ctx)
	if err != nil {
		return consent.Decision{}, fmt.Errorf("telemetry: load decision: %w", err)
	}
	if persisted.State == consent.StateGranted || persisted.State == consent.StateDenied {
		if persisted.PromptVersion == PromptVersion {
			// Fresh — nothing to do, no write, just hand back.
			return persisted, nil
		}
		// Stale. Fall through to the (re-)prompt branch below.
	}

	// Steps 5 & 6: no usable persisted decision (or stale). Branch
	// on TTY. Non-TTY auto-denies with source=config so the user can
	// distinguish later "you weren't asked" from "you said no".
	if !deps.isTTY() {
		return persist(ctx, store, consent.Decision{
			State:          consent.StateDenied,
			DecidedAt:      now(),
			PromptVersion:  PromptVersion,
			DecisionSource: consent.SourceConfig,
		})
	}

	// Interactive path. Default is No: the highlighted answer the
	// user accepts by hitting enter. Explicit "y" / "Y" / "yes"
	// grants. Anything else (including a blank line, "n", or EOF)
	// denies. Per task spec, this is the ONLY code path allowed to
	// write SourcePrompt.
	granted := askYesNo(deps.in, deps.out)
	state := consent.StateDenied
	if granted {
		state = consent.StateGranted
	}
	return persist(ctx, store, consent.Decision{
		State:          state,
		DecidedAt:      now(),
		PromptVersion:  PromptVersion,
		DecisionSource: consent.SourcePrompt,
	})
}

// askYesNo writes the disclosure copy + a "[y/N]: " line to out, then
// reads a single line from in. Returns true only for an explicit "y",
// "Y", or "yes" (case-insensitive). Blank input, EOF, "n", or any
// other token returns false — the default highlighted answer is No.
//
// We use bufio.Scanner over fmt.Fscanln to make EOF (closed stdin)
// behave identically to an empty line: both return false, neither
// errors. fmt.Fscanln on a closed stdin returns io.EOF which would
// force a separate error path the caller doesn't need.
func askYesNo(in io.Reader, out io.Writer) bool {
	// Best-effort write; ignore errors. A user staring at a broken
	// stderr is still going to see whatever shell printed by the
	// time they type their answer.
	_, _ = fmt.Fprintln(out, promptCopy)
	_, _ = fmt.Fprint(out, "[y/N]: ")

	sc := bufio.NewScanner(in)
	if !sc.Scan() {
		// EOF or scan error: treat as "default highlighted" — No.
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(sc.Text()))
	return answer == "y" || answer == "yes"
}

// persist writes d via store and returns (d, err). Always returns the
// passed-in Decision so callers can rely on the return value even
// when store.Set fails — the caller may still want to use the
// in-memory decision for this run while logging the persistence error.
func persist(ctx context.Context, store consent.Store, d consent.Decision) (consent.Decision, error) {
	if err := store.Set(ctx, d); err != nil {
		return d, fmt.Errorf("telemetry: persist decision: %w", err)
	}
	return d, nil
}
