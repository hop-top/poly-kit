package parity_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"hop.top/kit/contracts/parity"
	"hop.top/kit/go/console/serve"
)

func TestParityStatusSymbols(t *testing.T) {
	kinds := []string{"info", "success", "error", "warn"}
	for _, k := range kinds {
		sym, ok := parity.Values.Status.Symbols[k]
		if !ok || sym == "" {
			t.Errorf("status.symbols[%q]: missing or empty", k)
		}
	}
}

func TestParityStatusSymbolValues(t *testing.T) {
	// Pin the exact values so any accidental change fails the test.
	want := map[string]string{
		"info":    "ℹ",
		"success": "✓",
		"error":   "●",
		"warn":    "▲",
	}
	for k, v := range want {
		if got := parity.Values.Status.Symbols[k]; got != v {
			t.Errorf("status.symbols[%q] = %q, want %q", k, got, v)
		}
	}
}

func TestParitySpinnerFrames(t *testing.T) {
	frames := parity.Values.Spinner.Frames
	if len(frames) == 0 {
		t.Fatal("spinner.frames: empty")
	}
	if parity.Values.Spinner.IntervalMs <= 0 {
		t.Errorf("spinner.interval_ms = %d, want > 0", parity.Values.Spinner.IntervalMs)
	}
}

func TestParityAnimRunes(t *testing.T) {
	if parity.Values.Anim.Runes == "" {
		t.Fatal("anim.runes: empty")
	}
	if parity.Values.Anim.IntervalMs <= 0 {
		t.Errorf("anim.interval_ms = %d, want > 0", parity.Values.Anim.IntervalMs)
	}
	if parity.Values.Anim.DefaultWidth <= 0 {
		t.Errorf("anim.default_width = %d, want > 0", parity.Values.Anim.DefaultWidth)
	}
}

// TestParityNoUnloadedBlocks is the drift guard: it fails when parity.json
// declares a top-level block the Go loader does not know about, and when
// parity.Blocks names a block parity.json no longer declares.
//
// Without it, a new block can be added to parity.json and read as enforced
// while nothing ever loads it — the exact failure mode that left verbosity,
// streams and table decorative. Any block added to parity.json must also get
// a parity.Data field and a parity.Blocks entry, or this test goes red.
//
// Keys beginning with "$" are JSON Schema metadata and are exempt.
func TestParityNoUnloadedBlocks(t *testing.T) {
	var declared map[string]json.RawMessage
	if err := json.Unmarshal(parity.Raw(), &declared); err != nil {
		t.Fatalf("parse embedded parity.json: %v", err)
	}

	known := make(map[string]bool, len(parity.Blocks))
	for _, b := range parity.Blocks {
		known[b] = true
	}

	for key := range declared {
		if strings.HasPrefix(key, "$") {
			continue // JSON Schema metadata, not content
		}
		if !known[key] {
			t.Errorf("parity.json declares block %q that the loader does not know.\n"+
				"parity.json is a loaded contract, not documentation: an unloaded block "+
				"is invisible to every test and every consumer.\n"+
				"Fix by adding a %q field to parity.Data and %q to parity.Blocks in "+
				"contracts/parity/parity.go — or, if the block is not a cross-language "+
				"constant, move it to prose in contracts/parity/README.md.", key, key, key)
		}
	}

	for _, b := range parity.Blocks {
		if _, ok := declared[b]; !ok {
			t.Errorf("parity.Blocks lists %q but parity.json no longer declares it; "+
				"drop it from parity.Blocks in contracts/parity/parity.go", b)
		}
	}
}

// TestParityLoadedBlocksNonZero asserts that every content block the loader
// knows actually unmarshalled into something. A block wired into Data with a
// mismatched json tag parses without error and leaves a zero value behind —
// silent, and exactly what this contract exists to prevent.
func TestParityLoadedBlocksNonZero(t *testing.T) {
	v := parity.Values

	if len(v.Status.Symbols) == 0 {
		t.Error("status.symbols: empty after load")
	}
	if len(v.Spinner.Frames) == 0 {
		t.Error("spinner.frames: empty after load")
	}
	if v.Anim.Runes == "" {
		t.Error("anim.runes: empty after load")
	}
	if len(v.Help.SectionOrder) == 0 || len(v.Help.Sections) == 0 {
		t.Error("help: section_order or sections empty after load")
	}
	if v.Verbosity.Flag == "" || len(v.Verbosity.Levels) == 0 || v.Verbosity.QuietOverride == "" {
		t.Errorf("verbosity: incompletely loaded: %+v", v.Verbosity)
	}
	if v.Streams.Flag == "" || v.Streams.LabelFormat == "" || v.Streams.Output == "" {
		t.Errorf("streams: incompletely loaded: %+v", v.Streams)
	}
}

// TestParityVerbosityValues pins the verbosity contract. These values are
// currently hardcoded identically in the Go, TS and Python ports; pinning
// them here makes an accidental change fail rather than silently diverge.
func TestParityVerbosityValues(t *testing.T) {
	v := parity.Values.Verbosity
	if v.Flag != "-V" {
		t.Errorf("verbosity.flag = %q, want %q", v.Flag, "-V")
	}
	want := map[string]string{"0": "info", "1": "debug", "2": "trace"}
	if !reflect.DeepEqual(v.Levels, want) {
		t.Errorf("verbosity.levels = %v, want %v", v.Levels, want)
	}
	if v.QuietOverride != "warn" {
		t.Errorf("verbosity.quiet_override = %q, want %q", v.QuietOverride, "warn")
	}
}

// TestParityStreamsValues pins the streams contract. The label format is the
// value each port reproduces when prefixing stream lines on stderr.
func TestParityStreamsValues(t *testing.T) {
	s := parity.Values.Streams
	if s.Flag != "--stream" {
		t.Errorf("streams.flag = %q, want %q", s.Flag, "--stream")
	}
	if s.LabelFormat != "[{name}]" {
		t.Errorf("streams.label_format = %q, want %q", s.LabelFormat, "[{name}]")
	}
	if s.Output != "stderr" {
		t.Errorf("streams.output = %q, want %q", s.Output, "stderr")
	}
}

// TestScopeDefaultsContractSync asserts that every per-language local copy of
// scope-defaults.json is byte-equal (after JSON parse) to the canonical
// contracts/parity/scope-defaults.json. Drift means a port has gone out of
// sync — fix by re-copying from contracts/parity/.
func TestScopeDefaultsContractSync(t *testing.T) {
	root := repoRoot(t)
	canonical := readJSON(t, filepath.Join(root, "contracts", "parity", "scope-defaults.json"))

	copies := map[string]string{
		"go/core/scope/scope-defaults.json":      filepath.Join(root, "go", "core", "scope", "scope-defaults.json"),
		"sdk/ts/src/scope-defaults.json":         filepath.Join(root, "sdk", "ts", "src", "scope-defaults.json"),
		"sdk/py/hop_top_kit/scope-defaults.json": filepath.Join(root, "sdk", "py", "hop_top_kit", "scope-defaults.json"),
	}

	for label, path := range copies {
		got := readJSON(t, path)
		if !reflect.DeepEqual(got, canonical) {
			t.Errorf("%s drifted from canonical contracts/parity/scope-defaults.json", label)
		}
	}
}

// TestScopeDefaultsRegistered asserts that scope-defaults.json appears in
// parity.json's "extends" list — the registry of contracts the parity suite
// covers.
func TestScopeDefaultsRegistered(t *testing.T) {
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "contracts", "parity", "parity.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Extends []string `json:"extends"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	for _, name := range v.Extends {
		if name == "scope-defaults.json" {
			return
		}
	}
	t.Fatal("contracts/parity/parity.json: extends[] missing scope-defaults.json")
}

func readJSON(t *testing.T, path string) any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return v
}

// repoRoot returns the repo root by walking up from the test file location
// until it finds go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repoRoot: walked to /, no go.mod found")
		}
		dir = parent
	}
}

// --- serve.json ---------------------------------------------------------
//
// serve.json is not a parity.json block: it records a per-language
// conformance status, not a constant every port loads. It is registered
// in parity.json's extends[] and carries its own tests, the same shape
// scope-defaults.json uses.

// serveContract is the decoded shape of contracts/parity/serve.json.
// Only the fields the tests below assert on are modeled; an unmodeled
// key is not silently accepted, because TestServeContractBlocks checks
// the declared key set separately.
type serveContract struct {
	Constants struct {
		NamePattern   string            `json:"name_pattern"`
		ReservedNames []string          `json:"reserved_names"`
		Topics        map[string]string `json:"topics"`
		ConfigKeys    map[string]struct {
			Type    string `json:"type"`
			Default any    `json:"default"`
		} `json:"config_keys"`
		FailurePolicies []string `json:"failure_policies"`
		Signals         []string `json:"signals"`
		ExitCodes       map[string]struct {
			Code string `json:"code"`
			Exit int    `json:"exit"`
		} `json:"exit_codes"`
	} `json:"constants"`
	Behaviors map[string]string         `json:"behaviors"`
	Ports     map[string]map[string]any `json:"ports"`
}

func readServeContract(t *testing.T) serveContract {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "contracts", "parity", "serve.json"))
	if err != nil {
		t.Fatalf("read serve.json: %v", err)
	}
	var c serveContract
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("parse serve.json: %v", err)
	}
	return c
}

// TestServeContractRegistered asserts serve.json appears in parity.json's
// extends[] — the registry of contracts the parity suite covers.
func TestServeContractRegistered(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "contracts", "parity", "parity.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Extends []string `json:"extends"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	for _, name := range v.Extends {
		if name == "serve.json" {
			return
		}
	}
	t.Fatal("contracts/parity/parity.json: extends[] missing serve.json")
}

// TestServeContractMatchesGo pins the fixture's constants against the Go
// reference implementation. Go is the authority for these values, so a
// change on either side that is not made on both fails here rather than
// leaving the sibling ports implementing a stale contract.
func TestServeContractMatchesGo(t *testing.T) {
	c := readServeContract(t)

	t.Run("reserved names", func(t *testing.T) {
		for _, name := range c.Constants.ReservedNames {
			if !serve.IsReservedName(name) {
				t.Errorf("serve.json reserves %q but Go does not", name)
			}
		}
		for _, name := range []string{"all", "none", "list"} {
			if !slices.Contains(c.Constants.ReservedNames, name) {
				t.Errorf("Go reserves %q but serve.json does not", name)
			}
		}
	})

	t.Run("name pattern", func(t *testing.T) {
		re, err := regexp.Compile(c.Constants.NamePattern)
		if err != nil {
			t.Fatalf("name_pattern does not compile: %v", err)
		}
		// The fixture's pattern and Go's ValidateName must agree on
		// which identifiers are usable. Reserved words are excluded:
		// ValidateName refuses them for a separate reason, asserted above.
		for _, name := range []string{"api", "socket", "a", "a-b", "a1", "mcp-stdio"} {
			if !re.MatchString(name) {
				t.Errorf("name_pattern rejects %q, which Go accepts", name)
			}
			if err := serve.ValidateName(name); err != nil {
				t.Errorf("Go rejects %q, which name_pattern accepts: %v", name, err)
			}
		}
		for _, name := range []string{"", "API", "1api", "-api", "api_v2", "api.v2"} {
			if re.MatchString(name) {
				t.Errorf("name_pattern accepts %q, which Go rejects", name)
			}
			if err := serve.ValidateName(name); err == nil {
				t.Errorf("Go accepts %q, which name_pattern rejects", name)
			}
		}
	})

	t.Run("topics", func(t *testing.T) {
		want := serve.DefaultTopics(serve.DefaultTopicPrefix)
		if got := c.Constants.Topics["prefix"]; got != serve.DefaultTopicPrefix {
			t.Errorf("topics.prefix = %q, want %q", got, serve.DefaultTopicPrefix)
		}
		for key, topic := range want {
			got, ok := c.Constants.Topics[key]
			if !ok {
				t.Errorf("serve.json topics missing %q (Go emits %q)", key, topic)
				continue
			}
			if got != string(topic) {
				t.Errorf("topics[%q] = %q, want %q", key, got, topic)
			}
		}
		for key := range c.Constants.Topics {
			if key == "prefix" {
				continue
			}
			if _, ok := want[key]; !ok {
				t.Errorf("serve.json declares topic %q that Go does not emit", key)
			}
		}
	})

	t.Run("exit codes", func(t *testing.T) {
		for name, row := range c.Constants.ExitCodes {
			o := serve.LifecycleOutcome(name)
			if got := serve.ExitCodeFor(o); got != row.Exit {
				t.Errorf("exit_codes[%q].exit = %d, Go returns %d", name, row.Exit, got)
			}
			if got := serve.CodeFor(o); got != row.Code {
				t.Errorf("exit_codes[%q].code = %q, Go returns %q", name, row.Code, got)
			}
		}
		// Every outcome Go declares must be recorded, so a new one
		// cannot be added without telling the sibling ports about it.
		for _, o := range []serve.LifecycleOutcome{
			serve.OutcomeCleanStop, serve.OutcomeInvalidSelection,
			serve.OutcomeConfigInvalid, serve.OutcomeNoServices,
			serve.OutcomeUnknownService, serve.OutcomePolicyDenied,
			serve.OutcomeStartFailed, serve.OutcomeRuntimeCrash,
			serve.OutcomeShutdownTimeout,
		} {
			if _, ok := c.Constants.ExitCodes[string(o)]; !ok {
				t.Errorf("serve.json exit_codes missing outcome %q", o)
			}
		}
	})

	t.Run("failure policies", func(t *testing.T) {
		for _, p := range c.Constants.FailurePolicies {
			if !serve.FailurePolicy(p).IsValid() {
				t.Errorf("serve.json declares failure policy %q that Go rejects", p)
			}
		}
		for _, p := range []serve.FailurePolicy{serve.FailFast, serve.Isolate} {
			if !slices.Contains(c.Constants.FailurePolicies, string(p)) {
				t.Errorf("Go declares failure policy %q that serve.json omits", p)
			}
		}
		if got := c.Constants.ConfigKeys["services.failure_policy"].Default; got != string(serve.DefaultFailurePolicy) {
			t.Errorf("services.failure_policy default = %v, Go default is %q", got, serve.DefaultFailurePolicy)
		}
	})

	t.Run("timeout defaults", func(t *testing.T) {
		for key, want := range map[string]time.Duration{
			"services.<name>.ready_timeout": serve.DefaultReadyTimeout,
			"services.<name>.stop_timeout":  serve.DefaultStopTimeout,
			"services.shutdown_timeout":     serve.DefaultShutdownTimeout,
		} {
			raw, ok := c.Constants.ConfigKeys[key].Default.(string)
			if !ok {
				t.Errorf("config_keys[%q].default is not a duration string", key)
				continue
			}
			got, err := time.ParseDuration(raw)
			if err != nil {
				t.Errorf("config_keys[%q].default %q: %v", key, raw, err)
				continue
			}
			if got != want {
				t.Errorf("config_keys[%q].default = %s, Go default is %s", key, got, want)
			}
		}
		if got := c.Constants.ConfigKeys["services.<name>.enabled"].Default; got != false {
			t.Errorf("services.<name>.enabled default = %v, want false", got)
		}
	})
}

// TestServeContractPortsWellFormed asserts every port carries a status
// for every declared behavior, and that each status is one of the
// declared vocabulary.
//
// PENDING is deliberately not a failure: the harness's job is to make
// an unimplemented port visible, not to fail a build over work that has
// not started. What it does fail on is a port that omits a behavior
// entirely, which would let a gap go unrecorded.
func TestServeContractPortsWellFormed(t *testing.T) {
	c := readServeContract(t)

	if len(c.Behaviors) == 0 {
		t.Fatal("serve.json declares no behaviors")
	}
	if len(c.Ports) == 0 {
		t.Fatal("serve.json declares no ports")
	}

	valid := map[string]bool{"SHIPPED": true, "PENDING": true, "N/A": true}

	for port, statuses := range c.Ports {
		for behavior := range c.Behaviors {
			raw, ok := statuses[behavior]
			if !ok {
				t.Errorf("ports[%q] has no status for behavior %q", port, behavior)
				continue
			}
			s, ok := raw.(string)
			if !ok {
				t.Errorf("ports[%q][%q] = %v, want a status string", port, behavior, raw)
				continue
			}
			if !valid[s] {
				t.Errorf("ports[%q][%q] = %q, not one of SHIPPED/PENDING/N/A", port, behavior, s)
			}
		}
		for key := range statuses {
			if key == "implementation" || key == "note" {
				continue
			}
			if _, ok := c.Behaviors[key]; !ok {
				t.Errorf("ports[%q] carries status for unknown behavior %q", port, key)
			}
		}
	}
}

// TestServeContractGoIsShipped asserts the reference port claims no
// PENDING. Go is the authority for this contract; a PENDING there would
// mean the fixture and the implementation disagree about what exists.
func TestServeContractGoIsShipped(t *testing.T) {
	c := readServeContract(t)
	statuses, ok := c.Ports["go"]
	if !ok {
		t.Fatal("serve.json declares no go port")
	}
	for behavior := range c.Behaviors {
		if got := statuses[behavior]; got != "SHIPPED" {
			t.Errorf("ports.go[%q] = %v, want SHIPPED (Go is the reference)", behavior, got)
		}
	}
}
