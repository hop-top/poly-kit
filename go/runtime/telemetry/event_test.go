package telemetry

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"
)

// validInstallIDHex is a stable 64-char lowercase hex digest used as
// a happy-path InstallationID in tests. Any 64 lowercase hex chars
// would work; this one is the SHA-256 of zero bytes for reproducibility.
const validInstallIDHex = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

func newAnonEvent() Event {
	return Event{
		SchemaVersion:  SchemaVersion,
		SDKLang:        SDKLang,
		SDKVersion:     "0.4.0",
		InstallationID: validInstallIDHex,
		Mode:           "anon",
		CommandPath:    []string{"kit", "hop", "list"},
		ExitCode:       0,
		DurationMS:     42,
		OccurredAt:     time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC),
		KitVersion:     "0.4.0",
	}
}

func newFullEvent() Event {
	e := newAnonEvent()
	e.Mode = "full"
	e.Args = []string{"--flag", "value"}
	e.Flags = map[string]string{"--key": "value"}
	return e
}

func TestSchemaVersionConstant(t *testing.T) {
	if SchemaVersion != "1" {
		t.Fatalf("SchemaVersion = %q, want %q (pinned string \"1\")", SchemaVersion, "1")
	}
}

func TestSDKLangConstant(t *testing.T) {
	if SDKLang != "go" {
		t.Fatalf("SDKLang = %q, want %q", SDKLang, "go")
	}
}

func TestEventValidate_HappyAnon(t *testing.T) {
	if err := newAnonEvent().Validate(); err != nil {
		t.Fatalf("anon event must validate, got: %v", err)
	}
}

func TestEventValidate_HappyFull(t *testing.T) {
	if err := newFullEvent().Validate(); err != nil {
		t.Fatalf("full event must validate, got: %v", err)
	}
}

func TestEventValidate_BadSchemaVersion(t *testing.T) {
	e := newAnonEvent()
	e.SchemaVersion = "2"
	err := e.Validate()
	if !errors.Is(err, ErrSchemaVersion) {
		t.Fatalf("err = %v, want errors.Is(.., ErrSchemaVersion)", err)
	}
}

func TestEventValidate_EmptyInstallID(t *testing.T) {
	e := newAnonEvent()
	e.InstallationID = ""
	err := e.Validate()
	if !errors.Is(err, ErrInstallID) {
		t.Fatalf("err = %v, want errors.Is(.., ErrInstallID)", err)
	}
}

func TestEventValidate_BadInstallIDLength(t *testing.T) {
	e := newAnonEvent()
	// half-length hex — must reject
	e.InstallationID = validInstallIDHex[:32]
	err := e.Validate()
	if !errors.Is(err, ErrInstallID) {
		t.Fatalf("err = %v, want errors.Is(.., ErrInstallID)", err)
	}
}

func TestEventValidate_BadMode(t *testing.T) {
	e := newAnonEvent()
	e.Mode = "off"
	err := e.Validate()
	if !errors.Is(err, ErrMode) {
		t.Fatalf("err = %v, want errors.Is(.., ErrMode)", err)
	}
}

func TestEventValidate_EmptyCommandPath(t *testing.T) {
	e := newAnonEvent()
	e.CommandPath = nil
	err := e.Validate()
	if !errors.Is(err, ErrCommandPath) {
		t.Fatalf("err = %v, want errors.Is(.., ErrCommandPath)", err)
	}
}

func TestEventValidate_ZeroOccurredAt(t *testing.T) {
	e := newAnonEvent()
	e.OccurredAt = time.Time{}
	err := e.Validate()
	if !errors.Is(err, ErrOccurredAt) {
		t.Fatalf("err = %v, want errors.Is(.., ErrOccurredAt)", err)
	}
}

func TestEventValidate_EmptySDKLang(t *testing.T) {
	e := newAnonEvent()
	e.SDKLang = ""
	err := e.Validate()
	if !errors.Is(err, ErrSDKLang) {
		t.Fatalf("err = %v, want errors.Is(.., ErrSDKLang)", err)
	}
}

func TestEventJSONShape_Anon(t *testing.T) {
	e := newAnonEvent()
	buf, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	required := map[string]struct{}{
		"schema_version":  {},
		"sdk_lang":        {},
		"installation_id": {},
		"mode":            {},
		"command_path":    {},
		"exit_code":       {},
		"duration_ms":     {},
		"occurred_at":     {},
	}
	allowedOptional := map[string]struct{}{
		"sdk_version": {},
		"kit_version": {},
		"trace_id":    {},
	}
	for k := range required {
		if _, ok := got[k]; !ok {
			t.Errorf("anon event missing required key %q", k)
		}
	}
	for k := range got {
		if _, isRequired := required[k]; isRequired {
			continue
		}
		if _, isOptional := allowedOptional[k]; isOptional {
			continue
		}
		t.Errorf("anon event has unexpected key %q (must omitempty)", k)
	}
	if _, ok := got["args"]; ok {
		t.Errorf("anon event must omit args; got args = %v", got["args"])
	}
	if _, ok := got["flags"]; ok {
		t.Errorf("anon event must omit flags; got flags = %v", got["flags"])
	}
}

func TestEventJSONShape_Full(t *testing.T) {
	e := newFullEvent()
	buf, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, k := range []string{"args", "flags"} {
		if _, ok := got[k]; !ok {
			keys := make([]string, 0, len(got))
			for k := range got {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			t.Errorf("full event missing key %q (got keys: %v)", k, keys)
		}
	}
}

func TestEventJSONTimestampKey(t *testing.T) {
	buf, err := json.Marshal(newAnonEvent())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(buf)
	if !strings.Contains(s, `"occurred_at"`) {
		t.Errorf("expected JSON to contain \"occurred_at\", got: %s", s)
	}
	// Regression guard: the pre-reconciliation field name was
	// "timestamp"; the pinned key is occurred_at. Make sure we never
	// regress to the old key.
	if strings.Contains(s, `"timestamp"`) {
		t.Errorf("JSON must NOT contain \"timestamp\" key, got: %s", s)
	}
}
