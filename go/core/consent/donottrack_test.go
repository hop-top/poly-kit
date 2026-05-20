package consent

import "testing"

// TestDoNotTrackEnabled_VariantsAccepted pins the broadened
// consoledonottrack.com behavior: any non-empty value other than
// "0"/"false" (case-insensitive, whitespace trimmed) counts as
// opted-out. The historical "only literal 1" interpretation was
// narrower than the spec and surprised users with DO_NOT_TRACK=true
// in their shell rc.
func TestDoNotTrackEnabled_VariantsAccepted(t *testing.T) {
	t.Parallel()

	cases := []string{
		"1",
		"true",
		"yes",
		"TRUE",
		"Yes",
		"anything",
		" 1 ",   // surrounding whitespace trimmed
		"\t1\n", // tabs and newlines too
		"on",
		"y",
	}
	for _, v := range cases {
		v := v
		t.Run(v, func(t *testing.T) {
			env := MapEnv(map[string]string{"DO_NOT_TRACK": v})
			if !DoNotTrackEnabled(env) {
				t.Fatalf("DoNotTrackEnabled(DO_NOT_TRACK=%q) = false, want true", v)
			}
		})
	}
}

// TestDoNotTrackEnabled_NegativesIgnored confirms the two recognized
// "explicit do-track" tokens (and the unset case) do NOT short-circuit
// the precedence chain. Anything else broadly counts as opt-out.
func TestDoNotTrackEnabled_NegativesIgnored(t *testing.T) {
	t.Parallel()

	cases := []string{
		"",
		"0",
		"false",
		"FALSE",
		"False",
		" 0 ",
		"\tfalse\n",
	}
	for _, v := range cases {
		v := v
		t.Run("val="+v, func(t *testing.T) {
			env := MapEnv(map[string]string{"DO_NOT_TRACK": v})
			if DoNotTrackEnabled(env) {
				t.Fatalf("DoNotTrackEnabled(DO_NOT_TRACK=%q) = true, want false", v)
			}
		})
	}
}

// TestDoNotTrackEnabled_NilEnv exercises the OSEnv fallback path. The
// test asserts only that nil doesn't panic and returns false (the test
// process is not expected to have DO_NOT_TRACK set in CI).
func TestDoNotTrackEnabled_NilEnv(t *testing.T) {
	// Deliberately NOT parallel: relies on the absence of DO_NOT_TRACK
	// in the process env, which a sibling parallel test could mutate.
	t.Setenv("DO_NOT_TRACK", "")
	if DoNotTrackEnabled(nil) {
		t.Fatal("DoNotTrackEnabled(nil) = true with empty DO_NOT_TRACK, want false")
	}
}
