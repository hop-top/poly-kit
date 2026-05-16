package cmdsurface

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// sinkRecorder is a test Sink that captures every Emit call.
type sinkRecorder struct {
	mu    sync.Mutex
	calls []sinkRecord
	err   error // returned from Emit
}

type sinkRecord struct {
	inv Invocation
	res Result
	err error
}

func (r *sinkRecorder) Emit(_ context.Context, inv Invocation, res Result, err error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, sinkRecord{inv, res, err})
	return r.err
}

func (r *sinkRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func sinkInv(surface Surface, path ...string) Invocation {
	return Invocation{Path: path, Meta: Meta{Surface: surface}}
}

func TestSinkSet_OnErrorOnOKMatrix(t *testing.T) {
	cases := []struct {
		name     string
		spec     SinkSpec
		res      Result
		err      error
		wantEmit bool
	}{
		{"OnOK ok", SinkSpec{OnOK: true}, Result{ExitCode: 0}, nil, true},
		{"OnOK err", SinkSpec{OnOK: true}, Result{ExitCode: 0}, errors.New("x"), false},
		{"OnOK exit", SinkSpec{OnOK: true}, Result{ExitCode: 2}, nil, false},
		{"OnErr ok", SinkSpec{OnError: true}, Result{ExitCode: 0}, nil, false},
		{"OnErr err", SinkSpec{OnError: true}, Result{ExitCode: 0}, errors.New("x"), true},
		{"OnErr ex", SinkSpec{OnError: true}, Result{ExitCode: 1}, nil, true},
		{"both ok", SinkSpec{OnOK: true, OnError: true}, Result{ExitCode: 0}, nil, true},
		{"both err", SinkSpec{OnOK: true, OnError: true}, Result{ExitCode: 0}, errors.New("x"), true},
		{"neither", SinkSpec{}, Result{ExitCode: 0}, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &sinkRecorder{}
			tc.spec.Sink = rec
			set := SinkSet{tc.spec}
			set.Emit(context.Background(), sinkInv(SurfaceCLI, "x"), tc.res, tc.err)
			if got := rec.count() > 0; got != tc.wantEmit {
				t.Fatalf("emit=%v want=%v", got, tc.wantEmit)
			}
		})
	}
}

func TestSinkSet_SurfaceFilter(t *testing.T) {
	rec := &sinkRecorder{}
	set := SinkSet{{
		Sink:     rec,
		OnOK:     true,
		Surfaces: []Surface{SurfaceREST, SurfaceMCP},
	}}
	ctx := context.Background()
	set.Emit(ctx, sinkInv(SurfaceCLI, "a"), Result{}, nil)
	set.Emit(ctx, sinkInv(SurfaceREST, "a"), Result{}, nil)
	set.Emit(ctx, sinkInv(SurfaceMCP, "a"), Result{}, nil)
	set.Emit(ctx, sinkInv(SurfaceRPC, "a"), Result{}, nil)
	if rec.count() != 2 {
		t.Fatalf("got %d calls, want 2", rec.count())
	}
}

func TestSinkSet_PathPatterns(t *testing.T) {
	cases := []struct {
		pattern string
		path    []string
		want    bool
	}{
		{"*", []string{"anything"}, true},
		{"*", []string{}, true},
		{"widget add", []string{"widget", "add"}, true},
		{"widget add", []string{"widget", "rm"}, false},
		{"widget *", []string{"widget", "add"}, true},
		{"widget *", []string{"widget", "deep", "sub"}, true},
		{"widget *", []string{"report"}, false},
		{"report.purge", []string{"report", "purge"}, true},
		{"report.*", []string{"report", "purge", "all"}, true},
		{"", []string{"x"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.pattern+"_"+joinPath(tc.path), func(t *testing.T) {
			if got := sinkMatchPath(tc.pattern, tc.path); got != tc.want {
				t.Fatalf("sinkMatchPath(%q,%v)=%v want=%v",
					tc.pattern, tc.path, got, tc.want)
			}
		})
	}
}

func TestSinkSet_PathsFilter(t *testing.T) {
	rec := &sinkRecorder{}
	set := SinkSet{{
		Sink:  rec,
		OnOK:  true,
		Paths: []string{"widget *", "report.purge"},
	}}
	ctx := context.Background()
	set.Emit(ctx, sinkInv(SurfaceCLI, "widget", "add"), Result{}, nil)
	set.Emit(ctx, sinkInv(SurfaceCLI, "report", "purge"), Result{}, nil)
	set.Emit(ctx, sinkInv(SurfaceCLI, "report", "list"), Result{}, nil)
	if rec.count() != 2 {
		t.Fatalf("got %d calls, want 2", rec.count())
	}
}

func TestSinkSet_ErrorsCollectedNotShortCircuited(t *testing.T) {
	boom := errors.New("boom")
	a := &sinkRecorder{err: boom}
	b := &sinkRecorder{}
	c := &sinkRecorder{err: boom}
	set := SinkSet{
		{Sink: a, OnOK: true},
		{Sink: b, OnOK: true},
		{Sink: c, OnOK: true},
	}
	errs := set.Emit(context.Background(), sinkInv(SurfaceCLI, "x"), Result{}, nil)
	if len(errs) != 2 {
		t.Fatalf("got %d errs, want 2", len(errs))
	}
	if a.count() != 1 || b.count() != 1 || c.count() != 1 {
		t.Fatalf("all sinks should have been called once: a=%d b=%d c=%d",
			a.count(), b.count(), c.count())
	}
}

func TestSinkSet_NilSinkSkipped(t *testing.T) {
	rec := &sinkRecorder{}
	set := SinkSet{
		{Sink: nil, OnOK: true},
		{Sink: rec, OnOK: true},
	}
	errs := set.Emit(context.Background(), sinkInv(SurfaceCLI, "x"), Result{}, nil)
	if errs != nil {
		t.Fatalf("unexpected errs: %v", errs)
	}
	if rec.count() != 1 {
		t.Fatalf("recorder call count = %d, want 1", rec.count())
	}
}

func TestSinkSet_EmptyReturnsNil(t *testing.T) {
	var set SinkSet
	if errs := set.Emit(context.Background(), sinkInv(SurfaceCLI, "x"), Result{}, nil); errs != nil {
		t.Fatalf("empty set should return nil, got %v", errs)
	}
}
