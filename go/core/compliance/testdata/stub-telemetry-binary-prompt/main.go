// Stub binary for compliance runtime checks of F13 sub-conditions
// (e) first-run prompt fires-or-skips per precedence and (f) the
// persisted decision carries the canonical `prompt_version` field
// plus a valid `decision_source`.
//
// Sibling stubs live under ../stub-telemetry-binary/ (kill switch)
// and ../stub-telemetry-binary-inspect/ (consent subcommands +
// post-redact). The behavioral shape here is different from both —
// this stub's only job is to RESOLVE consent against env + persisted
// state per ADR-0036's precedence chain and WRITE the resulting
// decision back to <XDG_CONFIG_HOME>/kit/telemetry.yaml — so we keep
// it separate.
//
// Build via the test file's lazy sync.Once helper — never go-install'd.
// Tests live under hop.top/kit/hops/main/go/core/compliance.
//
// Modes are selected via the STUB_PROMPT env:
//
//	normal      Resolves consent per the documented precedence (see
//	            resolveConsent below) and writes the canonical YAML
//	            shape with `prompt_version: 1` and a valid
//	            decision_source from {env, config}. PASS path for
//	            all asserts. Default when STUB_PROMPT is unset.
//	bad-source  Resolves consent correctly but stamps an INVALID
//	            decision_source ("invalid"). Negative test for
//	            sub-condition (f) — runtime check must catch the
//	            unknown source and fail.
//	no-version  Resolves consent correctly but OMITS the
//	            `prompt_version` field entirely. Negative test for
//	            sub-condition (f) field-name lock — runtime check
//	            must catch the missing canonical field.
//
// Emission: on any invocation where the resolved consent is
// `granted`, we also write a single synthetic telemetry event to
// KIT_BUS_SINK_PATH. This is the load-bearing side effect for
// scenario 3 of the runtime check (env-beats-persisted-granted): the
// stub WOULD emit if not for DO_NOT_TRACK, so observing zero events
// proves the env-mask path is honored. Mirrors stub-telemetry-binary's
// emission shape (ADR-0035 §3) so the bus reader works for both.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	// We accept any subcommand — runtime_prompt.go invokes whatever
	// findReadCommand returns from the toolspec (e.g. "status"
	// --format json). The stub doesn't dispatch on the subcommand;
	// every invocation runs the resolve-persist-maybe-emit flow.

	xdgConfig := os.Getenv("XDG_CONFIG_HOME")
	if xdgConfig == "" {
		// No XDG_CONFIG_HOME → can't persist. Exit cleanly so the
		// harness's "no consent file" assertion fires the right
		// failure message (rather than us crashing here and confusing
		// the test).
		os.Exit(0)
	}

	mode := os.Getenv("STUB_PROMPT")
	if mode == "" {
		mode = "normal"
	}

	state, source := resolveConsent(xdgConfig)

	if err := persistConsent(xdgConfig, mode, state, source); err != nil {
		fmt.Fprintln(os.Stderr, "persist consent:", err)
		os.Exit(2)
	}

	// Emit a synthetic event ONLY if the resolved consent is
	// granted. Scenario 3 of the runtime check (env-beats-persisted-
	// granted) seeds state=granted into the file before running us
	// with DO_NOT_TRACK=1; resolveConsent below sees DO_NOT_TRACK and
	// returns denied, so no event is written — exactly what the
	// runtime check asserts.
	if state == "granted" {
		if err := emitEvent(); err != nil {
			fmt.Fprintln(os.Stderr, "emit event:", err)
			os.Exit(3)
		}
	}

	// Print an empty JSON object on stdout so the toolspec's
	// --format json contract is satisfied (rtConsentingTelemetryPrompt
	// itself doesn't check stdout, but the read-command pick uses
	// idempotent + output_schema commands, so a well-formed empty
	// JSON object keeps adjacent checks happy if they run too).
	fmt.Println("{}")
}

// resolveConsent implements ADR-0036's precedence chain, restricted
// to the subset of steps this stub needs:
//
//  1. DO_NOT_TRACK=1                     → denied / env
//  2. *_TELEMETRY_MODE=off               → denied / env
//  3. *_TELEMETRY_MODE=on                → granted / env
//  4. persisted decision present         → as stored / config
//  5. default (non-TTY)                  → denied / config
//
// The stub never prompts — exec.Command is always non-TTY, so the
// TTY branch of ADR-0036's chain is unreachable here (and documented
// as "covered by manual review" in the runtime check itself).
func resolveConsent(xdgConfig string) (state, source string) {
	if os.Getenv("DO_NOT_TRACK") == "1" {
		return "denied", "env"
	}
	// Honor any *_TELEMETRY_MODE seen in the environment. Walking
	// os.Environ lets the stub pick whichever shape the toolspec
	// declares (KIT_TELEMETRY_MODE or any app-prefixed form) without
	// hard-coding a list.
	for _, kv := range os.Environ() {
		i := strings.IndexByte(kv, '=')
		if i < 0 {
			continue
		}
		k, v := kv[:i], kv[i+1:]
		if !strings.HasSuffix(k, "_TELEMETRY_MODE") {
			continue
		}
		switch v {
		case "off":
			return "denied", "env"
		case "on":
			return "granted", "env"
		}
	}
	// Persisted decision wins next. We deliberately do NOT re-parse
	// the YAML — the runtime check pre-seeds via SeedConsent which
	// matches our own emit shape, so a simple "file exists with
	// state: granted" string check is enough for the stub.
	if seeded := readPersistedState(xdgConfig); seeded != "" {
		return seeded, "config"
	}
	// Default for non-TTY (which we always are): denied, source=config.
	return "denied", "config"
}

// readPersistedState scans <xdgConfig>/kit/telemetry.yaml for a
// `state:` line and returns its value (granted | denied). Returns ""
// when the file is absent or the state line cannot be found. We
// avoid a real YAML parser here to keep the stub dependency-free —
// the format SeedConsent writes is simple enough for a line scan.
func readPersistedState(xdgConfig string) string {
	path := filepath.Join(xdgConfig, "kit", "telemetry.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "state:") {
			return strings.TrimSpace(strings.TrimPrefix(trim, "state:"))
		}
	}
	return ""
}

// persistConsent writes the YAML shape ADR-0036 specifies, with mode-
// driven mutations for the negative tests. The 0o600 perm mirrors
// kit-consent's own write path (and the SeedConsent helper that
// seeds fixtures into the same file).
func persistConsent(xdgConfig, mode, state, source string) error {
	dir := filepath.Join(xdgConfig, "kit")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	path := filepath.Join(dir, "telemetry.yaml")

	// Mode tweaks: bad-source overrides source to an invalid value;
	// no-version omits the prompt_version line entirely. Otherwise
	// the YAML matches SeedConsent's wire format byte-for-byte.
	emitSource := source
	if mode == "bad-source" {
		emitSource = "invalid"
	}
	var sb strings.Builder
	sb.WriteString("telemetry:\n")
	sb.WriteString("  consent:\n")
	fmt.Fprintf(&sb, "    state: %s\n", state)
	sb.WriteString("    decided_at: 2026-05-19T12:00:00Z\n")
	if mode != "no-version" {
		sb.WriteString("    prompt_version: 1\n")
	}
	fmt.Fprintf(&sb, "    decision_source: %s\n", emitSource)

	return os.WriteFile(path, []byte(sb.String()), 0o600)
}

// emitEvent writes one synthetic telemetry event to
// KIT_BUS_SINK_PATH. Payload shape mirrors stub-telemetry-binary so
// any bus-reader assertions work uniformly across both stubs.
func emitEvent() error {
	sink := os.Getenv("KIT_BUS_SINK_PATH")
	if sink == "" {
		return nil
	}
	f, err := os.OpenFile(sink, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	ev := map[string]any{
		"schema_version":  "1",
		"sdk_lang":        "go",
		"installation_id": "stubprompt12345678901234567890ab",
		"mode":            "anon",
		"command_path":    []string{"stub-telemetry-binary-prompt"},
		"exit_code":       0,
		"duration_ms":     1,
		"occurred_at":     time.Now().UTC().Format(time.RFC3339Nano),
	}
	return json.NewEncoder(f).Encode(ev)
}
