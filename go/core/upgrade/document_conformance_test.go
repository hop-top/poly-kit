package upgrade

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type docConfStep struct {
	From   int         `json:"from"`
	To     int         `json:"to"`
	Note   string      `json:"note"`
	Ops    []docConfOp `json:"ops"`
	Verify *struct {
		Require string `json:"require"`
	} `json:"verify"`
}

type docConfOp struct {
	Set *struct {
		Path  string `json:"path"`
		Value any    `json:"value"`
	} `json:"set"`
	Rename *struct {
		From string `json:"from"`
		To   string `json:"to"`
	} `json:"rename"`
	Fail string `json:"fail"`
}

type docConfFile struct {
	upgradeConfHeader
	Scenarios []struct {
		Name    string          `json:"name"`
		Doc     json.RawMessage `json:"doc"`
		Text    *string         `json:"text"`
		Options struct {
			VersionField  string `json:"version_field"`
			Latest        int    `json:"latest"`
			MissingIsZero bool   `json:"missing_is_zero"`
		} `json:"options"`
		Steps  []docConfStep `json:"steps"`
		Expect struct {
			Doc    json.RawMessage `json:"doc"`
			Text   *string         `json:"text"`
			Report *DocReport      `json:"report"`
			Error  string          `json:"error"`
			Kind   string          `json:"kind"`
		} `json:"expect"`
	} `json:"scenarios"`
}

var docConfKinds = map[string]error{
	"newer": ErrDocNewer, "version": ErrDocVersion, "gap": ErrDocGap,
	"step": ErrDocStep, "verify": ErrVerify, "invalid": ErrDocInvalid,
}

func docConfLookup(doc map[string]any, path string) (any, bool) {
	var cur any = doc
	for _, seg := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[seg]; !ok {
			return nil, false
		}
	}
	return cur, true
}

func docConfSet(doc map[string]any, path string, v any) {
	segs := strings.Split(path, ".")
	cur := doc
	for _, seg := range segs[:len(segs)-1] {
		next, ok := cur[seg].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[seg] = next
		}
		cur = next
	}
	cur[segs[len(segs)-1]] = v
}

func docConfSteps(cases []docConfStep) []DocStep {
	var out []DocStep
	for _, c := range cases {
		c := c
		s := DocStep{From: c.From, To: c.To, Note: c.Note,
			// In place, on purpose: the migrator must hand steps a copy.
			Apply: func(doc map[string]any) (map[string]any, error) {
				for _, op := range c.Ops {
					switch {
					case op.Set != nil:
						docConfSet(doc, op.Set.Path, op.Set.Value)
					case op.Rename != nil:
						if v, ok := doc[op.Rename.From]; ok {
							doc[op.Rename.To] = v
							delete(doc, op.Rename.From)
						}
					case op.Fail != "":
						return nil, errors.New(op.Fail)
					}
				}
				return doc, nil
			}}
		if c.Verify != nil {
			s.Verify = func(doc map[string]any) error {
				if _, ok := docConfLookup(doc, c.Verify.Require); !ok {
					return errors.New("missing " + c.Verify.Require)
				}
				return nil
			}
		}
		out = append(out, s)
	}
	return out
}

func canonJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var x any
	if err := json.Unmarshal(raw, &x); err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(x)
	return string(out)
}

func TestConformanceUpgradeDocument(t *testing.T) {
	var f docConfFile
	loadUpgradeConf(t, "document.json", &f)
	if len(f.Scenarios) == 0 {
		t.Fatal("scenarios section is empty")
	}
	for _, sc := range f.Scenarios {
		t.Run(sc.Name, func(t *testing.T) {
			opts := DocOptions{VersionField: sc.Options.VersionField, Latest: sc.Options.Latest,
				MissingIsZero: sc.Options.MissingIsZero}
			steps := docConfSteps(sc.Steps)
			input := []byte(sc.Doc)
			if sc.Text != nil {
				input = []byte(*sc.Text)
			}
			before := append([]byte(nil), input...)
			out, report, err := MigrateDocument(input, steps, opts)
			if !bytes.Equal(input, before) {
				t.Errorf("input bytes changed")
			}
			check := func(via string, got any, report DocReport, err error) {
				if sc.Expect.Error != "" {
					if err == nil {
						t.Fatalf("%s: no error, want %q", via, sc.Expect.Error)
					}
					if err.Error() != sc.Expect.Error {
						t.Errorf("%s: error %q, want %q", via, err, sc.Expect.Error)
					}
					if kind := docConfKinds[sc.Expect.Kind]; !errors.Is(err, kind) {
						t.Errorf("%s: error %q is not kind %q", via, err, sc.Expect.Kind)
					}
					return
				}
				if err != nil {
					t.Fatalf("%s: unexpected error %v", via, err)
				}
				if sc.Expect.Doc != nil {
					var want any
					_ = json.Unmarshal(sc.Expect.Doc, &want)
					if g, w := canonJSON(t, got), canonJSON(t, want); g != w {
						t.Errorf("%s: doc = %s, want %s", via, g, w)
					}
				}
				if sc.Expect.Report != nil && !reflect.DeepEqual(report, *sc.Expect.Report) {
					t.Errorf("%s: report = %+v, want %+v", via, report, *sc.Expect.Report)
				}
			}
			var parsed any
			if err == nil {
				_ = json.Unmarshal(out, &parsed)
			}
			check("bytes", parsed, report, err)
			if sc.Expect.Text != nil && err == nil && string(out) != *sc.Expect.Text {
				t.Errorf("bytes: %q, want byte-identical %q", out, *sc.Expect.Text)
			}

			// The map form, when the input is an object: same result, and
			// the caller's map is untouched however the steps mutate.
			// Exact literals (text) only survive the bytes form.
			var doc map[string]any
			if sc.Text == nil && json.Unmarshal(input, &doc) == nil && doc != nil {
				snapshot := canonJSON(t, doc)
				got, report, err := MigrateDocumentMap(doc, steps, opts)
				if canonJSON(t, doc) != snapshot {
					t.Errorf("map: caller's document changed: %s", canonJSON(t, doc))
				}
				var g any
				if err == nil {
					g = got
				}
				check("map", g, report, err)
			}
		})
	}
}
