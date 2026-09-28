package cmdsurface

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	"hop.top/kit/go/security"
)

func openChain(tb testing.TB) (*security.AuditLog, string) {
	tb.Helper()
	path := filepath.Join(tb.TempDir(), "audit.chain")
	l, err := security.OpenAuditLog(path, security.AuditLogOptions{})
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = l.Close() })
	return l, path
}

func verifyChain(t *testing.T, path string) security.AuditReport {
	t.Helper()
	rep, err := security.VerifyAuditLog(path)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK() {
		t.Fatalf("chain broken: %v", rep.Break)
	}
	return rep
}

// The chain holds the redacted record: no secret reaches the file in
// clear, whichever field carried it.
func TestChainSink_StoresRedactedRecord(t *testing.T) {
	l, path := openChain(t)
	inv, res, err := secretInvocation()
	SinkSet{{Sink: &ChainSink{Log: l}, OnOK: true, OnError: true}}.Emit(context.Background(), inv, res, err)
	if e := l.Close(); e != nil {
		t.Fatal(e)
	}

	raw, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatal(rerr)
	}
	for _, s := range []string{tokenValue, passwordValue, ghToken, authzValue} {
		if strings.Contains(string(raw), s) {
			t.Errorf("secret %q in the chain file:\n%s", s, raw)
		}
	}
	for _, want := range []string{`"path":"db connect"`, `"token":"***REDACTED***"`, `"region":"eu-west-1"`, `"caller":"alice"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("chain record lacks %s:\n%s", want, raw)
		}
	}
	if strings.Contains(string(raw), `"stdout"`) {
		t.Errorf("chain record carries output:\n%s", raw)
	}
	if rep := verifyChain(t, path); rep.Records != 1 {
		t.Errorf("records=%d want 1", rep.Records)
	}
}

// Through a bridge, a flag the command marks secret is masked too: the
// bridge resolves the leaf before the record reaches the chain.
func TestChainSink_BridgeMasksAnnotatedFlag(t *testing.T) {
	root := &cobra.Command{Use: "app"}
	connect := &cobra.Command{Use: "connect", RunE: func(*cobra.Command, []string) error { return nil }}
	connect.Flags().String("dsn", "", "")
	if err := MarkFlagSecret(connect.Flags(), "dsn"); err != nil {
		t.Fatal(err)
	}
	root.AddCommand(connect)
	l, path := openChain(t)
	b := New(root,
		WithRunner(&fakeRunner{run: func(context.Context, Invocation) (Result, error) {
			return Result{Stdout: "connected with " + dsnValue + "\n"}, nil
		}}),
		WithSinks(SinkSpec{Sink: &ChainSink{Log: l}, OnOK: true, OnError: true}),
	)
	b.Expose("connect", SurfaceREST)
	if _, err := b.Invoke(context.Background(), Invocation{
		Path:  []string{"connect"},
		Flags: map[string]any{"dsn": dsnValue},
		Meta:  Meta{Surface: SurfaceREST, Caller: "alice"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), dsnValue) || !strings.Contains(string(raw), `"dsn":"***REDACTED***"`) {
		t.Errorf("dsn not masked in the chain:\n%s", raw)
	}
	verifyChain(t, path)
}

// Several services in one process share one ChainSink over one log;
// their concurrent audits form one unbroken chain.
func TestChainSink_ConcurrentServicesOneChain(t *testing.T) {
	l, path := openChain(t)
	sink := &ChainSink{Log: l}
	services := []SinkSet{
		{{Sink: sink, OnOK: true, OnError: true, Surfaces: []Surface{SurfaceREST}}},
		{{Sink: sink, OnOK: true, OnError: true, Surfaces: []Surface{SurfaceSocket}}},
		{{Sink: sink, OnOK: true, OnError: true}},
	}
	surfaces := []Surface{SurfaceREST, SurfaceSocket, SurfaceMCP}
	const each = 100
	var wg sync.WaitGroup
	for i, set := range services {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := range each {
				inv := Invocation{
					Path:  []string{"item", "add"},
					Args:  []string{fmt.Sprintf("w%d-%d", i, n)},
					Flags: map[string]any{"token": tokenValue},
					Meta:  Meta{Surface: surfaces[i]},
				}
				if errs := set.Emit(context.Background(), inv, Result{}, nil); errs != nil {
					t.Error(errs)
				}
			}
		}()
	}
	wg.Wait()
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if rep := verifyChain(t, path); rep.Records != uint64(len(services)*each) {
		t.Errorf("records=%d want %d", rep.Records, len(services)*each)
	}
}

func TestChainSink_NoLog(t *testing.T) {
	if err := (&ChainSink{}).Emit(context.Background(), Invocation{}, Result{}, nil); err == nil {
		t.Error("Emit without a log: want error")
	}
}

// The chain sink never reads output, so the audit pipeline skips
// redacting it when the chain is the only sink.
func TestChainSink_OutputBlind(t *testing.T) {
	var s Sink = &ChainSink{}
	blind, ok := s.(auditOutputBlind)
	if !ok || !blind.auditIgnoresOutput() {
		t.Error("ChainSink must be output-blind")
	}
}

// BenchmarkAuditSinkEmit compares the chain sink with the file sink on
// the audit hot path (SinkSet.Emit, redaction included), both writing
// to a real file without fsync. The difference is the chain's hashing
// and its richer record (args and flags).
func BenchmarkAuditSinkEmit(b *testing.B) {
	inv, res, err := secretInvocation()
	b.Run("file", func(b *testing.B) {
		f, ferr := os.Create(filepath.Join(b.TempDir(), "audit.jsonl"))
		if ferr != nil {
			b.Fatal(ferr)
		}
		defer f.Close()
		set := SinkSet{{Sink: &FileSink{W: f}, OnOK: true, OnError: true}}
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			set.Emit(context.Background(), inv, res, err)
		}
	})
	b.Run("chain", func(b *testing.B) {
		l, _ := openChain(b)
		set := SinkSet{{Sink: &ChainSink{Log: l}, OnOK: true, OnError: true}}
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			set.Emit(context.Background(), inv, res, err)
		}
	})
}

// BenchmarkAuditSinkEmitDirect isolates the sinks themselves: Emit
// called directly, without the redaction SinkSet.Emit runs first.
func BenchmarkAuditSinkEmitDirect(b *testing.B) {
	inv, res, err := secretInvocation()
	ctx := context.Background()
	b.Run("file", func(b *testing.B) {
		f, ferr := os.Create(filepath.Join(b.TempDir(), "audit.jsonl"))
		if ferr != nil {
			b.Fatal(ferr)
		}
		defer f.Close()
		sink := &FileSink{W: f}
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			_ = sink.Emit(ctx, inv, res, err)
		}
	})
	// The file sink writing the chain's record: what remains between
	// this and "chain" is the chain itself (hash, seq, prev).
	b.Run("file-chain-record", func(b *testing.B) {
		f, ferr := os.Create(filepath.Join(b.TempDir(), "audit.jsonl"))
		if ferr != nil {
			b.Fatal(ferr)
		}
		defer f.Close()
		sink := &FileSink{W: f, Format: func(inv Invocation, res Result, err error) ([]byte, error) {
			rec := chainRecord{
				Path: joinPath(inv.Path), Surface: string(inv.Meta.Surface), ExitCode: res.ExitCode,
				Caller: inv.Meta.Caller, RequestID: inv.Meta.RequestID, RequestedAt: inv.Meta.RequestedAt.UTC(),
				Args: inv.Args, Flags: inv.Flags,
			}
			if err != nil {
				rec.Error = err.Error()
			}
			return json.Marshal(rec)
		}}
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			_ = sink.Emit(ctx, inv, res, err)
		}
	})
	b.Run("chain", func(b *testing.B) {
		l, _ := openChain(b)
		sink := &ChainSink{Log: l}
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			_ = sink.Emit(ctx, inv, res, err)
		}
	})
}
