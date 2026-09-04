package cmdsurface

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"hop.top/kit/go/console/output"
)

// widgetRow is the structured output the schema-declaring fixture
// renders through the output package.
type widgetRow struct {
	ID    int    `json:"id" table:"ID"`
	Name  string `json:"name" table:"NAME"`
	Count int64  `json:"count" table:"COUNT"`
}

// newSchemaTree builds a tree wired like a kit root: the output flags
// on the root, and leaves that render through output.Dispatch.
//
//	root
//	├── widget
//	│   ├── list        declares an output schema; renders []widgetRow
//	│   └── plain       no schema; renders the same rows
//	├── noisy           declares a schema; writes text after its JSON
//	├── shell           interactive
//	├── listen          kit/network=ingress
//	├── upgrade         kit/self-hosting
//	└── serve           the depth-1 serve verb
//	    └── socket
func newSchemaTree() *cobra.Command {
	root := &cobra.Command{Use: "fix"}
	v := viper.New()
	output.RegisterFlags(root, v)

	rows := []widgetRow{{1, "bolt", 9007199254740993}, {2, "nut", 2}}
	render := func(cmd *cobra.Command, _ []string) error {
		return output.Dispatch(cmd, v, rows)
	}
	schema := map[string]string{
		"kit/side-effect":           "read",
		"kit/output-schema":         `{"type":"array"}`,
		"kit/output-schema-version": "1.0",
	}

	widget := &cobra.Command{Use: "widget"}
	widget.AddCommand(&cobra.Command{Use: "list", RunE: render, Annotations: schema})
	widget.AddCommand(&cobra.Command{
		Use: "plain", RunE: render,
		Annotations: map[string]string{"kit/side-effect": "read"},
	})
	root.AddCommand(widget)

	root.AddCommand(&cobra.Command{
		Use: "noisy",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := render(cmd, args); err != nil {
				return err
			}
			cmd.Print("done\n")
			return nil
		},
		Annotations: schema,
	})

	nop := func(*cobra.Command, []string) error { return nil }
	root.AddCommand(&cobra.Command{
		Use: "shell", RunE: nop,
		Annotations: map[string]string{"kit/side-effect": "interactive"},
	})
	root.AddCommand(&cobra.Command{
		Use: "listen", RunE: nop,
		Annotations: map[string]string{"kit/side-effect": "write-local", "kit/network": "ingress"},
	})
	root.AddCommand(&cobra.Command{
		Use: "upgrade", RunE: nop,
		Annotations: map[string]string{"kit/side-effect": "write-local", "kit/self-hosting": "true"},
	})
	serve := &cobra.Command{
		Use: "serve", RunE: nop,
		Annotations: map[string]string{"kit/side-effect": "write-shared"},
	}
	serve.AddCommand(&cobra.Command{
		Use: "socket", RunE: nop,
		Annotations: map[string]string{"kit/side-effect": "write-shared"},
	})
	root.AddCommand(serve)

	return root
}

// TestInProcessRunner_StructuredData pins the format rule: a
// schema-declaring command invoked without a format runs in json and
// its output is decoded into Data, exactly. Stdout is empty: the
// caller asked for no rendering, and the JSON text would only repeat
// Data.
func TestInProcessRunner_StructuredData(t *testing.T) {
	r := InProcessRunner(newSchemaTree())
	res, err := r.Run(context.Background(), Invocation{Path: []string{"widget", "list"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("ExitCode=%d stderr=%q", res.ExitCode, res.Stderr)
	}
	rows, ok := res.Data.([]any)
	if !ok || len(rows) != 2 {
		t.Fatalf("Data=%#v want two decoded rows", res.Data)
	}
	first, _ := rows[0].(map[string]any)
	if first["name"] != "bolt" {
		t.Errorf("Data[0]=%#v want name=bolt", first)
	}
	// Large integers survive the round trip: the decoder keeps the
	// number's text rather than collapsing it to a float64.
	if got, _ := first["count"].(json.Number); got.String() != "9007199254740993" {
		t.Errorf("Data[0].count=%#v want the exact integer", first["count"])
	}
	if res.Stdout != "" {
		t.Errorf("Stdout=%q want empty: the caller asked for no rendering", res.Stdout)
	}
}

// TestInProcessRunner_StructuredData_Stream pins that the done event
// carries Data the same way Run does.
func TestInProcessRunner_StructuredData_Stream(t *testing.T) {
	r := InProcessRunner(newSchemaTree())
	ch := make(chan Event, 32)
	errc := make(chan error, 1)
	go func() { errc <- r.Stream(context.Background(), Invocation{Path: []string{"widget", "list"}}, ch) }()
	var done *Result
	for ev := range ch {
		if ev.Kind == "done" {
			done = ev.Data.(*Result)
		}
	}
	if err := <-errc; err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if done == nil {
		t.Fatal("no done event")
	}
	if rows, ok := done.Data.([]any); !ok || len(rows) != 2 {
		t.Errorf("done.Data=%#v want two decoded rows", done.Data)
	}
	if done.Stdout != "" {
		t.Errorf("done.Stdout=%q want empty", done.Stdout)
	}
}

// TestInProcessRunner_RequestedFormatIsHonored pins the other half of
// the rule: a caller that names a format gets that rendering on
// stdout — json included, alongside Data — and a non-json rendering
// yields no Data.
func TestInProcessRunner_RequestedFormatIsHonored(t *testing.T) {
	r := InProcessRunner(newSchemaTree())
	ctx := context.Background()

	table, err := r.Run(ctx, Invocation{
		Path: []string{"widget", "list"}, Flags: map[string]any{"format": "table"},
	})
	if err != nil {
		t.Fatalf("table Run: %v", err)
	}
	if table.Data != nil {
		t.Errorf("table Data=%#v want nil: the caller asked for a rendering", table.Data)
	}
	if !strings.Contains(table.Stdout, "NAME") || !strings.Contains(table.Stdout, "bolt") {
		t.Errorf("table Stdout=%q want the table rendering", table.Stdout)
	}

	explicit, err := r.Run(ctx, Invocation{
		Path: []string{"widget", "list"}, Flags: map[string]any{"format": "json"},
	})
	if err != nil {
		t.Fatalf("json Run: %v", err)
	}
	if _, ok := explicit.Data.([]any); !ok {
		t.Errorf("explicit json Data=%#v want decoded rows", explicit.Data)
	}
	if !strings.Contains(explicit.Stdout, `"name": "bolt"`) {
		t.Errorf("explicit json Stdout=%q want the rendering the caller asked for", explicit.Stdout)
	}
}

// TestInProcessRunner_NoSchemaKeepsStreams pins that a command without
// a declared schema is untouched: its default rendering, no Data, even
// when the caller asks for json.
func TestInProcessRunner_NoSchemaKeepsStreams(t *testing.T) {
	r := InProcessRunner(newSchemaTree())
	ctx := context.Background()

	res, err := r.Run(ctx, Invocation{Path: []string{"widget", "plain"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Data != nil {
		t.Errorf("Data=%#v want nil for a command with no schema", res.Data)
	}
	if !strings.Contains(res.Stdout, "NAME") {
		t.Errorf("Stdout=%q want the command's default table rendering", res.Stdout)
	}

	asJSON, err := r.Run(ctx, Invocation{
		Path: []string{"widget", "plain"}, Flags: map[string]any{"format": "json"},
	})
	if err != nil {
		t.Fatalf("json Run: %v", err)
	}
	if asJSON.Data != nil {
		t.Errorf("Data=%#v want nil: no schema, so nothing is decoded", asJSON.Data)
	}
	if !strings.Contains(asJSON.Stdout, `"name": "bolt"`) {
		t.Errorf("Stdout=%q want json as requested", asJSON.Stdout)
	}
}

// TestInProcessRunner_TrailingOutputIsNotDecoded pins that Data is
// never a guess: a command that writes anything after its JSON
// document leaves Data nil and the streams intact.
func TestInProcessRunner_TrailingOutputIsNotDecoded(t *testing.T) {
	r := InProcessRunner(newSchemaTree())
	res, err := r.Run(context.Background(), Invocation{Path: []string{"noisy"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Data != nil {
		t.Errorf("Data=%#v want nil for a stream that is not one JSON document", res.Data)
	}
	if !strings.HasSuffix(res.Stdout, "done\n") {
		t.Errorf("Stdout=%q want the command's full output", res.Stdout)
	}
}

// TestInProcessRunner_SchemaWithoutFormatFlag pins that the runner
// only injects --format when the tree can take it: a bare tree with
// a schema but no output flags runs unchanged.
func TestInProcessRunner_SchemaWithoutFormatFlag(t *testing.T) {
	root := &cobra.Command{Use: "bare"}
	root.AddCommand(&cobra.Command{
		Use: "list",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Print("plain text")
			return nil
		},
		Annotations: map[string]string{
			"kit/output-schema":         `{"type":"object"}`,
			"kit/output-schema-version": "1.0",
		},
	})
	r := InProcessRunner(root)
	res, err := r.Run(context.Background(), Invocation{Path: []string{"list"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ExitCode != 0 || res.Stdout != "plain text" || res.Data != nil {
		t.Errorf("got exit=%d stdout=%q data=%#v; want the command untouched",
			res.ExitCode, res.Stdout, res.Data)
	}
}

// TestInProcessRunner_RefusesInteractiveAndSelfHosting pins the two
// classes no runner executes, and that the refusal names the
// reflector's reason.
func TestInProcessRunner_RefusesInteractiveAndSelfHosting(t *testing.T) {
	r := InProcessRunner(newSchemaTree())
	cases := []struct {
		path   []string
		reason string
	}{
		{[]string{"shell"}, "interactive"},
		{[]string{"listen"}, "self-hosting"},
		{[]string{"upgrade"}, "self-hosting"},
		{[]string{"serve"}, "self-hosting"},
		{[]string{"serve", "socket"}, "self-hosting"},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.path, " "), func(t *testing.T) {
			_, err := r.Run(context.Background(), Invocation{Path: tc.path})
			if !errors.Is(err, ErrNotInvocable) {
				t.Fatalf("err=%v want ErrNotInvocable", err)
			}
			if !strings.Contains(err.Error(), tc.reason) {
				t.Errorf("err=%q does not name the reason %q", err, tc.reason)
			}
			ch := make(chan Event, 1)
			if err := r.Stream(context.Background(), Invocation{Path: tc.path}, ch); !errors.Is(err, ErrNotInvocable) {
				t.Errorf("Stream err=%v want ErrNotInvocable", err)
			}
		})
	}

	// The refusal is the runner's, so the bridge reports it as-is.
	b := New(newSchemaTree(), WithPolicy(Policy{DefaultEnabled: []Surface{SurfaceLib}}))
	if _, err := b.Invoke(context.Background(), Invocation{Path: []string{"shell"}}); !errors.Is(err, ErrNotInvocable) {
		t.Errorf("bridge err=%v want ErrNotInvocable", err)
	}
	// Self-hosting commands are not even leaves: discovery withholds
	// them before the runner is reached.
	if _, err := b.Invoke(context.Background(), Invocation{Path: []string{"serve", "socket"}}); !errors.Is(err, ErrUnknownCommand) {
		t.Errorf("bridge err=%v want ErrUnknownCommand for a withheld self-hosting leaf", err)
	}
}
