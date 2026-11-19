package prompt_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"hop.top/kit/go/ai/llm"
	"hop.top/kit/go/ai/prompt"
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func parse(t *testing.T, body string) *prompt.Template {
	t.Helper()
	tpl, err := prompt.ParseTemplate(prompt.Ref{Name: "p", Version: 1}, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	return tpl
}

// The fixture is flo's meeting-brief@1.yaml byte for byte. The hash and the
// golden render were produced by flo's own loader over the same vars, so a
// drift in either is a format break with flo.
const floMeetingBriefHash = "04f498ee0626ce0d8244327d0589604d37a22b3f11129e93f895997709e894ae"

func TestGoldenAgainstFlo(t *testing.T) {
	ref, err := prompt.ParseRef("meeting-brief@1")
	if err != nil {
		t.Fatal(err)
	}
	tpl, err := prompt.DirSource("testdata").Load(ref)
	if err != nil {
		t.Fatal(err)
	}
	if tpl.Hash != floMeetingBriefHash {
		t.Errorf("hash %s, flo %s", tpl.Hash, floMeetingBriefHash)
	}
	raw, err := os.ReadFile("testdata/meeting-brief@1.vars.json")
	if err != nil {
		t.Fatal(err)
	}
	var vars map[string]any
	if err := json.Unmarshal(raw, &vars); err != nil {
		t.Fatal(err)
	}
	got, err := tpl.Render(vars)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/meeting-brief@1.golden.txt")
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("render differs from flo's:\n%s", got)
	}
	if !strings.HasPrefix(tpl.System, "Write a one-page brief") {
		t.Errorf("system %q", tpl.System)
	}
}

func TestParseRef(t *testing.T) {
	for _, bad := range []string{"", "p", "p@0", "p@x", "../p@1", "a/b@1", "P@1", "p@01"} {
		if _, err := prompt.ParseRef(bad); !errors.Is(err, prompt.ErrInvalid) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	r, err := prompt.ParseRef("meeting-brief@12")
	if err != nil || r.Name != "meeting-brief" || r.Version != 12 || r.File() != "meeting-brief@12.yaml" {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestRender(t *testing.T) {
	tpl := parse(t, "system: be brief\ntemplate: |\n  {{ .who }}: {{ join \", \" .items }} / {{ trunc 3 .long }} / {{ default \"none\" .empty }}\n")
	got, err := tpl.Render(map[string]any{"who": "ann", "items": []any{"a", 2, "c"}, "long": "héllo", "empty": ""})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(got) != "ann: a, 2, c / hél / none" {
		t.Errorf("got %q", got)
	}
	if tpl.System != "be brief" {
		t.Errorf("system %q", tpl.System)
	}
}

func TestMissingKeyIsAnError(t *testing.T) {
	tpl := parse(t, "template: \"{{ .nope }}\"\n")
	if _, err := tpl.Render(map[string]any{"x": 1}); !errors.Is(err, prompt.ErrInvalid) {
		t.Fatalf("a missing key rendered: %v", err)
	}
	if _, err := tpl.Render(nil); err == nil {
		t.Fatal("a nil context rendered a missing key")
	}
}

func TestDisallowedFuncIsAParseError(t *testing.T) {
	// text/template predeclared builtins (len, printf, index, ...) stay, as in
	// flo; the FuncMap adds only join, trunc and default.
	for _, fn := range []string{`env "HOME"`, `upper "x"`, `lower "x"`, `replace "a" "b" "c"`, `readFile "/etc/passwd"`, `now`} {
		_, err := prompt.ParseTemplate(prompt.Ref{Name: "p", Version: 1}, []byte("template: '{{ "+fn+" }}'\n"))
		if !errors.Is(err, prompt.ErrInvalid) {
			t.Errorf("%s: %v", fn, err)
		}
	}
}

func TestParseRejects(t *testing.T) {
	for name, body := range map[string]string{
		"no template":  "system: x\n",
		"blank":        "template: '  '\n",
		"unknown key":  "template: x\nextra: 1\n",
		"bad template": "template: '{{ .a '\n",
	} {
		if _, err := prompt.ParseTemplate(prompt.Ref{Name: "p", Version: 1}, []byte(body)); !errors.Is(err, prompt.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestHashIsTheFileHash(t *testing.T) {
	body := "template: hi\n"
	sum := sha256.Sum256([]byte(body))
	if tpl := parse(t, body); tpl.Hash != hex.EncodeToString(sum[:]) {
		t.Errorf("hash %s", tpl.Hash)
	}
	if parse(t, body).Hash == parse(t, "template: ho\n").Hash {
		t.Error("hash ignores the text")
	}
}

func TestMessages(t *testing.T) {
	got, err := parse(t, "system: sys\ntemplate: 'hi {{ .n }}'\n").Messages(map[string]any{"n": "x"})
	if err != nil {
		t.Fatal(err)
	}
	want := []llm.Message{{Role: "system", Content: "sys"}, {Role: "user", Content: "hi x"}}
	if len(got) != 2 || got[0].Role != want[0].Role || got[0].Content != want[0].Content ||
		got[1].Role != want[1].Role || got[1].Content != want[1].Content {
		t.Fatalf("%+v", got)
	}
	got, err = parse(t, "template: 'hi'\n").Messages(nil)
	if err != nil || len(got) != 1 || got[0].Role != "user" {
		t.Fatalf("no system: %+v %v", got, err)
	}
	if _, err := parse(t, "template: '{{ .n }}'\n").Messages(nil); err == nil {
		t.Fatal("missing key rendered via Messages")
	}
}

func TestDirSourcePrecedence(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	write(t, b, "p@1.yaml", "template: from-b")
	write(t, b, "q@2.yaml", "template: only-b\n")
	src := prompt.DirSource(a, b)
	tpl, err := src.Load(prompt.Ref{Name: "p", Version: 1})
	if err != nil || tpl.Body != "from-b" {
		t.Fatalf("%+v %v", tpl, err)
	}
	write(t, a, "p@1.yaml", "template: from-a")
	if tpl, err = src.Load(prompt.Ref{Name: "p", Version: 1}); err != nil || tpl.Body != "from-a" {
		t.Fatalf("first dir must win: %+v %v", tpl, err)
	}
	if tpl, err = src.Load(prompt.Ref{Name: "q", Version: 2}); err != nil || tpl.Body != "only-b" {
		t.Fatalf("%+v %v", tpl, err)
	}
	if _, err = src.Load(prompt.Ref{Name: "p", Version: 2}); !errors.Is(err, prompt.ErrNotFound) {
		t.Fatalf("versions are separate files: %v", err)
	}
}

func TestBrokenFileDoesNotFallThrough(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	write(t, a, "p@1.yaml", "template: '{{ .a '\n")
	write(t, b, "p@1.yaml", "template: ok\n")
	_, err := prompt.DirSource(a, b).Load(prompt.Ref{Name: "p", Version: 1})
	if !errors.Is(err, prompt.ErrInvalid) {
		t.Fatalf("a broken file fell through: %v", err)
	}
}

func TestFSSource(t *testing.T) {
	fsys := fstest.MapFS{"prompts/p@1.yaml": {Data: []byte("template: in-fs\n")}}
	src := prompt.FSSource(fsys, "prompts")
	tpl, err := src.Load(prompt.Ref{Name: "p", Version: 1})
	if err != nil || tpl.Body != "in-fs" {
		t.Fatalf("%+v %v", tpl, err)
	}
	if _, err = src.Load(prompt.Ref{Name: "p", Version: 2}); !errors.Is(err, prompt.ErrNotFound) {
		t.Fatalf("%v", err)
	}
	root := prompt.FSSource(fstest.MapFS{"p@1.yaml": {Data: []byte("template: top\n")}}, ".")
	if tpl, err = root.Load(prompt.Ref{Name: "p", Version: 1}); err != nil || tpl.Body != "top" {
		t.Fatalf("%+v %v", tpl, err)
	}
}
