package cmdsurface

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

// CronEngine abstracts a cron scheduler so adopters can substitute
// River, Temporal, hosted schedulers, etc. The default impl wraps
// robfig/cron/v3 (see [DefaultCronEngine]).
//
// Engines are expected to be safe for concurrent use: MountCron calls
// Schedule once per schedule from a single goroutine, but Start, Stop,
// and cancel funcs may be invoked from arbitrary goroutines.
type CronEngine interface {
	// Schedule registers fn to run on the cron expression in tz.
	// Returns a cancel func that unschedules the job. Implementations
	// MUST validate expr and return a non-nil error for invalid input
	// without scheduling the job.
	Schedule(expr string, tz *time.Location, fn func()) (cancel func(), err error)

	// Start begins firing jobs. Idempotent: calling Start on an
	// already-running engine is a no-op.
	Start()

	// Stop blocks until in-flight jobs complete (or ctx expires).
	// Idempotent: calling Stop on an already-stopped engine returns
	// nil immediately.
	Stop(ctx context.Context) error
}

// CronSchedule binds one leaf invocation to a cron expression. Path
// resolves against the bridge's leaf tree; Expr is a 5-field cron
// expression (minute, hour, day-of-month, month, day-of-week);
// Timezone is an IANA name (empty / "UTC" → UTC).
type CronSchedule struct {
	// Path is the cobra leaf path, e.g. ["jobs","cleanup"].
	Path []string
	// Expr is the 5-field cron expression.
	Expr string
	// Timezone is the IANA zone name; empty defaults to UTC.
	Timezone string
	// Args are positional arguments baked into the invocation.
	Args []string
	// Flags are flag values baked into the invocation.
	Flags map[string]any
}

// CronOption configures MountCron.
type CronOption func(*cronConfig)

type cronConfig struct {
	ctx       context.Context
	sink      func(CronSchedule, Result, error)
	autostart bool
	logger    func(string, ...any)
	allowAuth bool
}

func defaultCronConfig() cronConfig {
	return cronConfig{
		ctx:       context.Background(),
		autostart: true,
		logger:    func(string, ...any) {},
	}
}

// WithCronContext sets the context handed to every Bridge.Invoke call.
// Default is context.Background. Canceling the context does NOT
// unschedule jobs; use the cleanup func returned by MountCron.
func WithCronContext(ctx context.Context) CronOption {
	return func(c *cronConfig) {
		if ctx != nil {
			c.ctx = ctx
		}
	}
}

// WithCronResultSink installs a callback invoked after each scheduled
// job runs. The callback receives the originating CronSchedule, the
// Result, and any error returned by the bridge. Default is nil
// (results are discarded).
func WithCronResultSink(fn func(CronSchedule, Result, error)) CronOption {
	return func(c *cronConfig) { c.sink = fn }
}

// WithCronAutostart controls whether MountCron calls engine.Start
// before returning. Default true; set to false when the caller wants
// to coordinate engine lifecycle externally.
func WithCronAutostart(autostart bool) CronOption {
	return func(c *cronConfig) { c.autostart = autostart }
}

// WithCronLogger installs a structured logger called with diagnostic
// lines (Printf-style). Default is a no-op.
func WithCronLogger(fn func(string, ...any)) CronOption {
	return func(c *cronConfig) {
		if fn != nil {
			c.logger = fn
		}
	}
}

// WithCronAllowAuth permits scheduling leaves whose SafetyClass has
// AuthRequired=true. Cron has no human caller, so the default is to
// refuse such leaves at mount time. Setting this to true opts the
// caller into running auth-required jobs as the cron principal.
func WithCronAllowAuth(allow bool) CronOption {
	return func(c *cronConfig) { c.allowAuth = allow }
}

// MountCron schedules each CronSchedule on engine. Returns a cleanup
// func that cancels every schedule and (if autostarted) stops the
// engine. The cleanup is idempotent.
//
// Validation runs before any job is scheduled:
//
//   - schedule.Path must resolve to a leaf with SurfaceCron enabled
//   - destructive leaves require Policy.AllowDestructiveOn to include
//     SurfaceCron
//   - auth-required leaves require WithCronAllowAuth(true)
//   - schedule.Timezone must be loadable via time.LoadLocation
//   - schedule.Expr is validated by the engine
//
// If any schedule fails validation MountCron returns the error and
// schedules nothing.
func MountCron(b *Bridge, engine CronEngine, schedules []CronSchedule, opts ...CronOption) (func(), error) {
	if b == nil {
		return nil, errors.New("cmdsurface: nil Bridge")
	}
	if engine == nil {
		return nil, errors.New("cmdsurface: nil CronEngine")
	}
	cfg := defaultCronConfig()
	for _, o := range opts {
		o(&cfg)
	}

	// Pre-validate every schedule before touching the engine. This
	// guarantees an all-or-nothing mount: a single bad entry returns
	// the error and leaves engine state untouched.
	type prepared struct {
		schedule CronSchedule
		loc      *time.Location
	}
	prep := make([]prepared, 0, len(schedules))
	for i, s := range schedules {
		if len(s.Path) == 0 {
			return nil, fmt.Errorf("cmdsurface: cron schedule %d: empty Path", i)
		}
		if strings.TrimSpace(s.Expr) == "" {
			return nil, fmt.Errorf("cmdsurface: cron schedule %d (%s): empty Expr",
				i, strings.Join(s.Path, " "))
		}
		leaf, err := b.resolveLeaf(s.Path)
		if err != nil {
			return nil, fmt.Errorf("cmdsurface: cron schedule %d: %w", i, err)
		}
		if !leaf.Enabled[SurfaceCron] {
			return nil, fmt.Errorf("%w: %s on %s",
				ErrSurfaceNotEnabled, leaf.PathKey(), SurfaceCron)
		}
		if leaf.Class.Destructive && !b.cfg.policy.Allowed(leaf.Class, SurfaceCron) {
			return nil, fmt.Errorf("%w: %s on %s",
				ErrDestructiveBlocked, leaf.PathKey(), SurfaceCron)
		}
		if leaf.Class.AuthRequired && !cfg.allowAuth {
			return nil, fmt.Errorf("cmdsurface: cron schedule %s requires auth; pass WithCronAllowAuth(true) to opt in",
				leaf.PathKey())
		}
		loc, err := loadCronLocation(s.Timezone)
		if err != nil {
			return nil, fmt.Errorf("cmdsurface: cron schedule %s: %w", leaf.PathKey(), err)
		}
		prep = append(prep, prepared{schedule: s, loc: loc})
	}

	// Schedule each prepared entry. If any Schedule call fails, roll
	// back the cancels we already collected.
	cancels := make([]func(), 0, len(prep))
	rollback := func() {
		for _, c := range cancels {
			c()
		}
	}
	for _, p := range prep {
		s := p.schedule
		loc := p.loc
		fn := makeCronJob(b, s, cfg)
		cancel, err := engine.Schedule(s.Expr, loc, fn)
		if err != nil {
			rollback()
			return nil, fmt.Errorf("cmdsurface: cron schedule %s: %w",
				strings.Join(s.Path, " "), err)
		}
		cancels = append(cancels, cancel)
		cfg.logger("cmdsurface: cron scheduled %s expr=%q tz=%s",
			strings.Join(s.Path, " "), s.Expr, loc.String())
	}

	if cfg.autostart {
		engine.Start()
	}

	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			for _, c := range cancels {
				c()
			}
			if cfg.autostart {
				_ = engine.Stop(context.Background())
			}
		})
	}
	return cleanup, nil
}

// makeCronJob builds the func that the engine fires on each tick.
// Captured state is the bridge, schedule, and config (sink + logger).
// Path / Args / Flags are snapshotted at mount time so post-mount
// mutation by the caller cannot bleed into firings.
func makeCronJob(b *Bridge, s CronSchedule, cfg cronConfig) func() {
	frozen := CronSchedule{
		Path:     append([]string(nil), s.Path...),
		Expr:     s.Expr,
		Timezone: s.Timezone,
		Args:     append([]string(nil), s.Args...),
		Flags:    copyFlags(s.Flags),
	}
	return func() {
		inv := Invocation{
			Path:  append([]string(nil), frozen.Path...),
			Args:  append([]string(nil), frozen.Args...),
			Flags: copyFlags(frozen.Flags),
			Meta: Meta{
				Surface:     SurfaceCron,
				Caller:      "cron",
				RequestedAt: time.Now(),
			},
		}
		res, err := b.Invoke(cfg.ctx, inv)
		if cfg.sink != nil {
			cfg.sink(frozen, res, err)
		}
		if err != nil {
			cfg.logger("cmdsurface: cron %s error: %v",
				strings.Join(frozen.Path, " "), err)
			return
		}
		cfg.logger("cmdsurface: cron %s ran exit=%d",
			strings.Join(frozen.Path, " "), res.ExitCode)
	}
}

// copyFlags clones m so the scheduled job is insulated from
// post-MountCron mutation by the caller.
func copyFlags(m map[string]any) map[string]any {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// loadCronLocation resolves an IANA timezone name. Empty / "UTC" /
// "utc" all resolve to time.UTC without hitting the tz database.
func loadCronLocation(tz string) (*time.Location, error) {
	switch tz {
	case "", "UTC", "utc":
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, fmt.Errorf("invalid timezone %q: %w", tz, err)
	}
	return loc, nil
}

// DefaultCronEngine returns a CronEngine backed by robfig/cron/v3.
// The engine uses the library's default 5-field parser (no seconds).
// Per-schedule timezone is honored by prefixing the expression with
// the robfig CRON_TZ= prefix; the engine itself runs in UTC.
func DefaultCronEngine() CronEngine {
	return &defaultCronEngine{cron: cron.New()}
}

type defaultCronEngine struct {
	cron    *cron.Cron
	mu      sync.Mutex
	started bool
	stopped bool
}

// Schedule implements CronEngine using robfig/cron/v3.
func (e *defaultCronEngine) Schedule(expr string, tz *time.Location, fn func()) (func(), error) {
	if fn == nil {
		return nil, errors.New("cmdsurface: nil cron job func")
	}
	full := expr
	if tz != nil && tz != time.UTC && tz.String() != "UTC" {
		// robfig accepts a CRON_TZ=<zone> prefix per-entry to bind a
		// specific schedule to a location even when the *cron.Cron
		// itself is configured for a different zone.
		full = fmt.Sprintf("CRON_TZ=%s %s", tz.String(), expr)
	}
	id, err := e.cron.AddFunc(full, fn)
	if err != nil {
		return nil, err
	}
	cancel := func() { e.cron.Remove(id) }
	return cancel, nil
}

// Start implements CronEngine. Idempotent.
func (e *defaultCronEngine) Start() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.started || e.stopped {
		return
	}
	e.cron.Start()
	e.started = true
}

// Stop implements CronEngine. Blocks until in-flight jobs finish or
// ctx fires, whichever comes first. Idempotent.
func (e *defaultCronEngine) Stop(ctx context.Context) error {
	e.mu.Lock()
	if !e.started || e.stopped {
		e.stopped = true
		e.mu.Unlock()
		return nil
	}
	e.stopped = true
	e.mu.Unlock()

	stopCtx := e.cron.Stop()
	if ctx == nil {
		<-stopCtx.Done()
		return nil
	}
	select {
	case <-stopCtx.Done():
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
