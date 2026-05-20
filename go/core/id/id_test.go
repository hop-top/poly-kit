package id_test

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	typeid "go.jetify.com/typeid"

	"hop.top/kit/go/core/id"
)

// --- canonical cross-language fixtures -------------------------------------
//
// These are pinned by UUID input so that the Go, Rust, TypeScript, Python,
// and PHP SDK tests can all assert the same wire outputs (tlc T-0753).
// The expected strings were generated from the upstream library and
// re-verified by Parse below.

type fixture struct {
	prefix string
	uuid   string
	want   string
}

var fixtures = []fixture{
	{"task", "01940000-0000-7000-8000-000000000000", "task_01jg000000e008000000000000"},
	{"invoice", "01940000-0000-7000-8000-000000000001", "invoice_01jg000000e008000000000001"},
	{"user", "01940000-0000-7000-8000-0000000000ff", "user_01jg000000e00800000000007z"},
}

// --- Prefixers used across the tests ---------------------------------------

type taskPrefix struct{}

func (taskPrefix) Prefix() string { return "task" }

type invoicePrefix struct{}

func (invoicePrefix) Prefix() string { return "invoice" }

// --- New / MustNew / Parse round-trip --------------------------------------

func TestNew_RoundTrip(t *testing.T) {
	s, err := id.New("task")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !strings.HasPrefix(s, "task_") {
		t.Fatalf("expected task_ prefix, got %q", s)
	}
	parsed, err := id.Parse(s)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if parsed.Prefix != "task" {
		t.Errorf("Prefix: want task, got %q", parsed.Prefix)
	}
	if parsed.UUID == (uuid.UUID{}) {
		t.Error("expected non-zero uuid")
	}
}

func TestMustNew_Panics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("MustNew with invalid prefix should panic")
		}
	}()
	_ = id.MustNew("BAD_UPPER")
}

func TestParse_Fixtures(t *testing.T) {
	for _, f := range fixtures {
		t.Run(f.prefix, func(t *testing.T) {
			parsed, err := id.Parse(f.want)
			if err != nil {
				t.Fatalf("Parse(%q): %v", f.want, err)
			}
			if parsed.Prefix != f.prefix {
				t.Errorf("Prefix: want %q, got %q", f.prefix, parsed.Prefix)
			}
			wantUUID, err := uuid.Parse(f.uuid)
			if err != nil {
				t.Fatalf("uuid.Parse fixture: %v", err)
			}
			if parsed.UUID != wantUUID {
				t.Errorf("UUID: want %s, got %s", wantUUID, parsed.UUID)
			}
		})
	}
}

// TestFixtures_GeneratedFromLib re-derives the expected strings by feeding
// the pinned UUID inputs through the upstream library. This locks the
// fixture table to upstream behavior: if the lib ever changes its encoding,
// this test fails loudly before the other SDKs drift.
func TestFixtures_GeneratedFromLib(t *testing.T) {
	for _, f := range fixtures {
		t.Run(f.prefix, func(t *testing.T) {
			tid, err := typeid.FromUUIDWithPrefix(f.prefix, f.uuid)
			if err != nil {
				t.Fatalf("upstream FromUUIDWithPrefix: %v", err)
			}
			if got := tid.String(); got != f.want {
				t.Errorf("upstream canonical: want %q, got %q", f.want, got)
			}
		})
	}
}

// --- Invalid input ---------------------------------------------------------

func TestNew_InvalidPrefix(t *testing.T) {
	cases := []string{
		"BAD_UPPER",             // uppercase
		"1abc",                  // starts with digit
		"_abc",                  // starts with underscore
		"abc_",                  // ends with underscore
		strings.Repeat("a", 64), // > 63 chars
	}
	for _, p := range cases {
		t.Run(p, func(t *testing.T) {
			if _, err := id.New(p); err == nil {
				t.Errorf("New(%q): expected error, got nil", p)
			}
		})
	}
}

func TestParse_InvalidSuffix(t *testing.T) {
	cases := []string{
		"task_short",                        // too short
		"task_01jg000000e00800000000000000", // too long
		"task_01jg000000e008000000000000!!", // bad chars
		"task_",                             // empty suffix
	}
	for _, s := range cases {
		t.Run(s, func(t *testing.T) {
			if _, err := id.Parse(s); err == nil {
				t.Errorf("Parse(%q): expected error, got nil", s)
			}
		})
	}
}

// --- Typed[T] generic newtype ----------------------------------------------

func TestTyped_NewAndPrefix(t *testing.T) {
	tid, err := id.NewTyped[taskPrefix]()
	if err != nil {
		t.Fatalf("NewTyped: %v", err)
	}
	if tid.Prefix() != "task" {
		t.Errorf("Prefix: want task, got %q", tid.Prefix())
	}
	if !strings.HasPrefix(tid.String(), "task_") {
		t.Errorf("String: want task_ prefix, got %q", tid.String())
	}
	if tid.IsZero() {
		t.Error("freshly generated typed id should not be zero")
	}
}

func TestTyped_MustNew(t *testing.T) {
	// Should not panic.
	tid := id.MustNewTyped[taskPrefix]()
	if tid.Prefix() != "task" {
		t.Fatalf("expected task, got %q", tid.Prefix())
	}
}

func TestTyped_ParseRejectsWrongPrefix(t *testing.T) {
	// invoice_… should not parse as a task-typed id.
	_, err := id.ParseTyped[taskPrefix]("invoice_01jg000000e008000000000001")
	if err == nil {
		t.Fatal("expected prefix-mismatch error")
	}
	if !strings.Contains(err.Error(), "prefix mismatch") {
		t.Errorf("expected prefix mismatch error, got %v", err)
	}
}

func TestTyped_UUID(t *testing.T) {
	tid, err := id.ParseTyped[taskPrefix](fixtures[0].want)
	if err != nil {
		t.Fatalf("ParseTyped: %v", err)
	}
	got, err := tid.UUID()
	if err != nil {
		t.Fatalf("UUID: %v", err)
	}
	want, _ := uuid.Parse(fixtures[0].uuid)
	if got != want {
		t.Errorf("UUID: want %s, got %s", want, got)
	}
}

// --- JSON round-trip -------------------------------------------------------

func TestTyped_JSONRoundTrip(t *testing.T) {
	type envelope struct {
		ID id.Typed[taskPrefix] `json:"id"`
	}

	original, err := id.ParseTyped[taskPrefix](fixtures[0].want)
	if err != nil {
		t.Fatalf("ParseTyped: %v", err)
	}
	env := envelope{ID: original}

	b, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// Wire form must be the bare canonical string, not a {prefix,uuid} object.
	wantJSON := `{"id":"` + fixtures[0].want + `"}`
	if string(b) != wantJSON {
		t.Errorf("MarshalJSON:\n got %s\nwant %s", b, wantJSON)
	}

	var back envelope
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if back.ID.String() != original.String() {
		t.Errorf("round-trip mismatch:\n got %s\nwant %s", back.ID, original)
	}
}

func TestTyped_JSONRejectsWrongPrefix(t *testing.T) {
	var dest id.Typed[taskPrefix]
	err := json.Unmarshal([]byte(`"invoice_01jg000000e008000000000001"`), &dest)
	if err == nil {
		t.Fatal("expected prefix mismatch on unmarshal")
	}
}

func TestTyped_JSONRejectsNonString(t *testing.T) {
	var dest id.Typed[taskPrefix]
	err := json.Unmarshal([]byte(`{"prefix":"task","uuid":"…"}`), &dest)
	if err == nil {
		t.Fatal("expected error on object form (the kit wire form is a bare string)")
	}
}

// --- Sort stability under UUIDv7 timestamps --------------------------------

// TestSortStability_UUIDv7 confirms that successive [New] calls produce
// lexicographically sortable strings, mirroring the K-sortable property of
// UUIDv7. We generate batches with small delays so timestamps differ and
// assert that lexical sort agrees with insertion order.
func TestSortStability_UUIDv7(t *testing.T) {
	const n = 50
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		s, err := id.New("task")
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		ids = append(ids, s)
		// 1ms sleep is enough granularity for UUIDv7's millisecond timestamp.
		time.Sleep(1 * time.Millisecond)
	}

	sorted := make([]string, len(ids))
	copy(sorted, ids)
	sort.Strings(sorted)

	for i := range ids {
		if ids[i] != sorted[i] {
			t.Fatalf("sort instability at index %d: got %q, want %q (full: %v)", i, sorted[i], ids[i], ids)
		}
	}
}

func TestTyped_Zero(t *testing.T) {
	var z id.Typed[invoicePrefix]
	if !z.IsZero() {
		t.Error("expected zero Typed[T] to report IsZero")
	}
	if got, want := z.Prefix(), "invoice"; got != want {
		t.Errorf("zero Prefix: want %q, got %q", want, got)
	}
	if !strings.HasPrefix(z.String(), "invoice_") {
		t.Errorf("zero String: want invoice_ prefix, got %q", z.String())
	}
}
