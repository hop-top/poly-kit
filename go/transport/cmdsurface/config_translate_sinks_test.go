package cmdsurface_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	"hop.top/kit/go/transport/cmdsurface"
)

// sinkPublisher records every publish.
type sinkPublisher struct {
	mu     sync.Mutex
	topics []string
}

func (p *sinkPublisher) Publish(_ context.Context, topic, _ string, _ any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.topics = append(p.topics, topic)
	return nil
}

// sinkFiles is an OpenFile over in-memory buffers.
type sinkFiles struct {
	mu    sync.Mutex
	files map[string]*closeBuffer
	fail  string // a path whose open fails
}

type closeBuffer struct {
	bytes.Buffer
	closed bool
}

func (b *closeBuffer) Close() error { b.closed = true; return nil }

func (f *sinkFiles) open(path string) (io.Writer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if path == f.fail {
		return nil, errors.New("disk full")
	}
	if f.files == nil {
		f.files = map[string]*closeBuffer{}
	}
	b := &closeBuffer{}
	f.files[path] = b
	return b, nil
}

func TestConfigSinkSpecsTranslatesEveryType(t *testing.T) {
	cfg := loadTranslateConfig(t, `
surfaces:
  commands:
    "*":
      sinks:
        - { type: log, level: warn, on: [error], surfaces: [rest], paths: ["report *"] }
    "widget *":
      sinks:
        - { type: bus, topic: widgets.audit, source: widgets }
    "widget add":
      sinks:
        - { type: webhook, url: "https://audit.example/x", headers: { X-Team: core } }
        - { type: file, path: /var/log/widget-add.jsonl, on: [success] }
`)
	pub := &sinkPublisher{}
	files := &sinkFiles{}
	handler := slog.NewTextHandler(io.Discard, nil)
	specs, err := cfg.SinkSpecs(cmdsurface.SinkDeps{
		Publisher:  pub,
		OpenFile:   files.open,
		LogHandler: handler,
	})
	if err != nil {
		t.Fatalf("SinkSpecs: %v", err)
	}
	if len(specs) != 4 {
		t.Fatalf("specs=%d, want 4: %+v", len(specs), specs)
	}

	// Sorted by pattern, then list order: "*", "widget *", "widget add".
	logSpec := specs[0]
	ls, ok := logSpec.Sink.(*cmdsurface.LogSink)
	if !ok || ls.Level != slog.LevelWarn || ls.Handler != handler {
		t.Errorf("log sink=%#v", logSpec.Sink)
	}
	if logSpec.OnOK || !logSpec.OnError ||
		!reflect.DeepEqual(logSpec.Surfaces, []cmdsurface.Surface{cmdsurface.SurfaceREST}) ||
		!reflect.DeepEqual(logSpec.Paths, []string{"report *"}) {
		t.Errorf("log spec filters=%+v", logSpec)
	}

	busSpec := specs[1]
	bs, ok := busSpec.Sink.(*cmdsurface.BusSink)
	if !ok || bs.Topic != "widgets.audit" || bs.Source != "widgets" || bs.Publisher != pub {
		t.Errorf("bus sink=%#v", busSpec.Sink)
	}
	if !busSpec.OnOK || !busSpec.OnError || !reflect.DeepEqual(busSpec.Paths, []string{"widget *"}) {
		t.Errorf("bus spec filters=%+v; the pattern is the path filter, on defaults to both", busSpec)
	}

	ws, ok := specs[2].Sink.(*cmdsurface.WebhookSink)
	if !ok || ws.URL != "https://audit.example/x" || ws.Headers["X-Team"] != "core" {
		t.Errorf("webhook sink=%#v", specs[2].Sink)
	}
	if !reflect.DeepEqual(specs[2].Paths, []string{"widget add"}) {
		t.Errorf("webhook paths=%v", specs[2].Paths)
	}

	fs, ok := specs[3].Sink.(*cmdsurface.FileSink)
	if !ok || fs.W != io.Writer(files.files["/var/log/widget-add.jsonl"]) {
		t.Errorf("file sink=%#v, want the writer OpenFile returned", specs[3].Sink)
	}
	if !specs[3].OnOK || specs[3].OnError {
		t.Errorf("file spec outcomes=%+v, want success only", specs[3])
	}
}

func TestConfigSinkSpecsReachTheBridge(t *testing.T) {
	cfg := loadTranslateConfig(t, `
surfaces:
  commands:
    "widget add":
      enabled: [cli, rest]
      sinks:
        - { type: file, path: add.jsonl }
    "ping":
      enabled: [cli, rest]
`)
	files := &sinkFiles{}
	specs, err := cfg.SinkSpecs(cmdsurface.SinkDeps{OpenFile: files.open})
	if err != nil {
		t.Fatalf("SinkSpecs: %v", err)
	}

	root := &cobra.Command{Use: "tool"}
	widget := &cobra.Command{Use: "widget"}
	widget.AddCommand(&cobra.Command{Use: "add", RunE: func(*cobra.Command, []string) error { return nil }})
	root.AddCommand(widget, &cobra.Command{Use: "ping", RunE: func(*cobra.Command, []string) error { return nil }})
	b, err := cmdsurface.FromConfig(root, cfg, cmdsurface.WithSinks(specs...))
	if err != nil {
		t.Fatalf("FromConfig: %v", err)
	}

	rest := cmdsurface.Meta{Surface: cmdsurface.SurfaceREST}
	for _, path := range [][]string{{"widget", "add"}, {"ping"}} {
		if _, err := b.Invoke(context.Background(), cmdsurface.Invocation{Path: path, Meta: rest}); err != nil {
			t.Fatalf("Invoke %v: %v", path, err)
		}
	}
	got := files.files["add.jsonl"].String()
	if !strings.Contains(got, `"path":"widget add"`) {
		t.Errorf("file sink did not record widget add: %q", got)
	}
	if strings.Contains(got, `"path":"ping"`) {
		t.Errorf("file sink recorded ping, outside its pattern: %q", got)
	}
}

func TestConfigSinkSpecsRefusals(t *testing.T) {
	deps := cmdsurface.SinkDeps{Publisher: &sinkPublisher{}, OpenFile: (&sinkFiles{}).open}
	cases := []struct {
		name, yaml, want string
		deps             cmdsurface.SinkDeps
	}{
		{"missing type", `{ url: "https://x" }`, "type is required", deps},
		{"unknown type", `{ type: kafka }`, `unknown type "kafka"`, deps},
		{"foreign key", `{ type: bus, topic: t, url: "https://x" }`, "url applies to webhook", deps},
		{"webhook without url", `{ type: webhook }`, "webhook needs url", deps},
		{"bus without topic", `{ type: bus }`, "bus needs topic", deps},
		{"bus without publisher", `{ type: bus, topic: t }`, "SinkDeps.Publisher", cmdsurface.SinkDeps{}},
		{"file without path", `{ type: file }`, "file needs path", deps},
		{"file without opener", `{ type: file, path: p }`, "SinkDeps.OpenFile", cmdsurface.SinkDeps{}},
		{"bad level", `{ type: log, level: loud }`, `log level "loud"`, deps},
		{"bad outcome", `{ type: log, on: [always] }`, `on "always"`, deps},
		{"bad surface", `{ type: log, surfaces: [fax] }`, `unknown surface "fax"`, deps},
		{"paths under a named pattern", `{ type: log, paths: ["widget *"] }`, `paths applies under the "*" pattern`, deps},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := loadTranslateConfig(t, "surfaces:\n  commands:\n    \"widget add\":\n      sinks:\n        - "+tc.yaml+"\n")
			_, err := cfg.SinkSpecs(tc.deps)
			if err == nil {
				t.Fatal("SinkSpecs accepted the entry")
			}
			for _, want := range []string{`"widget add"`, "sinks[0]", tc.want} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
		})
	}
}

func TestConfigSinkSpecsClosesFilesWhenAnOpenFails(t *testing.T) {
	cfg := loadTranslateConfig(t, `
surfaces:
  commands:
    "a":
      sinks:
        - { type: file, path: first }
    "b":
      sinks:
        - { type: file, path: second }
`)
	files := &sinkFiles{fail: "second"}
	_, err := cfg.SinkSpecs(cmdsurface.SinkDeps{OpenFile: files.open})
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("err=%v, want the open failure", err)
	}
	if !files.files["first"].closed {
		t.Error("the file opened before the failure was left open")
	}
}

func TestConfigSinkSpecsOpensNothingWhenAnEntryIsInvalid(t *testing.T) {
	cfg := loadTranslateConfig(t, `
surfaces:
  commands:
    "a":
      sinks:
        - { type: file, path: first }
    "b":
      sinks:
        - { type: nope }
`)
	files := &sinkFiles{}
	if _, err := cfg.SinkSpecs(cmdsurface.SinkDeps{OpenFile: files.open}); err == nil {
		t.Fatal("SinkSpecs accepted an unknown type")
	}
	if len(files.files) != 0 {
		t.Errorf("opened %d files before validation finished", len(files.files))
	}
}

func TestConfigSinkSpecsEmpty(t *testing.T) {
	specs, err := loadTranslateConfig(t, "surfaces: {}\n").SinkSpecs(cmdsurface.SinkDeps{})
	if err != nil || len(specs) != 0 {
		t.Errorf("specs=%v err=%v, want none", specs, err)
	}
}
