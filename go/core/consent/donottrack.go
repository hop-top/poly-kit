package consent

import "strings"

// DoNotTrackEnabled reports whether the DO_NOT_TRACK env var (looked up
// via env) is set to a value that means "opt out".
//
// Per https://consoledonottrack.com — "the value should not matter".
// We honor the convention literally: any non-empty value counts as
// opted-out EXCEPT "0" or "false" (case-insensitive, surrounding
// whitespace ignored), which are treated as explicit "do track" hints
// that fall through to the rest of the precedence chain.
//
// This is the single canonical check shared by:
//   - the resolver (step 2 of ADR-0036 §5),
//   - the first-run prompt's env short-circuit,
//   - the `kit telemetry enable` env-block helper.
//
// Centralizing here means the three sites cannot drift: a value that
// blocks enable also blocks the prompt and blocks the resolver.
func DoNotTrackEnabled(env EnvProvider) bool {
	if env == nil {
		env = OSEnv()
	}
	v := strings.ToLower(strings.TrimSpace(env("DO_NOT_TRACK")))
	if v == "" {
		return false
	}
	if v == "0" || v == "false" {
		return false
	}
	return true
}
