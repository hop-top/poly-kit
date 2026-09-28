package cmdsurface

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"hop.top/kit/go/core/redact"
	"hop.top/kit/go/runtime/telemetry"
)

// Secret fixtures. None of the name-neutral ones is shaped like a
// known credential except ghToken, so a hit on the others can only
// come from the flag annotation, the flag name, or substitution of a
// value already identified as secret.
const (
	tokenValue    = "s3cr3t-tok-value"   // --token (name heuristic)
	passwordValue = "hunter2-pw-value"   // --password (name heuristic)
	dsnValue      = "pg-dsn-s3cr3t-9f2a" // --dsn (kit/secret annotation only)
	ghToken       = "ghp_" + "1234567890abcdefghijABCDEFGHIJ123456"
	authzValue    = "bearer-s3cr3t-hdr-77" // Meta.Extra["authorization"]
)

// leakedSecrets lists every value that must never reach a sink.
var leakedSecrets = []string{tokenValue, passwordValue, dsnValue, ghToken, authzValue}

// secretInvocation is one REST invocation carrying secrets in every
// field an audit record can hold: flag values, positional args, the
// Extra bag, and the command's echoed output.
func secretInvocation() (Invocation, Result, error) {
	inv := Invocation{
		Path: []string{"db", "connect"},
		Args: []string{"primary", ghToken},
		Flags: map[string]any{
			"token":    tokenValue,
			"password": passwordValue,
			"dsn":      dsnValue,
			"region":   "eu-west-1",
			"note":     "rotate " + ghToken,
		},
		Meta: Meta{
			Surface:     SurfaceREST,
			Caller:      "alice",
			RequestID:   "req-1",
			RequestedAt: time.Now(),
			Extra: map[string]string{
				"authorization": "Bearer " + authzValue,
				"remote_addr":   "127.0.0.1:5000",
			},
		},
	}
	res := Result{
		ExitCode: 1,
		Stdout:   "connecting with token " + tokenValue + " dsn " + dsnValue + "\n",
		Stderr:   "password " + passwordValue + " rejected\n",
		Data:     map[string]any{"token": tokenValue, "rows": json.Number("3")},
	}
	err := fmt.Errorf("auth failed for %s using %s", tokenValue, dsnValue)
	return inv, res, err
}

// secretCtx marks --dsn secret the way the bridge does for a leaf
// whose flag carries the kit/secret annotation.
func secretCtx(t *testing.T) context.Context {
	t.Helper()
	cmd := &cobra.Command{Use: "connect", RunE: func(*cobra.Command, []string) error { return nil }}
	cmd.Flags().String("dsn", "", "database dsn")
	if err := cmd.Flags().SetAnnotation("dsn", "kit/secret", []string{"true"}); err != nil {
		t.Fatal(err)
	}
	root := &cobra.Command{Use: "app"}
	db := &cobra.Command{Use: "db"}
	db.AddCommand(cmd)
	root.AddCommand(db)
	b := New(root)
	leaf, err := b.resolveLeaf([]string{"db", "connect"})
	if err != nil {
		t.Fatal(err)
	}
	return b.auditContext(context.Background(), leaf)
}

// assertNoSecrets fails when any secret fixture appears in out.
func assertNoSecrets(t *testing.T, sink, out string) {
	t.Helper()
	if out == "" {
		t.Fatalf("%s: sink captured nothing", sink)
	}
	for _, s := range leakedSecrets {
		if strings.Contains(out, s) {
			t.Errorf("%s: secret %q reached the sink:\n%s", sink, s, out)
		}
	}
}

// debugJSONLog captures LogSink output at debug level, the level at
// which the sink also records stdout and stderr.
func debugJSONLog() (*bytes.Buffer, *LogSink) {
	var buf bytes.Buffer
	h := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	return &buf, &LogSink{Handler: h, Level: slog.LevelDebug}
}

// emitThroughSet sends the secret invocation through a SinkSet, the
// fan-out every shipped caller uses.
func emitThroughSet(ctx context.Context, t *testing.T, sink Sink) {
	t.Helper()
	inv, res, err := secretInvocation()
	set := SinkSet{{Sink: sink, OnError: true, OnOK: true}}
	if errs := set.Emit(ctx, inv, res, err); len(errs) > 0 {
		t.Fatalf("emit: %v", errs)
	}
}

func TestAuditRedaction_LogSink(t *testing.T) {
	buf, sink := debugJSONLog()
	emitThroughSet(secretCtx(t), t, sink)
	assertNoSecrets(t, "log", buf.String())
}

func TestAuditRedaction_FileSinkDefaultFormat(t *testing.T) {
	var buf bytes.Buffer
	emitThroughSet(secretCtx(t), t, &FileSink{W: &buf})
	assertNoSecrets(t, "file", buf.String())
}

// A custom formatter sees the whole invocation, as an adopter's
// richer audit schema would.
func TestAuditRedaction_FileSinkCustomFormat(t *testing.T) {
	var buf bytes.Buffer
	sink := &FileSink{W: &buf, Format: func(inv Invocation, res Result, err error) ([]byte, error) {
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		return json.Marshal(map[string]any{"inv": inv, "res": res, "err": msg})
	}}
	emitThroughSet(secretCtx(t), t, sink)
	out := buf.String()
	assertNoSecrets(t, "file(custom)", out)
	// Non-secret context survives.
	for _, keep := range []string{"eu-west-1", "primary", "alice", "127.0.0.1:5000"} {
		if !strings.Contains(out, keep) {
			t.Errorf("file(custom): lost non-secret %q:\n%s", keep, out)
		}
	}
}

func TestAuditRedaction_WebhookSink(t *testing.T) {
	var (
		mu   sync.Mutex
		body []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		body = b
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	emitThroughSet(secretCtx(t), t, &WebhookSink{URL: srv.URL})
	mu.Lock()
	defer mu.Unlock()
	assertNoSecrets(t, "webhook", string(body))
}

func TestAuditRedaction_BusSink(t *testing.T) {
	bus := &sinkFakeBus{}
	emitThroughSet(secretCtx(t), t, &BusSink{Publisher: bus, Topic: "audit"})
	payload, err := json.Marshal(bus.latest().payload)
	if err != nil {
		t.Fatal(err)
	}
	assertNoSecrets(t, "bus", string(payload))
}

func TestAuditRedaction_TelemetrySinkFullMode(t *testing.T) {
	em := &stubEmitter{}
	sink, err := NewTelemetrySink(WithEmitter(em), WithMode(telemetry.ModeFull))
	if err != nil {
		t.Fatal(err)
	}
	emitThroughSet(secretCtx(t), t, sink)
	drainSync(t, sink)
	evs := em.events()
	if len(evs) != 1 {
		t.Fatalf("events=%d want 1", len(evs))
	}
	out, _ := json.Marshal(evs[0])
	assertNoSecrets(t, "telemetry", string(out))
	if evs[0].Flags["region"] != "eu-west-1" {
		t.Errorf("telemetry: non-secret flag lost: %v", evs[0].Flags)
	}
}

// The bridge stamps the leaf's secret flags itself: an annotated flag
// is redacted end to end without the caller naming it.
func TestAuditRedaction_BridgeInvokeAnnotatedFlag(t *testing.T) {
	root := &cobra.Command{Use: "app"}
	db := &cobra.Command{Use: "db"}
	connect := &cobra.Command{Use: "connect", RunE: func(*cobra.Command, []string) error { return nil }}
	connect.Flags().String("dsn", "", "database dsn")
	if err := connect.Flags().SetAnnotation("dsn", "kit/secret", []string{"true"}); err != nil {
		t.Fatal(err)
	}
	db.AddCommand(connect)
	root.AddCommand(db)

	var buf bytes.Buffer
	sink := &FileSink{W: &buf, Format: func(inv Invocation, res Result, err error) ([]byte, error) {
		return json.Marshal(map[string]any{"inv": inv, "res": res})
	}}
	// The runner echoes the dsn, as a verbose command would.
	runner := &fakeRunner{run: func(_ context.Context, inv Invocation) (Result, error) {
		return Result{Stdout: fmt.Sprintf("dsn=%v\n", inv.Flags["dsn"])}, nil
	}}
	b := New(root, WithRunner(runner), WithSinks(SinkSpec{Sink: sink, OnOK: true, OnError: true}))
	b.Expose("db connect", SurfaceREST)

	_, err := b.Invoke(context.Background(), Invocation{
		Path:  []string{"db", "connect"},
		Flags: map[string]any{"dsn": dsnValue},
		Meta:  Meta{Surface: SurfaceREST},
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	assertNoSecrets(t, "bridge", buf.String())
}

// Adopters wrapping their Runner to emit (the sinkRunner pattern)
// receive the bridge's context, so the annotation reaches them too.
func TestAuditRedaction_SinkRunnerPatternSeesAnnotation(t *testing.T) {
	root := &cobra.Command{Use: "app"}
	connect := &cobra.Command{Use: "connect", RunE: func(*cobra.Command, []string) error { return nil }}
	connect.Flags().String("dsn", "", "database dsn")
	if err := connect.Flags().SetAnnotation("dsn", "kit/secret", []string{"true"}); err != nil {
		t.Fatal(err)
	}
	root.AddCommand(connect)

	var buf bytes.Buffer
	set := SinkSet{{Sink: &FileSink{W: &buf, Format: func(inv Invocation, _ Result, _ error) ([]byte, error) {
		return json.Marshal(inv)
	}}, OnOK: true, OnError: true}}
	runner := &fakeRunner{run: func(ctx context.Context, inv Invocation) (Result, error) {
		_ = set.Emit(ctx, inv, Result{}, nil)
		return Result{}, nil
	}}
	b := New(root, WithRunner(runner))
	if _, err := b.Invoke(context.Background(), Invocation{
		Path:  []string{"connect"},
		Flags: map[string]any{"dsn": dsnValue},
		Meta:  Meta{Surface: SurfaceCLI},
	}); err != nil {
		t.Fatalf("invoke: %v", err)
	}
	assertNoSecrets(t, "sinkRunner", buf.String())
}

// streamEchoRunner streams one line echoing the dsn, emits to its own
// SinkSet under the context it was given (the sinkRunner pattern),
// and ends with a done event.
type streamEchoRunner struct{ set SinkSet }

func (r *streamEchoRunner) Run(ctx context.Context, inv Invocation) (Result, error) {
	_ = r.set.Emit(ctx, inv, Result{}, nil)
	return Result{Stdout: fmt.Sprintf("dsn=%v\n", inv.Flags["dsn"])}, nil
}

func (r *streamEchoRunner) Stream(ctx context.Context, inv Invocation, out chan<- Event) error {
	defer close(out)
	_ = r.set.Emit(ctx, inv, Result{}, nil)
	line := fmt.Sprintf("dsn=%v", inv.Flags["dsn"])
	out <- Event{Kind: "stdout", Data: line}
	out <- Event{Kind: "done", Data: &Result{Stdout: line + "\n"}}
	return nil
}

// Every audit path of the two halves of Invoke reaches the sinks
// through SinkSet.Emit, redacted: a refusal from Bridge.Admit, the
// record Admission.Run and Admission.Stream write, and a Runner's own
// emit under the context either half runs it with.
func TestAuditRedaction_AdmitRefusalAndAdmissionRecords(t *testing.T) {
	root := &cobra.Command{Use: "app"}
	connect := &cobra.Command{Use: "connect", RunE: func(*cobra.Command, []string) error { return nil }}
	connect.Flags().String("dsn", "", "database dsn")
	if err := connect.Flags().SetAnnotation("dsn", "kit/secret", []string{"true"}); err != nil {
		t.Fatal(err)
	}
	root.AddCommand(connect)

	format := func(inv Invocation, res Result, err error) ([]byte, error) {
		return json.Marshal(map[string]any{"inv": inv, "res": res, "err": fmt.Sprint(err)})
	}
	var bridgeBuf, runnerBuf bytes.Buffer
	runner := &streamEchoRunner{set: SinkSet{{Sink: &FileSink{W: &runnerBuf, Format: format}, OnOK: true, OnError: true}}}
	refuse := true
	b := New(root, WithRunner(runner),
		WithSinks(SinkSpec{Sink: &FileSink{W: &bridgeBuf, Format: format}, OnOK: true, OnError: true}),
		WithPermission(func(context.Context, Meta, *Leaf) PermissionDecision {
			return PermissionDecision{Allowed: !refuse, Reason: "dsn=" + dsnValue}
		}))
	b.Expose("connect", SurfaceREST)
	inv := Invocation{
		Path:  []string{"connect"},
		Flags: map[string]any{"dsn": dsnValue},
		Meta:  Meta{Surface: SurfaceREST},
	}

	if _, err := b.Admit(context.Background(), inv); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("admit: err=%v, want ErrPermissionDenied", err)
	}
	assertNoSecrets(t, "admit refusal", bridgeBuf.String())

	refuse = false
	for _, run := range []struct {
		name string
		do   func(*Admission) error
	}{
		{"run", func(a *Admission) error { _, err := a.Run(context.Background()); return err }},
		{"stream", func(a *Admission) error {
			out := make(chan Event, 4)
			go func() {
				for range out {
				}
			}()
			return a.Stream(context.Background(), out)
		}},
	} {
		bridgeBuf.Reset()
		runnerBuf.Reset()
		adm, err := b.Admit(context.Background(), inv)
		if err != nil {
			t.Fatalf("%s: admit: %v", run.name, err)
		}
		if err := run.do(adm); err != nil {
			t.Fatalf("%s: %v", run.name, err)
		}
		assertNoSecrets(t, run.name+" record", bridgeBuf.String())
		assertNoSecrets(t, run.name+" sinkRunner", runnerBuf.String())
	}
}

// Redaction must not break sinks that route on the error's identity.
func TestAuditRedaction_ErrorIdentityPreserved(t *testing.T) {
	rec := &sinkRecorder{}
	set := SinkSet{{Sink: rec, OnError: true}}
	cause := fmt.Errorf("%w: bad token %s", ErrAuthRefused, tokenValue)
	inv := Invocation{Path: []string{"x"}, Flags: map[string]any{"token": tokenValue}, Meta: Meta{Surface: SurfaceREST}}
	set.Emit(context.Background(), inv, Result{}, cause)
	if len(rec.calls) != 1 {
		t.Fatalf("records=%d", len(rec.calls))
	}
	got := rec.calls[0].err
	if !errors.Is(got, ErrAuthRefused) {
		t.Errorf("errors.Is(ErrAuthRefused) lost after redaction: %v", got)
	}
	if strings.Contains(got.Error(), tokenValue) {
		t.Errorf("error message leaked: %v", got)
	}
}

// The caller's values are never mutated: redaction works on copies.
func TestAuditRedaction_DoesNotMutateCaller(t *testing.T) {
	inv, res, err := secretInvocation()
	set := SinkSet{{Sink: &sinkRecorder{}, OnError: true}}
	set.Emit(context.Background(), inv, res, err)
	if inv.Flags["token"] != tokenValue || inv.Args[1] != ghToken ||
		inv.Meta.Extra["authorization"] != "Bearer "+authzValue ||
		!strings.Contains(res.Stdout, tokenValue) {
		t.Errorf("caller values mutated: %+v %+v", inv, res)
	}
	if d, _ := res.Data.(map[string]any); d["token"] != tokenValue {
		t.Errorf("caller Data mutated: %v", res.Data)
	}
}

func TestSecretName(t *testing.T) {
	for name, want := range map[string]bool{
		"token": true, "api-token": true, "oauth_token": true, "--token": true,
		"password": true, "db-passwd": true, "passphrase": true, "pass": true,
		"secret": true, "client-secret": true, "credentials": true, "creds": true,
		"api-key": true, "apiKey": true, "x-api-key": true, "access_key": true,
		"private-key": true, "key": true, "pin": true, "pwd": true,
		"authorization": true, "proxy-authorization": true, "auth": true,
		"x-auth-token": true, "cookie": true, "set-cookie": true, "jwt": true,
		"session-id": true, "otp": true, "bearer": true,

		"name": false, "region": false, "author": false, "sort-key": false,
		"idempotency-key": false, "primary-key": false, "keyboard": false,
		"pass-through": false, "passthrough": false, "pinned": false,
		"remote_addr": false, "scopes": false, "": false, "--": false,
	} {
		if got := secretName(name); got != want {
			t.Errorf("secretName(%q)=%v want %v", name, got, want)
		}
	}
}

// MarkFlagSecret on a root persistent flag reaches every leaf that
// inherits it; an explicit "false" opts a flag back out.
func TestMarkFlagSecret_InheritedPersistentFlag(t *testing.T) {
	root := &cobra.Command{Use: "app"}
	root.PersistentFlags().String("dsn", "", "")
	if err := MarkFlagSecret(root.PersistentFlags(), "dsn"); err != nil {
		t.Fatal(err)
	}
	leaf := &cobra.Command{Use: "run", RunE: func(*cobra.Command, []string) error { return nil }}
	leaf.Flags().String("conn", "", "")
	if err := MarkFlagSecret(leaf.Flags(), "conn"); err != nil {
		t.Fatal(err)
	}
	leaf.Flags().String("token", "", "")
	if err := leaf.Flags().SetAnnotation("token", AnnotationSecretFlag, []string{"false"}); err != nil {
		t.Fatal(err)
	}
	root.AddCommand(leaf)

	got := secretFlagSet(leaf)
	if !got["dsn"] || !got["conn"] || got["token"] || len(got) != 2 {
		t.Errorf("secretFlagSet=%v want dsn, conn", got)
	}
	if err := MarkFlagSecret(leaf.Flags(), "missing"); err == nil {
		t.Error("MarkFlagSecret on an unknown flag: want error")
	}
}

// A transport's own refusal (Bridge.Audit) resolves the leaf from the
// path, so annotated flags are masked there too.
func TestAuditRedaction_BridgeAuditResolvesLeaf(t *testing.T) {
	root := &cobra.Command{Use: "app"}
	connect := &cobra.Command{Use: "connect", RunE: func(*cobra.Command, []string) error { return nil }}
	connect.Flags().String("dsn", "", "")
	if err := MarkFlagSecret(connect.Flags(), "dsn"); err != nil {
		t.Fatal(err)
	}
	root.AddCommand(connect)
	rec := &sinkRecorder{}
	b := New(root, WithSinks(SinkSpec{Sink: rec, OnError: true}))
	b.Audit(context.Background(), Invocation{
		Path:  []string{"connect"},
		Flags: map[string]any{"dsn": dsnValue},
		Meta:  Meta{Surface: SurfaceREST},
	}, Result{}, ErrAuthRefused)
	if len(rec.calls) != 1 || rec.calls[0].inv.Flags["dsn"] != auditRedacted {
		t.Errorf("dsn not masked: %+v", rec.calls)
	}
}

func TestAuditRedaction_PositionalSecretPairs(t *testing.T) {
	rec := &sinkRecorder{}
	set := SinkSet{{Sink: rec, OnOK: true}}
	inv := Invocation{
		Path: []string{"exec"},
		Args: []string{"--password", passwordValue, "--token=" + tokenValue, "--region", "eu-west-1", "echo " + tokenValue},
		Meta: Meta{Surface: SurfaceREST},
	}
	set.Emit(context.Background(), inv, Result{}, nil)
	want := []string{"--password", auditRedacted, auditRedacted, "--region", "eu-west-1", "echo " + auditRedacted}
	got := rec.calls[0].inv.Args
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("args=%q want %q", got, want)
	}
}

// outputBlindSink stands in for a shipped sink that never reads
// Stdout, Stderr or Data.
type outputBlindSink struct{ sinkRecorder }

func (*outputBlindSink) auditIgnoresOutput() bool { return true }

func TestAuditRedaction_OutputDroppedWhenEverySinkIsBlind(t *testing.T) {
	inv, res, err := secretInvocation()
	blind := &outputBlindSink{}
	SinkSet{{Sink: blind, OnError: true}}.Emit(context.Background(), inv, res, err)
	if r := blind.calls[0].res; r.Stdout != "" || r.Stderr != "" || r.Data != nil || r.ExitCode != res.ExitCode {
		t.Errorf("blind-only set: want output dropped, exit code kept; got %+v", r)
	}

	// One reader in the set means every sink gets the scanned output.
	blind2, reader := &outputBlindSink{}, &sinkRecorder{}
	SinkSet{{Sink: blind2, OnError: true}, {Sink: reader, OnError: true}}.Emit(context.Background(), inv, res, err)
	if r := reader.calls[0].res; !strings.Contains(r.Stdout, "connecting with token "+auditRedacted) {
		t.Errorf("reader: stdout=%q", r.Stdout)
	}

	// Shipped sinks declare what they read.
	for _, c := range []struct {
		sink Sink
		want bool
	}{
		{&FileSink{}, true},
		{&FileSink{Format: DefaultFileSinkFormat}, false},
		{&LogSink{}, true},
		{&LogSink{Level: slog.LevelDebug}, false},
		{&TelemetrySink{}, true},
	} {
		b, ok := c.sink.(auditOutputBlind)
		if got := ok && b.auditIgnoresOutput(); got != c.want {
			t.Errorf("%T%+v ignores output=%v want %v", c.sink, c.sink, got, c.want)
		}
	}
}

// A field too long to scan is withheld, never shipped unscanned.
func TestAuditRedaction_OversizeFieldWithheld(t *testing.T) {
	rec := &sinkRecorder{}
	big := strings.Repeat("x", DefaultAuditMaxFieldBytes) + ghToken
	SinkSet{{Sink: rec, OnOK: true}}.Emit(context.Background(),
		Invocation{Path: []string{"x"}, Meta: Meta{Surface: SurfaceREST}},
		Result{Stdout: big, Data: []any{big}}, nil)
	r := rec.calls[0].res
	if !strings.HasPrefix(r.Stdout, "[withheld from audit: ") {
		t.Errorf("stdout not withheld: %.80q", r.Stdout)
	}
	if s, _ := r.Data.(string); !strings.HasPrefix(s, "[withheld from audit: ") {
		t.Errorf("data not withheld: %.80v", r.Data)
	}
}

// MaxFieldBytes moves the withhold limit for one bridge: a lower
// limit withholds a field the default would scan, a higher one scans
// (and redacts) a field the default would withhold, and a zero keeps
// the default. The last positive value wins.
func TestAuditRedaction_MaxFieldBytes(t *testing.T) {
	long := strings.Repeat("x", DefaultAuditMaxFieldBytes) + " " + ghToken
	short := "rotate " + ghToken
	run := func(stdout string, opts ...AuditRedaction) Result {
		t.Helper()
		root := &cobra.Command{Use: "app"}
		root.AddCommand(&cobra.Command{Use: "sync", RunE: func(*cobra.Command, []string) error { return nil }})
		rec := &sinkRecorder{}
		bopts := []Option{
			WithRunner(&fakeRunner{run: func(context.Context, Invocation) (Result, error) {
				return Result{Stdout: stdout}, nil
			}}),
			WithSinks(SinkSpec{Sink: rec, OnOK: true, OnError: true}),
		}
		for _, o := range opts {
			bopts = append(bopts, WithAuditRedaction(o))
		}
		b := New(root, bopts...)
		b.Expose("sync", SurfaceREST)
		if _, err := b.Invoke(context.Background(), Invocation{Path: []string{"sync"}, Meta: Meta{Surface: SurfaceREST}}); err != nil {
			t.Fatal(err)
		}
		return rec.calls[0].res
	}

	if got := run(long).Stdout; !strings.HasPrefix(got, "[withheld from audit: ") ||
		!strings.Contains(got, fmt.Sprintf("the %d-byte", DefaultAuditMaxFieldBytes)) {
		t.Errorf("default limit: stdout=%.80q", got)
	}
	if got := run(long, AuditRedaction{MaxFieldBytes: 0}).Stdout; !strings.HasPrefix(got, "[withheld from audit: ") {
		t.Errorf("zero keeps the default: stdout=%.80q", got)
	}
	raised := run(long, AuditRedaction{MaxFieldBytes: 1 << 13}).Stdout
	if strings.HasPrefix(raised, "[withheld") || strings.Contains(raised, ghToken) || !strings.HasPrefix(raised, "xxxx") {
		t.Errorf("raised limit: want scanned and redacted, got %.80q...%q", raised, raised[max(0, len(raised)-60):])
	}
	lowered := run(short, AuditRedaction{MaxFieldBytes: 1 << 13}, AuditRedaction{MaxFieldBytes: 16}, AuditRedaction{}).Stdout
	if lowered != "[withheld from audit: 47 bytes exceed the 16-byte redaction scan limit]" {
		t.Errorf("lowered limit (last positive wins): stdout=%q", lowered)
	}
}

// Typed Data (not the generic JSON shape) is redacted through its
// JSON form, the form every shipped sink serializes.
func TestAuditRedaction_TypedData(t *testing.T) {
	type cred struct {
		User   string `json:"user"`
		APIKey string `json:"api_key"`
		Uses   int    `json:"tokens_used"`
	}
	rec := &sinkRecorder{}
	SinkSet{{Sink: rec, OnOK: true}}.Emit(context.Background(),
		Invocation{Path: []string{"x"}, Meta: Meta{Surface: SurfaceREST}},
		Result{Data: []cred{{User: "bob", APIKey: "k-1234567", Uses: 7}}}, nil)
	out, _ := json.Marshal(rec.calls[0].res.Data)
	if strings.Contains(string(out), "k-1234567") || !strings.Contains(string(out), `"user":"bob"`) ||
		!strings.Contains(string(out), `"tokens_used":7`) {
		t.Errorf("data=%s", out)
	}
}

func BenchmarkSinkSetEmit_Redacted(b *testing.B) {
	inv, res, err := secretInvocation()
	set := SinkSet{{Sink: &sinkRecorder{}, OnOK: true, OnError: true}}
	blind := SinkSet{{Sink: &outputBlindSink{}, OnOK: true, OnError: true}}
	b.Run("with-output", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			set[0].Sink.(*sinkRecorder).calls = nil
			set.Emit(context.Background(), inv, res, err)
		}
	})
	b.Run("output-blind", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			blind[0].Sink.(*outputBlindSink).calls = nil
			blind.Emit(context.Background(), inv, res, err)
		}
	})
}

// WithAuditRedaction adds flags and content rules; it never removes
// the floor (the name check still masks --token).
func TestAuditRedaction_WithAuditRedactionAdds(t *testing.T) {
	root := &cobra.Command{Use: "app"}
	root.AddCommand(&cobra.Command{Use: "sync", RunE: func(*cobra.Command, []string) error { return nil }})
	rule, err := redact.NewRule("acme-token", `acme_[a-z0-9]{12}`, "")
	if err != nil {
		t.Fatal(err)
	}
	rec := &sinkRecorder{}
	b := New(root,
		WithRunner(&fakeRunner{run: func(context.Context, Invocation) (Result, error) {
			return Result{Stdout: "issued acme_abcdef123456\n"}, nil
		}}),
		WithSinks(SinkSpec{Sink: rec, OnOK: true, OnError: true}),
		WithAuditRedaction(AuditRedaction{SecretFlags: []string{"--conn"}}),
		WithAuditRedaction(AuditRedaction{Rules: []redact.Rule{rule}}),
	)
	b.Expose("sync", SurfaceREST)
	if _, err := b.Invoke(context.Background(), Invocation{
		Path:  []string{"sync"},
		Args:  []string{"acme_zyxwvu987654"},
		Flags: map[string]any{"conn": "host=db user=x", "token": tokenValue, "region": "eu"},
		Meta:  Meta{Surface: SurfaceREST},
	}); err != nil {
		t.Fatal(err)
	}
	got := rec.calls[0]
	if got.inv.Flags["conn"] != auditRedacted || got.inv.Flags["token"] != auditRedacted || got.inv.Flags["region"] != "eu" {
		t.Errorf("flags=%v", got.inv.Flags)
	}
	if got.inv.Args[0] != auditRedacted {
		t.Errorf("args=%v: extra rule not applied", got.inv.Args)
	}
	if strings.Contains(got.res.Stdout, "acme_abcdef123456") {
		t.Errorf("stdout=%q: extra rule not applied", got.res.Stdout)
	}

	// A refusal for an unknown path still carries the extra redaction.
	rec.calls = nil
	_, _ = b.Invoke(context.Background(), Invocation{
		Path: []string{"nope"}, Flags: map[string]any{"conn": "host=db user=x"}, Meta: Meta{Surface: SurfaceREST},
	})
	if len(rec.calls) != 1 || rec.calls[0].inv.Flags["conn"] != auditRedacted {
		t.Errorf("unknown-path refusal: %+v", rec.calls)
	}
}
