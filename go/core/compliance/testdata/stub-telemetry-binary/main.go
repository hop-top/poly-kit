// stub-telemetry-binary is a tiny program used by the compliance
// package's runtime-check tests (T-0701 and the broader F13 suite).
//
// Real adopter binaries don't yet auto-route from KIT_BUS_SINK=jsonl
// (see ADR-0037 / T-0700 harness caveat), so the runtime check needs
// a stand-in that DOES honor KIT_BUS_SINK_PATH directly. This stub
// emits (or refuses to emit) a single synthetic event per invocation
// based on env flags, so each F13 sub-check can verify the
// harness-plus-check loop end-to-end against a controlled fixture.
//
// Behavior is driven by the STUB_EMIT env var:
//
//	STUB_EMIT=respect — honors DO_NOT_TRACK, KIT_TELEMETRY_MODE,
//	                    and any *_TELEMETRY_MODE env. Used to model
//	                    a well-behaved adopter binary.
//	STUB_EMIT=on      — emits unconditionally, ignoring kill-switch
//	                    envs. Used to model a NON-compliant binary
//	                    so the runtime check's failure path is
//	                    exercised.
//	STUB_EMIT=never   — never emits, regardless of env. Used to
//	                    verify the runtime check's harness-sanity
//	                    baseline catches a binary that simply never
//	                    routes events.
//	(unset)           — same as `respect` (safe default).
//
// The event payload mirrors the shape adopters write per ADR-0035 §3
// (schema_version, sdk_lang, installation_id, mode, command_path,
// exit_code, duration_ms, occurred_at). It's a synthetic stub so the
// values are static; the compliance check only cares about presence,
// not content.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

func main() {
	sink := os.Getenv("KIT_BUS_SINK_PATH")
	if sink == "" {
		// No sink configured — can't emit, but not an error per the
		// harness contract (the harness always sets KIT_BUS_SINK_PATH;
		// a missing one signals an unrelated direct invocation).
		os.Exit(0)
	}

	mode := os.Getenv("STUB_EMIT")
	if mode == "" {
		mode = "respect"
	}

	switch mode {
	case "never":
		os.Exit(0)

	case "respect":
		if os.Getenv("DO_NOT_TRACK") == "1" {
			os.Exit(0)
		}
		// Honor every *_TELEMETRY_MODE=off seen in the environment.
		// Walking os.Environ lets the runtime check pick whichever
		// shape the toolspec declares (KIT_TELEMETRY_MODE or any
		// app-prefixed form like SPACED_TELEMETRY_MODE) without the
		// stub needing to know which one.
		for _, kv := range os.Environ() {
			i := strings.IndexByte(kv, '=')
			if i < 0 {
				continue
			}
			k, v := kv[:i], kv[i+1:]
			if strings.HasSuffix(k, "_TELEMETRY_MODE") && v == "off" {
				os.Exit(0)
			}
		}
		// fall through to emit

	case "on":
		// emit unconditionally
	}

	f, err := os.OpenFile(sink, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	defer f.Close()

	ev := map[string]any{
		"schema_version":  "1",
		"sdk_lang":        "go",
		"installation_id": "stubabc1234567890123456789012345",
		"mode":            "anon",
		"command_path":    []string{"stub-telemetry-binary"},
		"exit_code":       0,
		"duration_ms":     1,
		"occurred_at":     time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := json.NewEncoder(f).Encode(ev); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	}
}
