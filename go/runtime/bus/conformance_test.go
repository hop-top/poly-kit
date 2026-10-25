package bus

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Runs the shared corpus in contracts/bus-v1. The vectors are the
// source of truth: a failure here means this implementation drifted
// from the contract, not that the vector should be edited to fit.

func confLoad(t *testing.T, name string, v any) {
	t.Helper()
	p := filepath.Join("..", "..", "..", "contracts", "bus-v1", name)
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("parse %s: %v", p, err)
	}
}

func TestConformanceMatch(t *testing.T) {
	var f struct {
		Cases []struct {
			Name    string `json:"name"`
			Pattern string `json:"pattern"`
			Topic   string `json:"topic"`
			Match   bool   `json:"match"`
		} `json:"cases"`
	}
	confLoad(t, "match.json", &f)
	if len(f.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range f.Cases {
		if got := Topic(c.Topic).Match(c.Pattern); got != c.Match {
			t.Errorf("%s: Match = %v, want %v", c.Name, got, c.Match)
		}
	}
}

type confValidateCase struct {
	Name  string `json:"name"`
	Topic string `json:"topic"`
	OK    bool   `json:"ok"`
	Error string `json:"error"`
	// ErrorContains replaces Error when the exact text names a
	// language-specific identifier; see the corpus README.
	ErrorContains []string `json:"error_contains"`
}

// confCheckErr asserts err against a case: exact text when the vector has
// one, else every error_contains substring.
func confCheckErr(t *testing.T, name string, ok bool, want string, contains []string, err error) {
	t.Helper()
	switch {
	case ok && err != nil:
		t.Errorf("%s: unexpected error %q", name, err)
	case !ok && err == nil:
		t.Errorf("%s: want error %q %v, got nil", name, want, contains)
	case !ok && want != "" && err.Error() != want:
		t.Errorf("%s:\n got  %q\n want %q", name, err.Error(), want)
	case !ok && want == "":
		for _, sub := range contains {
			if !strings.Contains(err.Error(), sub) {
				t.Errorf("%s: error %q lacks %q", name, err.Error(), sub)
			}
		}
	}
}

func confRunValidate(t *testing.T, file string, fn func(Topic) error) {
	t.Helper()
	var f struct {
		Cases []confValidateCase `json:"cases"`
	}
	confLoad(t, file, &f)
	if len(f.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range f.Cases {
		confCheckErr(t, c.Name, c.OK, c.Error, c.ErrorContains, fn(Topic(c.Topic)))
	}
}

func TestConformanceValidate(t *testing.T) {
	confRunValidate(t, "validate.json", Validate)
}

func TestConformanceValidateIsInvalidTopic(t *testing.T) {
	var f struct {
		Cases []confValidateCase `json:"cases"`
	}
	confLoad(t, "validate.json", &f)
	for _, c := range f.Cases {
		if c.OK {
			continue
		}
		var ite *InvalidTopicError
		err := Validate(Topic(c.Topic))
		if !errors.Is(err, ErrInvalidTopic) || !errors.As(err, &ite) || string(ite.Topic) != c.Topic {
			t.Errorf("%s: error does not wrap ErrInvalidTopic for the topic: %v", c.Name, err)
		}
	}
}

func TestConformanceValidateTopic(t *testing.T) {
	confRunValidate(t, "validate_topic.json", ValidateTopic)
}

func TestConformancePrefixTopics(t *testing.T) {
	var f struct {
		Cases []struct {
			Name    string            `json:"name"`
			Prefix  string            `json:"prefix"`
			Actions []string          `json:"actions"`
			Topics  map[string]string `json:"topics"`
			OK      bool              `json:"ok"`
			Error   string            `json:"error"`

			ErrorContains []string `json:"error_contains"`
		} `json:"cases"`
	}
	confLoad(t, "prefix_topics.json", &f)
	if len(f.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range f.Cases {
		got, err := PrefixTopics(c.Prefix, c.Actions)
		confCheckErr(t, c.Name, c.OK, c.Error, c.ErrorContains, err)
		gotM := map[string]string{}
		for k, v := range got {
			gotM[k] = string(v)
		}
		if !reflect.DeepEqual(gotM, c.Topics) {
			t.Errorf("%s: map = %v, want %v", c.Name, gotM, c.Topics)
		}
	}
}

func TestConformanceModeParse(t *testing.T) {
	var f struct {
		Display map[string]string `json:"display"`
		Cases   []struct {
			Name  string `json:"name"`
			Input string `json:"input"`
			Mode  string `json:"mode"`
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		} `json:"cases"`
	}
	confLoad(t, "mode_parse.json", &f)
	if len(f.Cases) == 0 || len(f.Display) != 3 {
		t.Fatal("corpus incomplete")
	}
	modes := map[string]Mode{"off": ModeOff, "warn": ModeWarn, "strict": ModeStrict}
	for name, m := range modes {
		if m.String() != f.Display[name] {
			t.Errorf("String(%s) = %q, want %q", name, m.String(), f.Display[name])
		}
	}
	for _, c := range f.Cases {
		got, err := ModeFromString(c.Input)
		if got != modes[c.Mode] {
			t.Errorf("%s: mode = %v, want %s", c.Name, got, c.Mode)
		}
		switch {
		case c.OK && err != nil:
			t.Errorf("%s: unexpected error %q", c.Name, err)
		case !c.OK && err == nil:
			t.Errorf("%s: want error %q, got nil", c.Name, c.Error)
		case !c.OK && err.Error() != c.Error:
			t.Errorf("%s:\n got  %q\n want %q", c.Name, err.Error(), c.Error)
		}
	}
}

type confSnapshot struct {
	SnapshotID string `json:"snapshot_id"`
}

type confEmbedded struct {
	Qualifiers
	confSnapshot
}

type confNamed struct {
	Q          Qualifiers `json:"qualifiers"`
	SnapshotID string     `json:"snapshot_id"`
}

type confNone struct {
	SnapshotID string `json:"snapshot_id"`
}

func confQ(m map[string]string) Qualifiers {
	return Qualifiers{m["reason"], m["mechanism"], m["property"], m["circumstance"]}
}

func TestConformanceQualifiers(t *testing.T) {
	var f struct {
		Serialize []struct {
			Name       string            `json:"name"`
			Qualifiers map[string]string `json:"qualifiers"`
			JSON       map[string]any    `json:"json"`
			IsZero     bool              `json:"is_zero"`
		} `json:"serialize"`
		Extract []struct {
			Name       string            `json:"name"`
			Shape      string            `json:"shape"`
			Qualifiers map[string]string `json:"qualifiers"`
			Found      bool              `json:"found"`
		} `json:"extract"`
	}
	confLoad(t, "qualifiers.json", &f)
	if len(f.Serialize) == 0 || len(f.Extract) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range f.Serialize {
		q := confQ(c.Qualifiers)
		raw, err := json.Marshal(q)
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		_ = json.Unmarshal(raw, &got)
		if !reflect.DeepEqual(got, c.JSON) {
			t.Errorf("%s: json = %s, want %v", c.Name, raw, c.JSON)
		}
		if q.IsZero() != c.IsZero {
			t.Errorf("%s: IsZero = %v, want %v", c.Name, q.IsZero(), c.IsZero)
		}
	}
	for _, c := range f.Extract {
		q := confQ(c.Qualifiers)
		var payload any
		switch c.Shape {
		case "embedded":
			payload = confEmbedded{Qualifiers: q}
		case "pointer_embedded":
			payload = &confEmbedded{Qualifiers: q}
		case "named":
			payload = confNamed{Q: q}
		case "none":
			payload = confNone{}
		case "nil":
			payload = nil
		case "scalar":
			payload = "x"
		case "array":
			payload = []any{}
		default:
			t.Fatalf("%s: unknown shape %q", c.Name, c.Shape)
		}
		got, ok := QualifiersFrom(payload)
		if ok != c.Found {
			t.Errorf("%s: found = %v, want %v", c.Name, ok, c.Found)
		}
		want := Qualifiers{}
		if c.Found {
			want = q
		}
		if got != want {
			t.Errorf("%s: qualifiers = %+v, want %+v", c.Name, got, want)
		}
	}
}

type confWire struct {
	Topic       string `json:"topic"`
	Source      string `json:"source"`
	Timestamp   string `json:"timestamp"`
	Payload     any    `json:"payload"`
	WorkspaceID string `json:"workspace_id"`
}

func TestConformanceEventJSON(t *testing.T) {
	var f struct {
		Cases []struct {
			Name      string         `json:"name"`
			Mode      string         `json:"mode"`
			Event     map[string]any `json:"event"`
			WireExtra map[string]any `json:"wire_extra"`
		} `json:"cases"`
		Reject []struct {
			Name  string         `json:"name"`
			Event map[string]any `json:"event"`
		} `json:"decode_reject"`
	}
	confLoad(t, "event_json.json", &f)
	if len(f.Cases) == 0 || len(f.Reject) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range f.Cases {
		var w confWire
		raw, _ := json.Marshal(c.Event)
		if err := json.Unmarshal(raw, &w); err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		ts, err := time.Parse(time.RFC3339Nano, w.Timestamp)
		if err != nil {
			t.Fatalf("%s: bad vector timestamp: %v", c.Name, err)
		}
		want := Event{Topic: Topic(w.Topic), Source: w.Source, Timestamp: ts, Payload: w.Payload, WorkspaceID: w.WorkspaceID}

		wire := map[string]any{}
		for k, v := range c.Event {
			wire[k] = v
		}
		for k, v := range c.WireExtra {
			wire[k] = v
		}
		wireRaw, _ := json.Marshal(wire)
		var dec Event
		if err := json.Unmarshal(wireRaw, &dec); err != nil {
			t.Errorf("%s: decode: %v", c.Name, err)
			continue
		}
		if dec.Topic != want.Topic || dec.Source != want.Source || dec.WorkspaceID != want.WorkspaceID ||
			!dec.Timestamp.Equal(ts) || !reflect.DeepEqual(dec.Payload, want.Payload) {
			t.Errorf("%s: decoded %+v, want %+v", c.Name, dec, want)
		}
		if c.Mode != "roundtrip" {
			continue
		}
		enc, err := json.Marshal(want)
		if err != nil {
			t.Errorf("%s: encode: %v", c.Name, err)
			continue
		}
		var got map[string]any
		_ = json.Unmarshal(enc, &got)
		if !reflect.DeepEqual(got, c.Event) {
			t.Errorf("%s: encoded %s, want %v", c.Name, enc, c.Event)
		}
		for k := range got {
			if k != strings.ToLower(k) {
				t.Errorf("%s: non-lowercase key %q", c.Name, k)
			}
		}
	}
	for _, c := range f.Reject {
		raw, _ := json.Marshal(c.Event)
		var dec Event
		if err := json.Unmarshal(raw, &dec); err == nil {
			t.Errorf("%s: decode succeeded, want error", c.Name)
		}
	}
}
