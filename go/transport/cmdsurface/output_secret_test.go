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
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"hop.top/kit/go/ai/cmdreflect"
	"hop.top/kit/go/console/cli/idemstore"
	"hop.top/kit/go/runtime/telemetry"
	"hop.top/kit/go/security"
	"hop.top/kit/go/storage/kv/memory"
)

// Values chosen to match no content rule, so a hit in a record is the
// declaration's doing or its absence, never the default corpus.
const (
	osValue   = "kestrel-orbit-7731"
	osNested  = "heron-glacier-4096"
	osRow1    = "ibis-meadow-0001"
	osRow2    = "ibis-meadow-0002"
	osText    = "osprey-canyon-5512"
	osAllVals = osValue + osNested + osRow1 + osRow2 + osText
)

const osSchema = `{"type":"object","properties":{` +
	`"value":{"type":"string"},"note":{"type":"string"},` +
	`"detail":{"type":"object","properties":{"value":{"type":"string"}}},` +
	`"rows":{"type":"array","items":{"type":"object","properties":{` +
	`"number":{"type":"string"},"label":{"type":"string"}}}}}}`

// osLeaf builds a read leaf with the shared output schema.
func osLeaf(use string, secret func(*cobra.Command) error) *cobra.Command {
	c := &cobra.Command{
		Use: use,
		Annotations: map[string]string{
			"kit/side-effect":           "read",
			"kit/output-schema":         osSchema,
			"kit/output-schema-version": "1.0",
		},
		RunE: func(*cobra.Command, []string) error { return nil },
	}
	if secret != nil {
		if err := secret(c); err != nil {
			panic(err)
		}
	}
	return c
}

// osTree has:
//
//	reveal  fields value, number secret
//	plain   same output, nothing declared (the control)
//	text    whole output secret, no schema
//	fails   like reveal; the runner fails after producing the value
//	typo    declares a field the schema lacks
func osTree() *cobra.Command {
	root := &cobra.Command{Use: "tool"}
	reveal := osLeaf("reveal", func(c *cobra.Command) error { return MarkOutputSecret(c, "value", "number") })
	plain := osLeaf("plain", nil)
	fails := osLeaf("fails", func(c *cobra.Command) error { return MarkOutputSecret(c, "value") })
	typo := osLeaf("typo", nil)
	typo.Annotations[AnnotationSecretOutput] = `["valeu"]`
	text := &cobra.Command{
		Use:         "text",
		Annotations: map[string]string{"kit/side-effect": "read"},
		RunE:        func(*cobra.Command, []string) error { return nil },
	}
	if err := MarkOutputSecret(text); err != nil {
		panic(err)
	}
	root.AddCommand(reveal, plain, fails, typo, text)
	return root
}

// osData is the structured result of reveal, plain and fails.
func osData() map[string]any {
	return map[string]any{
		"value":  osValue,
		"note":   "issued",
		"detail": map[string]any{"value": osNested},
		"rows": []any{
			map[string]any{"number": osRow1, "label": "first"},
			map[string]any{"number": osRow2, "label": "second"},
		},
	}
}

// osRunner returns the same output for every leaf, as text and as data,
// and streams the text as it goes.
type osRunner struct {
	mu    sync.Mutex
	calls int
}

func (r *osRunner) result(inv Invocation) (Result, error) {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	switch inv.Path[0] {
	case "text":
		return Result{Stdout: "passport " + osText + "\n", Data: map[string]any{"passport": osText}}, nil
	case "fails":
		return Result{
			ExitCode: 1,
			Stderr:   "decrypted " + osValue + " then failed\n",
		}, fmt.Errorf("after decrypting %s: write refused", osValue)
	}
	return Result{
		Stdout: "value: " + osValue + "\nnested: " + osNested + "\n",
		Stderr: "note: " + osRow1 + "\n",
		Data:   osData(),
	}, nil
}

func (r *osRunner) Run(_ context.Context, inv Invocation) (Result, error) { return r.result(inv) }

func (r *osRunner) Stream(_ context.Context, inv Invocation, out chan<- Event) error {
	defer close(out)
	res, err := r.result(inv)
	for _, line := range strings.Split(strings.TrimSuffix(res.Stdout, "\n"), "\n") {
		out <- Event{Kind: "stdout", Data: line, At: time.Now()}
	}
	out <- Event{Kind: "done", Data: &res, At: time.Now()}
	return err
}

// osSafe is a lock-guarded buffer.
type osSafe struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *osSafe) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *osSafe) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// osPublisher records every payload a BusSink publishes, as JSON.
type osPublisher struct{ out osSafe }

func (p *osPublisher) Publish(_ context.Context, topic, source string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(&p.out, "%s %s %s\n", topic, source, raw)
	return nil
}

// osRecorder is a custom sink that keeps everything it is given.
type osRecorder struct{ out osSafe }

func (r *osRecorder) Emit(_ context.Context, inv Invocation, res Result, err error) error {
	rec := struct {
		Inv Invocation
		Res Result
		Err string
	}{inv, res, ""}
	if err != nil {
		rec.Err = err.Error()
	}
	raw, merr := json.Marshal(rec)
	if merr != nil {
		return merr
	}
	_, _ = r.out.Write(append(raw, '\n'))
	return nil
}

// osRig is every sink type the toolkit ships plus a recording custom
// sink, and the means to read back every byte they wrote.
type osRig struct {
	t         *testing.T
	specs     []SinkSpec
	chainPath string
	chain     *security.AuditLog
	logBuf    osSafe
	fileBuf   osSafe
	bus       osPublisher
	rec       osRecorder
	tele      *TelemetrySink
	teleEm    *stubEmitter
	hookMu    sync.Mutex
	hookBody  bytes.Buffer
}

func newOSRig(t *testing.T) *osRig {
	t.Helper()
	r := &osRig{t: t}
	r.chainPath = filepath.Join(t.TempDir(), "audit.chain")
	l, err := security.OpenAuditLog(r.chainPath, security.AuditLogOptions{})
	if err != nil {
		t.Fatal(err)
	}
	r.chain = l
	t.Cleanup(func() { _ = l.Close() })

	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		raw, _ := io.ReadAll(req.Body)
		r.hookMu.Lock()
		r.hookBody.Write(raw)
		r.hookBody.WriteByte('\n')
		r.hookMu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(hook.Close)

	r.teleEm = &stubEmitter{}
	r.tele, err = NewTelemetrySink(WithEmitter(r.teleEm), WithMode(telemetry.ModeFull))
	if err != nil {
		t.Fatal(err)
	}

	logger := slog.NewJSONHandler(&r.logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})
	everything := func(inv Invocation, res Result, err error) ([]byte, error) {
		rec := struct {
			Inv Invocation
			Res Result
			Err string
		}{inv, res, ""}
		if err != nil {
			rec.Err = err.Error()
		}
		return json.Marshal(rec)
	}
	spec := func(s Sink) SinkSpec { return SinkSpec{Sink: s, OnOK: true, OnError: true} }
	r.specs = []SinkSpec{
		spec(&ChainSink{Log: l}),
		spec(&LogSink{Handler: logger, Level: slog.LevelDebug}),
		spec(&FileSink{W: &r.fileBuf, Format: everything}),
		spec(&WebhookSink{URL: hook.URL}),
		spec(&BusSink{Publisher: &r.bus, Topic: "audit"}),
		spec(r.tele),
		spec(&r.rec),
	}
	return r
}

// bytesWritten is every byte the rig's sinks wrote, flushed.
func (r *osRig) bytesWritten() string {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := r.tele.Close(ctx); err != nil {
		r.t.Fatalf("telemetry close: %v", err)
	}
	if err := r.chain.Close(); err != nil {
		r.t.Fatalf("chain close: %v", err)
	}
	chain, err := os.ReadFile(r.chainPath)
	if err != nil {
		r.t.Fatal(err)
	}
	events, _ := json.Marshal(r.teleEm.events())
	r.hookMu.Lock()
	hook := r.hookBody.String()
	r.hookMu.Unlock()
	return strings.Join([]string{
		string(chain), r.logBuf.String(), r.fileBuf.String(), hook,
		r.bus.out.String(), string(events), r.rec.out.String(),
	}, "\n")
}

// perSink is each sink's bytes by name, for a failure message.
func (r *osRig) perSink() map[string]string {
	events, _ := json.Marshal(r.teleEm.events())
	chain, _ := os.ReadFile(r.chainPath)
	r.hookMu.Lock()
	defer r.hookMu.Unlock()
	return map[string]string{
		"chain": string(chain), "log": r.logBuf.String(), "file": r.fileBuf.String(),
		"webhook": r.hookBody.String(), "bus": r.bus.out.String(),
		"telemetry": string(events), "recorder": r.rec.out.String(),
	}
}

// osBridge serves osTree over REST with the rig's sinks.
func osBridge(t *testing.T, r *osRig, run Runner) *Bridge {
	t.Helper()
	b := New(osTree(), WithRunner(run), WithSinks(r.specs...))
	b.Expose("*", SurfaceREST)
	return b
}

func osInv(path string) Invocation {
	return Invocation{Path: []string{path}, Meta: Meta{Surface: SurfaceREST, Caller: "alice", RequestID: "req-1"}}
}

func assertNone(t *testing.T, rig *osRig, what string, values ...string) {
	t.Helper()
	all := rig.bytesWritten()
	per := rig.perSink()
	for _, v := range values {
		if strings.Contains(all, v) {
			for name, b := range per {
				if strings.Contains(b, v) {
					t.Errorf("%s: %q reached the %s sink:\n%s", what, v, name, b)
				}
			}
		}
	}
}

// The harness sees a leak: a leaf declaring nothing leaves the value in
// every record that keeps output, which is what the declaration fixes.
func TestSecretOutput_ControlLeaksWithoutDeclaration(t *testing.T) {
	rig := newOSRig(t)
	b := osBridge(t, rig, &osRunner{})
	if _, err := b.Invoke(context.Background(), osInv("plain")); err != nil {
		t.Fatal(err)
	}
	all := rig.bytesWritten()
	for _, sink := range []string{"log", "file", "webhook", "bus", "recorder"} {
		if !strings.Contains(rig.perSink()[sink], osNested) {
			t.Errorf("control: the %s sink should hold the undeclared value", sink)
		}
	}
	if !strings.Contains(all, osNested) {
		t.Fatal("control: no sink held the value; the checks below prove nothing")
	}
}

// Structured output: the declared fields are masked at every depth,
// text is not kept, the caller's result is the command's own.
func TestSecretOutput_RecordsHoldNoValue(t *testing.T) {
	rig := newOSRig(t)
	b := osBridge(t, rig, &osRunner{})
	res, err := b.Invoke(context.Background(), osInv("reveal"))
	if err != nil {
		t.Fatal(err)
	}

	// The response to the caller is untouched.
	if !reflect.DeepEqual(res.Data, osData()) {
		t.Errorf("caller Data changed: %v", res.Data)
	}
	if want := "value: " + osValue + "\nnested: " + osNested + "\n"; res.Stdout != want {
		t.Errorf("caller Stdout=%q want %q", res.Stdout, want)
	}
	if res.Stderr != "note: "+osRow1+"\n" {
		t.Errorf("caller Stderr=%q", res.Stderr)
	}

	// Records hold none of the declared values, nor any text.
	assertNone(t, rig, "reveal", osValue, osNested, osRow1, osRow2)
	// What is not declared still reads: the record stays useful.
	if got := rig.rec.out.String(); !strings.Contains(got, `"note":"issued"`) ||
		!strings.Contains(got, `"label":"first"`) || !strings.Contains(got, auditRedacted) {
		t.Errorf("recorder lost the undeclared fields or the marker: %s", got)
	}
}

// A command that prints the value as text declares the whole output.
func TestSecretOutput_WholeOutputSecret(t *testing.T) {
	rig := newOSRig(t)
	b := osBridge(t, rig, &osRunner{})
	res, err := b.Invoke(context.Background(), osInv("text"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Stdout, osText) {
		t.Fatalf("caller lost the value: %q", res.Stdout)
	}
	assertNone(t, rig, "text", osText)
}

// A failure after the value was produced: the error text and stderr
// quote it, and records still hold none.
func TestSecretOutput_ErrorPathHoldsNoValue(t *testing.T) {
	rig := newOSRig(t)
	b := osBridge(t, rig, &osRunner{})
	res, err := b.Invoke(context.Background(), osInv("fails"))
	if err == nil || !strings.Contains(err.Error(), osValue) {
		t.Fatalf("caller's error was altered: %v", err)
	}
	if !strings.Contains(res.Stderr, osValue) {
		t.Fatalf("caller's stderr was altered: %q", res.Stderr)
	}
	assertNone(t, rig, "fails", osValue)
}

// Streamed lines reach the caller whole; the record kept after the
// stream holds none of them.
func TestSecretOutput_StreamRecordsHoldNoValue(t *testing.T) {
	rig := newOSRig(t)
	b := osBridge(t, rig, &osRunner{})
	adm, err := b.Admit(context.Background(), osInv("reveal"))
	if err != nil {
		t.Fatal(err)
	}
	out := make(chan Event, 16)
	errc := make(chan error, 1)
	go func() { errc <- adm.Stream(context.Background(), out) }()
	var seen strings.Builder
	var done *Result
	for ev := range out {
		if s, ok := ev.Data.(string); ok {
			seen.WriteString(s)
		}
		if r, ok := ev.Data.(*Result); ok {
			done = r
		}
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(seen.String(), osValue) || done == nil || !reflect.DeepEqual(done.Data, osData()) {
		t.Fatalf("caller's stream was altered: lines=%q done=%+v", seen.String(), done)
	}
	assertNone(t, rig, "stream", osValue, osNested, osRow1, osRow2)
}

// A sink handed the raw result cannot see it: SinkSet.Emit redacts
// before any sink, a custom one included, receives anything.
func TestSecretOutput_CustomSinkReceivesRedactedForm(t *testing.T) {
	var got Result
	var gotErr error
	custom := sinkFunc(func(_ context.Context, _ Invocation, res Result, err error) error {
		got, gotErr = res, err
		return nil
	})
	b := New(osTree(), WithRunner(&osRunner{}), WithSinks(SinkSpec{Sink: custom, OnOK: true, OnError: true}))
	b.Expose("*", SurfaceREST)
	if _, err := b.Invoke(context.Background(), osInv("fails")); err == nil {
		t.Fatal("want the command's error back")
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), osValue) || strings.Contains(gotErr.Error(), osValue) {
		t.Errorf("custom sink saw the value: %s / %v", raw, gotErr)
	}
	if !errors.Is(gotErr, gotErr) || gotErr == nil {
		t.Fatal("custom sink lost the error")
	}
}

type sinkFunc func(context.Context, Invocation, Result, error) error

func (f sinkFunc) Emit(ctx context.Context, inv Invocation, res Result, err error) error {
	return f(ctx, inv, res, err)
}

// The audit scope is the leaf's: a leaf declaring nothing is not
// masked because a sibling declares.
func TestSecretOutput_DeclarationIsPerLeaf(t *testing.T) {
	rig := newOSRig(t)
	b := osBridge(t, rig, &osRunner{})
	if _, err := b.Invoke(context.Background(), osInv("reveal")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Invoke(context.Background(), osInv("plain")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rig.rec.out.String(), osNested) {
		t.Error("plain leaf's record should still carry its value")
	}
}

// The content rule that masks by key name keeps working beside it.
func TestSecretOutput_NameRuleStillMasks(t *testing.T) {
	rig := newOSRig(t)
	run := &dataRunner{data: map[string]any{
		"value": osValue, "note": "ok", "api_token": "tok-" + osNested,
	}}
	b := New(osTree(), WithRunner(run), WithSinks(rig.specs...))
	b.Expose("*", SurfaceREST)
	if _, err := b.Invoke(context.Background(), osInv("reveal")); err != nil {
		t.Fatal(err)
	}
	assertNone(t, rig, "name rule", osValue, "tok-"+osNested)
	if !strings.Contains(rig.rec.out.String(), `"note":"ok"`) {
		t.Error("undeclared, unnamed field should read")
	}
}

type dataRunner struct{ data map[string]any }

func (d *dataRunner) Run(context.Context, Invocation) (Result, error) {
	return Result{Data: d.data, Stdout: "x"}, nil
}

func (d *dataRunner) Stream(_ context.Context, _ Invocation, out chan<- Event) error {
	defer close(out)
	out <- Event{Kind: "done", Data: &Result{Data: d.data}, At: time.Now()}
	return nil
}

// A secret-output leaf is not kept by the result cache or the
// idempotency ledger: either would hold a copy of the answer.
func TestSecretOutput_NotCachedNotLedgered(t *testing.T) {
	root := osTree()
	for _, c := range root.Commands() {
		if c.Name() == "reveal" || c.Name() == "plain" {
			c.Annotations[AnnotationCacheTTL] = "1m"
		}
	}
	for _, name := range []string{"reveal", "plain"} {
		want := map[string]int{"reveal": 2, "plain": 1}[name]
		run := &osRunner{}
		b := New(root, WithRunner(run), WithResultCache(memory.New()))
		b.Expose("*", SurfaceREST)
		for range 2 {
			if _, err := b.Invoke(context.Background(), osInv(name)); err != nil {
				t.Fatal(err)
			}
		}
		if run.calls != want {
			t.Errorf("%s ran %d times, want %d (cache)", name, run.calls, want)
		}
	}
}

// A cron job has no caller; its result callback is a record.
func TestSecretOutput_CronCallbackIsRedacted(t *testing.T) {
	b := New(osTree(), WithRunner(&osRunner{}))
	b.Expose("*", SurfaceCron)
	var got []Result
	job := makeCronJob(b, CronSchedule{Path: []string{"reveal"}}, cronConfig{
		ctx:    context.Background(),
		logger: func(string, ...any) {},
		sink:   func(_ CronSchedule, r Result, _ error) { got = append(got, r) },
	})
	job()
	job2 := makeCronJob(b, CronSchedule{Path: []string{"plain"}}, cronConfig{
		ctx:    context.Background(),
		logger: func(string, ...any) {},
		sink:   func(_ CronSchedule, r Result, _ error) { got = append(got, r) },
	})
	job2()
	if len(got) != 2 {
		t.Fatalf("callbacks=%d", len(got))
	}
	raw, _ := json.Marshal(got[0])
	if strings.Contains(string(raw), osValue) || strings.Contains(string(raw), osRow1) {
		t.Errorf("cron callback saw the value: %s", raw)
	}
	if !reflect.DeepEqual(got[1].Data, osData()) {
		t.Errorf("an undeclared leaf's cron result changed: %v", got[1].Data)
	}
}

// A declaration naming a field the schema lacks is a registration-time
// error, never a silent gap.
func TestSecretOutput_TypoIsRefusedAtRegistration(t *testing.T) {
	c := osLeaf("x", nil)
	err := MarkOutputSecret(c, "valeu")
	if err == nil || !strings.Contains(err.Error(), "valeu") {
		t.Fatalf("MarkOutputSecret(typo)=%v, want an error naming the field", err)
	}
	if _, set := c.Annotations[AnnotationSecretOutput]; set {
		t.Error("a refused declaration left its annotation behind")
	}
	if err := MarkOutputSecret(c, "value"); err != nil {
		t.Errorf("MarkOutputSecret(value)=%v", err)
	}
	if err := MarkOutputSecret(c, "number"); err != nil {
		t.Errorf("a field nested in a list is declared: %v", err)
	}

	// Declared before the schema: the bridge refuses the command.
	root := osTree()
	rig := newOSRig(t)
	b := New(root, WithRunner(&osRunner{}), WithSinks(rig.specs...))
	b.Expose("*", SurfaceREST)
	for _, l := range b.Leaves() {
		if l.PathKey() == "typo" {
			t.Fatal("a leaf with an undeclared secret field is invocable")
		}
	}
	var found bool
	for _, d := range b.NonInvocable() {
		if d.Cmd != nil && d.Cmd.Name() == "typo" {
			found = true
			if d.Reason != cmdreflect.ReasonMalformedSchema {
				t.Errorf("typo reason=%q want malformed-schema", d.Reason)
			}
		}
	}
	if !found {
		t.Error("typo leaf is not listed as non-invocable")
	}
	if _, err := b.Invoke(context.Background(), osInv("typo")); err == nil {
		t.Error("typo leaf ran")
	}
}

// Unreadable declarations are refused too.
func TestSecretOutput_UnreadableDeclarationIsRefused(t *testing.T) {
	for _, v := range []string{"", "value", `[]`, `[""]`, `{"value":1}`} {
		root := &cobra.Command{Use: "tool"}
		c := osLeaf("x", nil)
		c.Annotations[AnnotationSecretOutput] = v
		root.AddCommand(c)
		b := New(root)
		if len(b.Leaves()) != 0 {
			t.Errorf("declaration %q left the leaf invocable", v)
		}
	}
}

// The leaf reports what it declares.
func TestSecretOutput_LeafReportsDeclaration(t *testing.T) {
	b := New(osTree(), WithRunner(&osRunner{}))
	for _, l := range b.Leaves() {
		all, fields := l.SecretOutput()
		switch l.PathKey() {
		case "reveal":
			if all || !reflect.DeepEqual(fields, []string{"number", "value"}) {
				t.Errorf("reveal: all=%v fields=%v", all, fields)
			}
		case "text":
			if !all {
				t.Error("text: want whole output")
			}
		case "plain":
			if all || fields != nil {
				t.Errorf("plain declares %v %v", all, fields)
			}
		}
	}
}

// A whole-output declaration keeps no structured data either.
func TestSecretOutput_WholeOutputKeepsNoData(t *testing.T) {
	rig := newOSRig(t)
	b := osBridge(t, rig, &osRunner{})
	if _, err := b.Invoke(context.Background(), osInv("text")); err != nil {
		t.Fatal(err)
	}
	if got := rig.rec.out.String(); strings.Contains(got, "passport") && strings.Contains(got, osText) {
		t.Errorf("recorder kept data of a whole-output secret: %s", got)
	}
}

// A value too short for substitution is still masked where it sits.
func TestSecretOutput_ShortValueMaskedByField(t *testing.T) {
	rig := newOSRig(t)
	run := &dataRunner{data: map[string]any{"value": "x9", "note": "kept"}}
	b := New(osTree(), WithRunner(run), WithSinks(rig.specs...))
	b.Expose("*", SurfaceREST)
	if _, err := b.Invoke(context.Background(), osInv("reveal")); err != nil {
		t.Fatal(err)
	}
	got := rig.rec.out.String()
	if strings.Contains(got, `"x9"`) || !strings.Contains(got, `"note":"kept"`) {
		t.Errorf("short declared value not masked in place: %s", got)
	}
}

// A declared value quoted in an error message or an echoed argument
// is replaced there too, wherever the record carries it.
func TestSecretOutput_ValueEchoedElsewhereIsSubstituted(t *testing.T) {
	rig := newOSRig(t)
	b := osBridge(t, rig, &osRunner{})
	inv := osInv("reveal")
	inv.Args = []string{"lookup", osValue}
	inv.Meta.Extra = map[string]string{"note": "for " + osNested}
	b.Audit(context.Background(), inv, Result{Data: osData()}, fmt.Errorf("could not store %s", osValue))
	assertNone(t, rig, "echo", osValue, osNested, osRow1)
}

// The idempotency ledger records a leaf that declares nothing and
// never one that declares secret output: a retry under its key runs
// again instead of replaying a stored copy of the value.
func TestSecretOutput_NotLedgered(t *testing.T) {
	for name, want := range map[string]struct {
		runs     int
		replayed bool
	}{"plain": {1, true}, "reveal": {2, false}} {
		run := &osRunner{}
		b := New(osTree(), WithRunner(run),
			WithIdempotency(NewIdempotencyLedger(idemstore.Memory()), time.Hour))
		b.Expose("*", SurfaceREST)
		inv := osInv(name)
		inv.Meta.IdempotencyKey = "k-1"
		inv.Meta.Caller = "alice"
		inv.Meta.Established = EstablishedVerified
		var res Result
		for range 2 {
			var err error
			if res, err = b.Invoke(context.Background(), inv); err != nil {
				t.Fatal(err)
			}
		}
		if run.calls != want.runs || res.Replayed != want.replayed {
			t.Errorf("%s: runs=%d replayed=%v, want %d %v", name, run.calls, res.Replayed, want.runs, want.replayed)
		}
	}
}

// The in-process runner decodes a declared output into Data and keeps
// the text on stdout when the caller names a rendering; either way the
// records hold none of it and the caller gets what the command wrote.
func TestSecretOutput_InProcessRunner(t *testing.T) {
	root := &cobra.Command{Use: "tool"}
	root.PersistentFlags().String("format", "", "output format")
	leaf := osLeaf("reveal", func(c *cobra.Command) error { return MarkOutputSecret(c, "value", "number") })
	leaf.RunE = func(cmd *cobra.Command, _ []string) error {
		if f, _ := cmd.Flags().GetString("format"); f == "json" {
			raw, _ := json.Marshal(osData())
			cmd.Println(string(raw))
			return nil
		}
		cmd.Println("value: " + osValue + " detail " + osNested + " " + osRow1 + " " + osRow2)
		return nil
	}
	root.AddCommand(leaf)

	for name, flags := range map[string]map[string]any{
		"structured": nil,
		"text":       {"format": "table"},
	} {
		rig := newOSRig(t)
		b := New(root, WithSinks(rig.specs...))
		b.Expose("*", SurfaceREST)
		inv := osInv("reveal")
		inv.Flags = flags
		res, err := b.Invoke(context.Background(), inv)
		if err != nil {
			t.Fatal(name, err)
		}
		raw, _ := json.Marshal(res)
		if !strings.Contains(string(raw), osValue) || !strings.Contains(string(raw), osRow2) {
			t.Errorf("%s: caller lost the value: %s", name, raw)
		}
		if name == "structured" && res.Data == nil {
			t.Error("structured: Data not decoded")
		}
		assertNone(t, rig, name, osValue, osNested, osRow1, osRow2)
	}
}
