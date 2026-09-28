package cmdsurface

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strings"

	"hop.top/kit/go/transport/api"
)

// SinkDeps carries what a sinks: block cannot say in YAML: the
// backends only the caller holds. A field is required only when an
// entry of its type exists.
type SinkDeps struct {
	// Publisher backs type: bus entries.
	Publisher api.EventPublisher
	// OpenFile opens the writer a type: file entry appends to. The
	// caller owns what it opens; close it at shutdown. The usual
	// implementation:
	//
	//	func(path string) (io.Writer, error) {
	//	    return os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	//	}
	OpenFile func(path string) (io.Writer, error)
	// LogHandler backs type: log entries. Nil uses slog.Default().
	LogHandler slog.Handler
	// HTTPClient posts type: webhook entries. Nil uses the sink's
	// default client.
	HTTPClient *http.Client
}

// SinkSpecs returns one SinkSpec per entry of every per-command
// sinks: list, ready for [WithSinks]:
//
//	sinks, err := cfg.SinkSpecs(cmdsurface.SinkDeps{Publisher: pub, OpenFile: open})
//	// ...
//	b, err := cmdsurface.FromConfig(root, cfg, cmdsurface.WithSinks(sinks...))
//
// The command pattern the list sits under is the entry's path filter:
// "widget *" reaches the widget subtree, "*" every command. An
// entry's own paths: key narrows the "*" pattern only; under any other
// pattern the pattern already names the commands, and the key is
// refused. on: takes "success" and "error" (empty is both); surfaces:
// takes surface names.
//
// type: selects the sink and the keys that apply: webhook (url
// required; headers), bus (topic required; source), log (level:
// debug, info, warn or error), file (path required). A key that
// belongs to another type, an unknown type, and a missing dependency
// in deps are errors at startup, not silent no-ops. Files are opened
// only once every entry is valid; if one fails to open, those already
// opened are closed.
func (c Config) SinkSpecs(deps SinkDeps) ([]SinkSpec, error) {
	type pending struct {
		spec SinkSpec
		path string // file: path, opened once every entry is valid
	}
	var entries []pending
	for _, pattern := range sortedPatterns(c) {
		for i, sc := range c.Surfaces.Commands[pattern].Sinks {
			spec, err := sinkSpec(pattern, sc, deps)
			if err != nil {
				return nil, fmt.Errorf("cmdsurface: config %q: sinks[%d]: %w", pattern, i, err)
			}
			p := pending{spec: spec}
			if spec.Sink == nil {
				p.path = sc.Path
			}
			entries = append(entries, p)
		}
	}

	out := make([]SinkSpec, 0, len(entries))
	var opened []io.Writer
	for _, e := range entries {
		if e.path != "" {
			w, err := deps.OpenFile(e.path)
			if err != nil {
				closeAll(opened)
				return nil, fmt.Errorf("cmdsurface: config: sinks: open %s: %w", e.path, err)
			}
			opened = append(opened, w)
			e.spec.Sink = &FileSink{W: w}
		}
		out = append(out, e.spec)
	}
	return out, nil
}

// sinkSpec validates one entry and builds its spec. A file entry is
// returned with a nil Sink: SinkSpecs opens it last.
func sinkSpec(pattern string, sc SinkConfig, deps SinkDeps) (SinkSpec, error) {
	spec := SinkSpec{Surfaces: slices.Clone(sc.Surfaces)}
	if err := validateSurfaces(sc.Surfaces); err != nil {
		return spec, err
	}
	var err error
	if spec.OnOK, spec.OnError, err = sinkOutcomes(sc.On); err != nil {
		return spec, err
	}
	switch {
	case strings.TrimSpace(pattern) == "*":
		spec.Paths = slices.Clone(sc.Paths)
	case len(sc.Paths) > 0:
		return spec, errors.New(`paths applies under the "*" pattern; this one already names its commands`)
	default:
		spec.Paths = []string{pattern}
	}

	typ := strings.ToLower(strings.TrimSpace(sc.Type))
	switch typ {
	case "webhook", "bus", "log", "file":
	case "":
		return spec, errors.New("type is required: webhook, bus, log or file")
	default:
		return spec, fmt.Errorf("unknown type %q; use webhook, bus, log or file", sc.Type)
	}
	if err := sinkForeignKeys(typ, sc); err != nil {
		return spec, err
	}
	switch typ {
	case "webhook":
		if sc.URL == "" {
			return spec, errors.New("webhook needs url")
		}
		spec.Sink = &WebhookSink{URL: sc.URL, Client: deps.HTTPClient, Headers: maps.Clone(sc.Headers)}
	case "bus":
		if sc.Topic == "" {
			return spec, errors.New("bus needs topic")
		}
		if deps.Publisher == nil {
			return spec, errors.New("bus needs SinkDeps.Publisher")
		}
		spec.Sink = &BusSink{Publisher: deps.Publisher, Topic: sc.Topic, Source: sc.Source}
	case "log":
		var level slog.Level
		if sc.Level != "" {
			if err := level.UnmarshalText([]byte(sc.Level)); err != nil {
				return spec, fmt.Errorf("log level %q: use debug, info, warn or error", sc.Level)
			}
		}
		spec.Sink = &LogSink{Handler: deps.LogHandler, Level: level}
	case "file":
		if sc.Path == "" {
			return spec, errors.New("file needs path")
		}
		if deps.OpenFile == nil {
			return spec, errors.New("file needs SinkDeps.OpenFile")
		}
	}
	return spec, nil
}

// sinkOutcomes resolves an on: list into the OnOK / OnError pair.
func sinkOutcomes(on []string) (ok, failed bool, err error) {
	if len(on) == 0 {
		return true, true, nil
	}
	for _, tok := range on {
		switch strings.ToLower(strings.TrimSpace(tok)) {
		case "success":
			ok = true
		case "error":
			failed = true
		default:
			return false, false, fmt.Errorf("on %q: use success or error", tok)
		}
	}
	return ok, failed, nil
}

// sinkForeignKeys refuses a key set on an entry whose type does not
// read it.
func sinkForeignKeys(typ string, sc SinkConfig) error {
	owners := []struct {
		key, owner string
		set        bool
	}{
		{"url", "webhook", sc.URL != ""},
		{"headers", "webhook", len(sc.Headers) > 0},
		{"topic", "bus", sc.Topic != ""},
		{"source", "bus", sc.Source != ""},
		{"level", "log", sc.Level != ""},
		{"path", "file", sc.Path != ""},
	}
	for _, o := range owners {
		if o.set && typ != o.owner {
			return fmt.Errorf("%s applies to %s, and type is %q", o.key, o.owner, typ)
		}
	}
	return nil
}

// closeAll closes every writer that is an io.Closer.
func closeAll(ws []io.Writer) {
	for _, w := range ws {
		if c, ok := w.(io.Closer); ok {
			_ = c.Close()
		}
	}
}
