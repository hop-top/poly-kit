package bus

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"

	"hop.top/kit/go/core/id"
)

// Runs the shared corpus in contracts/bus-v1. The vectors are the
// source of truth: a failure here means this implementation drifted
// from the contract, not that the vector should be edited to fit.

// confFiles is every vector file this runner consumes. The corpus
// directory must match it exactly, so a new file cannot go unrun.
var confFiles = map[string]bool{
	"builder.json":          true,
	"parse_topic.json":      true,
	"match.json":            true,
	"validate.json":         true,
	"validate_topic.json":   true,
	"prefix_topics.json":    true,
	"qualifiers.json":       true,
	"mode_parse.json":       true,
	"event_json.json":       true,
	"event_timestamp.json":  true,
	"event_extensions.json": true,
	"event_id.json":         true,
}

const confDir = "../../../contracts/bus-v1"

// confHeader holds the fields every vector file carries.
type confHeader struct {
	Schema      string `json:"$schema"`
	ID          string `json:"$id"`
	Version     string `json:"version"`
	Description string `json:"description"`
}

func (h confHeader) check() error {
	if h.Version != "v1" {
		return fmt.Errorf("version = %q, want v1", h.Version)
	}
	return nil
}

type confChecker interface{ check() error }

// confLoad decodes a vector file strictly: unknown fields, an
// unregistered file or a version other than v1 fail the test.
func confLoad(t *testing.T, name string, v confChecker) {
	t.Helper()
	if !confFiles[name] {
		t.Fatalf("%s is not registered in confFiles", name)
	}
	p := filepath.Join(filepath.FromSlash(confDir), name)
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("parse %s: %v", p, err)
	}
	if err := v.check(); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

func TestConformanceAllFilesConsumed(t *testing.T) {
	entries, err := os.ReadDir(filepath.FromSlash(confDir))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		seen[e.Name()] = true
		if !confFiles[e.Name()] {
			t.Errorf("%s has no runner", e.Name())
		}
	}
	for n := range confFiles {
		if !seen[n] {
			t.Errorf("%s registered but missing from the corpus", n)
		}
	}
}

func TestConformanceMatch(t *testing.T) {
	var f struct {
		confHeader
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
	case !ok && want == "" && len(contains) == 0:
		t.Errorf("%s: failing case has neither error nor error_contains", name)
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
		confHeader
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
		confHeader
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
		confHeader
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
		confHeader
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

func confQ(t *testing.T, m map[string]string) Qualifiers {
	t.Helper()
	for k := range m {
		switch k {
		case "reason", "mechanism", "property", "circumstance":
		default:
			t.Fatalf("unknown qualifier key %q", k)
		}
	}
	return Qualifiers{m["reason"], m["mechanism"], m["property"], m["circumstance"]}
}

func TestConformanceQualifiers(t *testing.T) {
	var f struct {
		confHeader
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
		q := confQ(t, c.Qualifiers)
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
		q := confQ(t, c.Qualifiers)
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

func TestConformanceEventJSON(t *testing.T) {
	var f struct {
		confHeader
		Cases []struct {
			Name      string         `json:"name"`
			Mode      string         `json:"mode"`
			Event     map[string]any `json:"event"`
			WireExtra map[string]any `json:"wire_extra"`
			Text      string         `json:"text"`
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
	known := map[string]bool{
		"topic": true, "source": true, "timestamp": true, "payload": true,
		"workspace_id": true, "id": true, "subject": true,
		"datacontenttype": true, "dataschema": true, "extensions": true,
	}
	for _, c := range f.Cases {
		for k := range c.Event {
			if !known[k] {
				t.Fatalf("%s: unknown event key %q", c.Name, k)
			}
		}
		if c.Mode != "roundtrip" && c.Mode != "decode" {
			t.Fatalf("%s: unknown mode %q", c.Name, c.Mode)
		}
		if c.Mode == "roundtrip" && c.WireExtra != nil {
			t.Fatalf("%s: wire_extra needs mode decode", c.Name)
		}
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
		enc, err := json.Marshal(dec)
		if err != nil {
			t.Errorf("%s: encode: %v", c.Name, err)
			continue
		}
		var got map[string]any
		_ = json.Unmarshal(enc, &got)
		if !reflect.DeepEqual(got, c.Event) {
			t.Errorf("%s: encoded %s, want %v", c.Name, enc, c.Event)
		}
		if c.Text != "" && string(enc) != c.Text {
			t.Errorf("%s: encoded text\n got  %s\n want %s", c.Name, enc, c.Text)
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

func TestConformanceEventTimestamp(t *testing.T) {
	var f struct {
		confHeader
		Cases []struct {
			Name      string `json:"name"`
			Timestamp string `json:"timestamp"`
			Canonical string `json:"canonical"`
		} `json:"cases"`
		Reject []struct {
			Name          string `json:"name"`
			Timestamp     any    `json:"timestamp"`
			ErrorContains string `json:"error_contains"`
		} `json:"reject"`
		Defaults []struct {
			Name      string         `json:"name"`
			Event     map[string]any `json:"event"`
			Timestamp string         `json:"timestamp"`
		} `json:"defaults"`
	}
	confLoad(t, "event_timestamp.json", &f)
	if len(f.Cases) == 0 || len(f.Reject) == 0 || len(f.Defaults) == 0 {
		t.Fatal("no cases")
	}
	decode := func(wire map[string]any) (Event, error) {
		raw, _ := json.Marshal(wire)
		var e Event
		return e, json.Unmarshal(raw, &e)
	}
	stamp := func(e Event) string {
		b, err := json.Marshal(e.Timestamp)
		if err != nil {
			return "marshal: " + err.Error()
		}
		return strings.Trim(string(b), `"`)
	}
	for _, c := range f.Cases {
		e, err := decode(map[string]any{"timestamp": c.Timestamp})
		if err != nil {
			t.Errorf("%s: decode: %v", c.Name, err)
			continue
		}
		if got := stamp(e); got != c.Canonical {
			t.Errorf("%s: re-encoded %q, want %q", c.Name, got, c.Canonical)
		}
	}
	for _, c := range f.Reject {
		_, err := decode(map[string]any{"timestamp": c.Timestamp})
		if err == nil {
			t.Errorf("%s: decode succeeded, want error", c.Name)
		} else if !strings.Contains(err.Error(), c.ErrorContains) {
			t.Errorf("%s: error %q lacks %q", c.Name, err, c.ErrorContains)
		}
	}
	for _, c := range f.Defaults {
		e, err := decode(c.Event)
		if err != nil {
			t.Errorf("%s: decode: %v", c.Name, err)
			continue
		}
		if got := stamp(e); got != c.Timestamp {
			t.Errorf("%s: timestamp %q, want %q", c.Name, got, c.Timestamp)
		}
	}
}

func TestConformanceEventExtensions(t *testing.T) {
	var f struct {
		confHeader
		Cases []struct {
			Name        string          `json:"name"`
			WorkspaceID string          `json:"workspace_id"`
			Extensions  json.RawMessage `json:"extensions"`
			Error       string          `json:"error"`
		} `json:"cases"`
	}
	confLoad(t, "event_extensions.json", &f)
	if len(f.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range f.Cases {
		// json.Number keeps integer literals exact, as the network layer does.
		dec := json.NewDecoder(bytes.NewReader(c.Extensions))
		dec.UseNumber()
		var ext map[string]any
		if err := dec.Decode(&ext); err != nil {
			t.Fatalf("%s: extensions: %v", c.Name, err)
		}
		_, err := ToCloudEvent(Event{
			Topic: "a.b.c.d", Source: "s", WorkspaceID: c.WorkspaceID, Extensions: ext,
		})
		switch {
		case c.Error == "" && err != nil:
			t.Errorf("%s: unexpected error %q", c.Name, err)
		case c.Error != "" && err == nil:
			t.Errorf("%s: want error %q, got none", c.Name, c.Error)
		case c.Error != "" && err.Error() != c.Error:
			t.Errorf("%s:\n got  %q\n want %q", c.Name, err.Error(), c.Error)
		}
	}
}

func TestConformanceEventID(t *testing.T) {
	var f struct {
		confHeader
		Prefix       string `json:"prefix"`
		SuffixLength int    `json:"suffix_length"`
		Alphabet     string `json:"alphabet"`
		FirstCharMax string `json:"first_char_max"`
		UUIDVersion  int    `json:"uuid_version"`
		Distinct     int    `json:"distinct"`
	}
	confLoad(t, "event_id.json", &f)
	check := func(label, s string) {
		if !strings.HasPrefix(s, f.Prefix) || len(s) != len(f.Prefix)+f.SuffixLength {
			t.Errorf("%s: %q is not %s + %d chars", label, s, f.Prefix, f.SuffixLength)
			return
		}
		suffix := s[len(f.Prefix):]
		for _, r := range suffix {
			if !strings.ContainsRune(f.Alphabet, r) {
				t.Errorf("%s: %q has %q outside the alphabet", label, s, r)
				return
			}
		}
		if suffix[:1] > f.FirstCharMax {
			t.Errorf("%s: %q first char above %q", label, s, f.FirstCharMax)
		}
		p, err := id.Parse(s)
		if err != nil {
			t.Errorf("%s: %q does not parse: %v", label, s, err)
			return
		}
		if int(p.UUID.Version()) != f.UUIDVersion || p.UUID.Variant() != uuid.RFC4122 {
			t.Errorf("%s: %q uuid version %d variant %v", label, s, p.UUID.Version(), p.UUID.Variant())
		}
	}
	seen := map[string]bool{}
	for i := 0; i < f.Distinct; i++ {
		e := NewEvent("a.b.c.d", "s", nil)
		check("NewEvent", e.ID)
		if seen[e.ID] {
			t.Errorf("duplicate id %q", e.ID)
		}
		seen[e.ID] = true
	}
	c, err := ToCloudEvent(Event{Topic: "a.b.c.d", Source: "s"})
	if err != nil {
		t.Fatal(err)
	}
	check("ToCloudEvent", c.ID)
}

// confPanicText runs f and returns the text of the error it panics
// with, or "" when it returns normally.
func confPanicText(f func()) (msg string) {
	defer func() {
		if r := recover(); r != nil {
			if err, ok := r.(error); ok {
				msg = err.Error()
			} else {
				msg = fmt.Sprint(r)
			}
		}
	}()
	f()
	return ""
}

// confCheckPanic is confCheckErr for a render that panics: a non-empty
// panic text is the error.
func confCheckPanic(t *testing.T, name string, ok bool, want string, contains []string, text string) {
	t.Helper()
	var err error
	if text != "" {
		err = errors.New(text)
	}
	confCheckErr(t, name, ok, want, contains, err)
}

func TestConformanceBuilder(t *testing.T) {
	var f struct {
		confHeader
		Cases []struct {
			Name          string   `json:"name"`
			Source        string   `json:"source"`
			Category      string   `json:"category"`
			Object        string   `json:"object"`
			CtorModifiers []string `json:"ctor_modifiers"`
			Mods          []string `json:"mods"`
			Action        string   `json:"action"`
			OK            bool     `json:"ok"`
			Topic         string   `json:"topic"`
			Error         string   `json:"error"`
			ErrorContains []string `json:"error_contains"`
		} `json:"cases"`
	}
	confLoad(t, "builder.json", &f)
	if len(f.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range f.Cases {
		b := PrefixedTopicOf(c.Source, c.Category, c.Object, c.CtorModifiers...)
		for _, m := range c.Mods {
			b = b.Mod(m)
		}
		var got Topic
		text := confPanicText(func() { got = b.Action(c.Action) })
		confCheckPanic(t, c.Name, c.OK, c.Error, c.ErrorContains, text)
		if c.OK && string(got) != c.Topic {
			t.Errorf("%s: topic = %q, want %q", c.Name, got, c.Topic)
		}
	}
}

func TestConformanceParseTopic(t *testing.T) {
	var f struct {
		confHeader
		Cases []struct {
			Name          string   `json:"name"`
			Topic         string   `json:"topic"`
			OK            bool     `json:"ok"`
			Source        string   `json:"source"`
			Category      string   `json:"category"`
			Object        string   `json:"object"`
			Modifier      string   `json:"modifier"`
			Action        string   `json:"action"`
			Rerender      string   `json:"rerender"`
			RerenderError string   `json:"rerender_error"`
			Error         string   `json:"error"`
			ErrorContains []string `json:"error_contains"`
		} `json:"cases"`
	}
	confLoad(t, "parse_topic.json", &f)
	if len(f.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range f.Cases {
		b, action, err := ParseTopic(c.Topic)
		confCheckErr(t, c.Name, c.OK, c.Error, c.ErrorContains, err)
		if !c.OK {
			var ite *InvalidTopicError
			if !errors.As(err, &ite) || string(ite.Topic) != c.Topic {
				t.Errorf("%s: error does not carry the topic: %v", c.Name, err)
			}
			continue
		}
		if b.SourceSeg() != c.Source || b.CategorySeg() != c.Category ||
			b.ObjectSeg() != c.Object || b.ModifierSeg() != c.Modifier || action != c.Action {
			t.Errorf("%s: got %q %q %q %q action %q", c.Name,
				b.SourceSeg(), b.CategorySeg(), b.ObjectSeg(), b.ModifierSeg(), action)
		}
		var re Topic
		text := confPanicText(func() { re = b.Action(action) })
		switch {
		case c.RerenderError != "" && text != c.RerenderError:
			t.Errorf("%s: rerender panic = %q, want %q", c.Name, text, c.RerenderError)
		case c.RerenderError == "" && (text != "" || string(re) != c.Rerender):
			t.Errorf("%s: rerender = %q (panic %q), want %q", c.Name, re, text, c.Rerender)
		}
	}
}
