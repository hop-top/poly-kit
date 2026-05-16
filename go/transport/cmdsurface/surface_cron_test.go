package cmdsurface_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"hop.top/kit/go/transport/cmdsurface"
)

// cronFakeRunner records invocations and (optionally) returns a
// configured error. It is the synchronous boundary the cron tests
// assert against — bridge.Invoke calls Run, the tests inspect the
// captured Invocation.
type cronFakeRunner struct {
	mu  sync.Mutex
	got []cmdsurface.Invocation

	RunFn func(ctx context.Context, inv cmdsurface.Invocation) (cmdsurface.Result, error)
}

func (f *cronFakeRunner) Run(ctx context.Context, inv cmdsurface.Invocation) (cmdsurface.Result, error) {
	f.mu.Lock()
	f.got = append(f.got, inv)
	f.mu.Unlock()
	if f.RunFn != nil {
		return f.RunFn(ctx, inv)
	}
	return cmdsurface.Result{Stdout: strings.Join(inv.Path, " ")}, nil
}

func (f *cronFakeRunner) Stream(context.Context, cmdsurface.Invocation, chan<- cmdsurface.Event) error {
	return errors.New("cronFakeRunner: Stream not supported")
}

func (f *cronFakeRunner) captured() []cmdsurface.Invocation {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]cmdsurface.Invocation, len(f.got))
	copy(out, f.got)
	return out
}

// cronTestEngine is a synchronous CronEngine for behavioral tests.
// Jobs registered with Schedule fire only when TriggerAll is called.
// Schedule, Start, Stop, and cancel funcs are all safe for concurrent
// use by tests but assume sequential invocation in practice.
type cronTestEngine struct {
	mu        sync.Mutex
	jobs      map[int]*cronTestJob
	next      int
	started   bool
	stopped   bool
	scheduled []cronTestSchedule
}

type cronTestJob struct {
	expr string
	tz   *time.Location
	fn   func()
}

type cronTestSchedule struct {
	expr string
	tz   *time.Location
}

func newCronTestEngine() *cronTestEngine {
	return &cronTestEngine{jobs: make(map[int]*cronTestJob)}
}

func (e *cronTestEngine) Schedule(expr string, tz *time.Location, fn func()) (func(), error) {
	if strings.TrimSpace(expr) == "" {
		return nil, errors.New("cronTestEngine: empty expr")
	}
	if fn == nil {
		return nil, errors.New("cronTestEngine: nil fn")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	id := e.next
	e.next++
	e.jobs[id] = &cronTestJob{expr: expr, tz: tz, fn: fn}
	e.scheduled = append(e.scheduled, cronTestSchedule{expr: expr, tz: tz})
	return func() {
		e.mu.Lock()
		delete(e.jobs, id)
		e.mu.Unlock()
	}, nil
}

func (e *cronTestEngine) Start() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.started = true
}

func (e *cronTestEngine) Stop(_ context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stopped = true
	return nil
}

// triggerAll fires every active job once, sequentially.
func (e *cronTestEngine) triggerAll() {
	e.mu.Lock()
	fns := make([]func(), 0, len(e.jobs))
	for _, j := range e.jobs {
		fns = append(fns, j.fn)
	}
	e.mu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

func (e *cronTestEngine) jobCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.jobs)
}

func (e *cronTestEngine) scheduleAt(i int) cronTestSchedule {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.scheduled[i]
}

// cronTestTree builds a small cobra tree exercising every safety
// class the cron surface cares about:
//
//	root
//	├── jobs cleanup    (write)
//	├── jobs purge      (destructive)
//	├── jobs secure     (auth-required, write)
//	└── ping            (read)
func cronTestTree() *cobra.Command {
	root := &cobra.Command{Use: "root"}

	jobs := &cobra.Command{Use: "jobs"}
	cleanup := &cobra.Command{
		Use:         "cleanup",
		Short:       "Clean up stale records",
		RunE:        func(*cobra.Command, []string) error { return nil },
		Annotations: map[string]string{"kit/side-effect": "write"},
	}
	purge := &cobra.Command{
		Use:         "purge",
		Short:       "Purge records",
		RunE:        func(*cobra.Command, []string) error { return nil },
		Annotations: map[string]string{"kit/side-effect": "destructive"},
	}
	secure := &cobra.Command{
		Use:   "secure",
		Short: "Run secure maintenance",
		RunE:  func(*cobra.Command, []string) error { return nil },
		Annotations: map[string]string{
			"kit/side-effect":   "write",
			"kit/auth-required": "true",
		},
	}
	jobs.AddCommand(cleanup, purge, secure)
	root.AddCommand(jobs)

	ping := &cobra.Command{
		Use:         "ping",
		Short:       "Ping",
		RunE:        func(*cobra.Command, []string) error { return nil },
		Annotations: map[string]string{"kit/side-effect": "read"},
	}
	root.AddCommand(ping)

	return root
}

// cronNewBridge builds a bridge with the supplied policy and exposes
// SurfaceCron on every leaf so individual tests opt-out by Hide.
func cronNewBridge(t *testing.T, runner cmdsurface.Runner, policy cmdsurface.Policy) *cmdsurface.Bridge {
	t.Helper()
	root := cronTestTree()
	b := cmdsurface.New(root,
		cmdsurface.WithRunner(runner),
		cmdsurface.WithPolicy(policy),
	)
	b.Expose("*", cmdsurface.SurfaceCron)
	return b
}

func TestMountCron_HappyPath(t *testing.T) {
	runner := &cronFakeRunner{}
	b := cronNewBridge(t, runner, cmdsurface.DefaultPolicy())
	eng := newCronTestEngine()

	var sinkSchedules []cmdsurface.CronSchedule
	var sinkResults []cmdsurface.Result
	var sinkErrs []error
	sink := func(s cmdsurface.CronSchedule, r cmdsurface.Result, err error) {
		sinkSchedules = append(sinkSchedules, s)
		sinkResults = append(sinkResults, r)
		sinkErrs = append(sinkErrs, err)
	}

	schedules := []cmdsurface.CronSchedule{
		{Path: []string{"jobs", "cleanup"}, Expr: "*/5 * * * *"},
	}
	cleanup, err := cmdsurface.MountCron(b, eng, schedules,
		cmdsurface.WithCronResultSink(sink),
	)
	if err != nil {
		t.Fatalf("MountCron: %v", err)
	}
	defer cleanup()

	if !eng.started {
		t.Fatalf("autostart default should call engine.Start()")
	}
	if got := eng.jobCount(); got != 1 {
		t.Fatalf("expected 1 scheduled job, got %d", got)
	}

	eng.triggerAll()

	captured := runner.captured()
	if len(captured) != 1 {
		t.Fatalf("expected 1 captured invocation, got %d", len(captured))
	}
	if !reflect.DeepEqual(captured[0].Path, []string{"jobs", "cleanup"}) {
		t.Fatalf("unexpected path: %v", captured[0].Path)
	}
	if captured[0].Meta.Surface != cmdsurface.SurfaceCron {
		t.Fatalf("expected Meta.Surface=%q got %q",
			cmdsurface.SurfaceCron, captured[0].Meta.Surface)
	}
	if len(sinkSchedules) != 1 || sinkErrs[0] != nil {
		t.Fatalf("sink not invoked or unexpected error: schedules=%d errs=%v",
			len(sinkSchedules), sinkErrs)
	}
	if sinkResults[0].Stdout != "jobs cleanup" {
		t.Fatalf("unexpected sink result stdout: %q", sinkResults[0].Stdout)
	}
}

func TestMountCron_UnknownLeaf(t *testing.T) {
	b := cronNewBridge(t, &cronFakeRunner{}, cmdsurface.DefaultPolicy())
	eng := newCronTestEngine()

	_, err := cmdsurface.MountCron(b, eng, []cmdsurface.CronSchedule{
		{Path: []string{"nope"}, Expr: "* * * * *"},
	})
	if err == nil {
		t.Fatalf("expected error for unknown leaf")
	}
	if !errors.Is(err, cmdsurface.ErrUnknownCommand) {
		t.Fatalf("expected ErrUnknownCommand, got %v", err)
	}
	if eng.jobCount() != 0 {
		t.Fatalf("no jobs should have been scheduled, got %d", eng.jobCount())
	}
}

func TestMountCron_SurfaceNotEnabled(t *testing.T) {
	runner := &cronFakeRunner{}
	root := cronTestTree()
	b := cmdsurface.New(root,
		cmdsurface.WithRunner(runner),
		cmdsurface.WithPolicy(cmdsurface.DefaultPolicy()),
	)
	// Intentionally do NOT expose SurfaceCron on any leaf.
	eng := newCronTestEngine()

	_, err := cmdsurface.MountCron(b, eng, []cmdsurface.CronSchedule{
		{Path: []string{"jobs", "cleanup"}, Expr: "* * * * *"},
	})
	if err == nil {
		t.Fatalf("expected error for disabled surface")
	}
	if !errors.Is(err, cmdsurface.ErrSurfaceNotEnabled) {
		t.Fatalf("expected ErrSurfaceNotEnabled, got %v", err)
	}
	if eng.jobCount() != 0 {
		t.Fatalf("no jobs should have been scheduled, got %d", eng.jobCount())
	}
}

func TestMountCron_DestructiveBlockedAtMount(t *testing.T) {
	b := cronNewBridge(t, &cronFakeRunner{}, cmdsurface.DefaultPolicy())
	eng := newCronTestEngine()

	_, err := cmdsurface.MountCron(b, eng, []cmdsurface.CronSchedule{
		{Path: []string{"jobs", "purge"}, Expr: "* * * * *"},
	})
	if err == nil {
		t.Fatalf("expected destructive block error")
	}
	if !errors.Is(err, cmdsurface.ErrDestructiveBlocked) {
		t.Fatalf("expected ErrDestructiveBlocked, got %v", err)
	}
	if eng.jobCount() != 0 {
		t.Fatalf("no jobs should have been scheduled, got %d", eng.jobCount())
	}
}

func TestMountCron_DestructiveAllowed(t *testing.T) {
	policy := cmdsurface.DefaultPolicy()
	policy.AllowDestructiveOn = []cmdsurface.Surface{cmdsurface.SurfaceCron}

	b := cronNewBridge(t, &cronFakeRunner{}, policy)
	eng := newCronTestEngine()

	cleanup, err := cmdsurface.MountCron(b, eng, []cmdsurface.CronSchedule{
		{Path: []string{"jobs", "purge"}, Expr: "* * * * *"},
	})
	if err != nil {
		t.Fatalf("MountCron: %v", err)
	}
	defer cleanup()
	if eng.jobCount() != 1 {
		t.Fatalf("expected destructive schedule, got %d", eng.jobCount())
	}
}

func TestMountCron_AuthRequiredRefusedByDefault(t *testing.T) {
	b := cronNewBridge(t, &cronFakeRunner{}, cmdsurface.DefaultPolicy())
	eng := newCronTestEngine()

	_, err := cmdsurface.MountCron(b, eng, []cmdsurface.CronSchedule{
		{Path: []string{"jobs", "secure"}, Expr: "* * * * *"},
	})
	if err == nil {
		t.Fatalf("expected auth-required refusal")
	}
	if !strings.Contains(err.Error(), "requires auth") {
		t.Fatalf("unexpected error: %v", err)
	}
	if eng.jobCount() != 0 {
		t.Fatalf("no jobs should have been scheduled, got %d", eng.jobCount())
	}
}

func TestMountCron_AuthRequiredAllowed(t *testing.T) {
	b := cronNewBridge(t, &cronFakeRunner{}, cmdsurface.DefaultPolicy())
	eng := newCronTestEngine()

	cleanup, err := cmdsurface.MountCron(b, eng,
		[]cmdsurface.CronSchedule{
			{Path: []string{"jobs", "secure"}, Expr: "* * * * *"},
		},
		cmdsurface.WithCronAllowAuth(true),
	)
	if err != nil {
		t.Fatalf("MountCron: %v", err)
	}
	defer cleanup()
	if eng.jobCount() != 1 {
		t.Fatalf("expected auth schedule allowed, got %d", eng.jobCount())
	}
}

func TestMountCron_ArgsFlagsBakedIn(t *testing.T) {
	runner := &cronFakeRunner{}
	b := cronNewBridge(t, runner, cmdsurface.DefaultPolicy())
	eng := newCronTestEngine()

	schedule := cmdsurface.CronSchedule{
		Path:  []string{"jobs", "cleanup"},
		Expr:  "0 * * * *",
		Args:  []string{"a", "b"},
		Flags: map[string]any{"force": true, "limit": 42},
	}
	cleanup, err := cmdsurface.MountCron(b, eng, []cmdsurface.CronSchedule{schedule})
	if err != nil {
		t.Fatalf("MountCron: %v", err)
	}
	defer cleanup()

	// Mutate caller-side flags after mount to assert MountCron took a
	// defensive copy.
	schedule.Flags["force"] = false

	eng.triggerAll()

	captured := runner.captured()
	if len(captured) != 1 {
		t.Fatalf("expected 1 invocation, got %d", len(captured))
	}
	if !reflect.DeepEqual(captured[0].Args, []string{"a", "b"}) {
		t.Fatalf("unexpected args: %v", captured[0].Args)
	}
	wantFlags := map[string]any{"force": true, "limit": 42}
	if !reflect.DeepEqual(captured[0].Flags, wantFlags) {
		t.Fatalf("unexpected flags: %v want %v", captured[0].Flags, wantFlags)
	}
}

func TestMountCron_MetaSurfaceForced(t *testing.T) {
	runner := &cronFakeRunner{}
	b := cronNewBridge(t, runner, cmdsurface.DefaultPolicy())
	eng := newCronTestEngine()

	cleanup, err := cmdsurface.MountCron(b, eng, []cmdsurface.CronSchedule{
		{Path: []string{"ping"}, Expr: "* * * * *"},
	})
	if err != nil {
		t.Fatalf("MountCron: %v", err)
	}
	defer cleanup()

	eng.triggerAll()

	captured := runner.captured()
	if len(captured) != 1 {
		t.Fatalf("expected 1 invocation, got %d", len(captured))
	}
	if captured[0].Meta.Surface != cmdsurface.SurfaceCron {
		t.Fatalf("expected Meta.Surface=%q got %q",
			cmdsurface.SurfaceCron, captured[0].Meta.Surface)
	}
	if captured[0].Meta.Caller != "cron" {
		t.Fatalf("expected Meta.Caller=cron got %q", captured[0].Meta.Caller)
	}
	if captured[0].Meta.RequestedAt.IsZero() {
		t.Fatalf("expected Meta.RequestedAt to be set")
	}
}

func TestMountCron_CleanupCancelsSchedules(t *testing.T) {
	runner := &cronFakeRunner{}
	b := cronNewBridge(t, runner, cmdsurface.DefaultPolicy())
	eng := newCronTestEngine()

	cleanup, err := cmdsurface.MountCron(b, eng, []cmdsurface.CronSchedule{
		{Path: []string{"jobs", "cleanup"}, Expr: "* * * * *"},
	})
	if err != nil {
		t.Fatalf("MountCron: %v", err)
	}
	if eng.jobCount() != 1 {
		t.Fatalf("expected 1 scheduled job, got %d", eng.jobCount())
	}

	cleanup()
	// Cleanup is idempotent.
	cleanup()

	if eng.jobCount() != 0 {
		t.Fatalf("cleanup should remove jobs, got %d", eng.jobCount())
	}
	if !eng.stopped {
		t.Fatalf("cleanup should stop engine when autostarted")
	}

	eng.triggerAll()
	if got := len(runner.captured()); got != 0 {
		t.Fatalf("no jobs should have fired after cleanup, got %d", got)
	}
}

func TestMountCron_SinkObservesErrors(t *testing.T) {
	wantErr := errors.New("runner boom")
	runner := &cronFakeRunner{
		RunFn: func(context.Context, cmdsurface.Invocation) (cmdsurface.Result, error) {
			return cmdsurface.Result{}, wantErr
		},
	}
	b := cronNewBridge(t, runner, cmdsurface.DefaultPolicy())
	eng := newCronTestEngine()

	var gotErr error
	var gotResult cmdsurface.Result
	sink := func(_ cmdsurface.CronSchedule, r cmdsurface.Result, err error) {
		gotErr = err
		gotResult = r
	}

	cleanup, err := cmdsurface.MountCron(b, eng,
		[]cmdsurface.CronSchedule{
			{Path: []string{"jobs", "cleanup"}, Expr: "* * * * *"},
		},
		cmdsurface.WithCronResultSink(sink),
	)
	if err != nil {
		t.Fatalf("MountCron: %v", err)
	}
	defer cleanup()

	eng.triggerAll()

	if !errors.Is(gotErr, wantErr) {
		t.Fatalf("sink err = %v want %v", gotErr, wantErr)
	}
	if gotResult.ExitCode != 0 || gotResult.Stdout != "" {
		t.Fatalf("expected zero Result on error, got %+v", gotResult)
	}
}

func TestMountCron_TimezoneParsed(t *testing.T) {
	b := cronNewBridge(t, &cronFakeRunner{}, cmdsurface.DefaultPolicy())
	eng := newCronTestEngine()

	cleanup, err := cmdsurface.MountCron(b, eng, []cmdsurface.CronSchedule{
		{Path: []string{"jobs", "cleanup"}, Expr: "0 9 * * *", Timezone: "America/New_York"},
	})
	if err != nil {
		t.Fatalf("MountCron: %v", err)
	}
	defer cleanup()

	got := eng.scheduleAt(0)
	if got.tz == nil || got.tz.String() != "America/New_York" {
		t.Fatalf("expected tz=America/New_York, got %v", got.tz)
	}
}

func TestMountCron_BadTimezone(t *testing.T) {
	b := cronNewBridge(t, &cronFakeRunner{}, cmdsurface.DefaultPolicy())
	eng := newCronTestEngine()

	_, err := cmdsurface.MountCron(b, eng, []cmdsurface.CronSchedule{
		{Path: []string{"jobs", "cleanup"}, Expr: "* * * * *", Timezone: "Not/A/Zone"},
	})
	if err == nil {
		t.Fatalf("expected timezone error")
	}
	if eng.jobCount() != 0 {
		t.Fatalf("no jobs should have been scheduled, got %d", eng.jobCount())
	}
}

func TestMountCron_AutostartDisabled(t *testing.T) {
	b := cronNewBridge(t, &cronFakeRunner{}, cmdsurface.DefaultPolicy())
	eng := newCronTestEngine()

	cleanup, err := cmdsurface.MountCron(b, eng,
		[]cmdsurface.CronSchedule{
			{Path: []string{"jobs", "cleanup"}, Expr: "* * * * *"},
		},
		cmdsurface.WithCronAutostart(false),
	)
	if err != nil {
		t.Fatalf("MountCron: %v", err)
	}
	defer cleanup()

	if eng.started {
		t.Fatalf("WithCronAutostart(false) should not call Start()")
	}
	cleanup()
	if eng.stopped {
		t.Fatalf("cleanup must not Stop an engine it didn't start")
	}
}

func TestDefaultCronEngine_Smoke(t *testing.T) {
	eng := cmdsurface.DefaultCronEngine()

	cancel, err := eng.Schedule("*/5 * * * *", time.UTC, func() {})
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	cancel()

	cancel2, err := eng.Schedule("0 9 * * *", mustLoadLocation(t, "America/New_York"), func() {})
	if err != nil {
		t.Fatalf("Schedule (tz): %v", err)
	}
	cancel2()

	if _, err := eng.Schedule("not a cron expr", time.UTC, func() {}); err == nil {
		t.Fatalf("expected error for invalid expression")
	}

	eng.Start()
	eng.Start() // idempotent

	ctx, ctxCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer ctxCancel()
	if err := eng.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := eng.Stop(ctx); err != nil {
		t.Fatalf("Stop (idempotent): %v", err)
	}
}

func mustLoadLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Skipf("tz %s unavailable: %v", name, err)
	}
	return loc
}
