package job

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	// EnvRunOneDisable disables RunOne when set to any truthy value.
	EnvRunOneDisable = "JOB_RUNONE_DISABLE"

	// cooldownFile is the advisory file used to throttle RunOne calls.
	cooldownFile = ".job_runone_cooldown"

	// cooldownTTL is the minimum interval between RunOne invocations.
	cooldownTTL = 10 * time.Second
)

// RunOneOption configures RunOne.
type RunOneOption func(*runOneConfig)

type runOneConfig struct{ cooldownDir string }

// WithCooldownDir sets the directory holding the cooldown marker.
// Default: os.TempDir(). Use a private dir per process or test to
// avoid sharing throttle state.
func WithCooldownDir(dir string) RunOneOption {
	return func(c *runOneConfig) { c.cooldownDir = dir }
}

// RunOne opportunistically claims and processes a single job.
//
// Guards:
//   - Env opt-out: if JOB_RUNONE_DISABLE is set, returns nil immediately.
//   - Cooldown file: if a cooldown marker (in os.TempDir(), or the dir
//     from WithCooldownDir) exists and is recent, returns nil.
//   - Pre-check: if Claim returns nil, nothing to do.
//   - A handler panic is recovered, fails the job and is returned as a
//     *PanicError.
func RunOne(
	ctx context.Context,
	svc Service,
	queue, workerID string,
	handlers HandlerMap,
	opts ...RunOneOption,
) error {
	var cfg runOneConfig
	for _, o := range opts {
		o(&cfg)
	}

	// Env opt-out.
	if v := os.Getenv(EnvRunOneDisable); v != "" {
		return nil
	}

	// Cooldown check.
	cd := cooldownPath(cfg.cooldownDir)
	if info, err := os.Stat(cd); err == nil {
		if time.Since(info.ModTime()) < cooldownTTL {
			return nil
		}
	}

	j, err := svc.Claim(ctx, queue, workerID)
	if err != nil {
		return fmt.Errorf("runone claim: %w", err)
	}
	if j == nil {
		return nil
	}

	// Touch cooldown file after successful claim.
	_ = os.MkdirAll(filepath.Dir(cd), 0o755)
	if f, err := os.Create(cd); err == nil {
		_ = f.Close()
	}

	handler, ok := handlers[j.Type]
	if !ok {
		_ = svc.Fail(ctx, j.ID, FailOpts{
			Error: "no handler for type: " + j.Type,
			Retry: false,
		})
		return nil
	}

	if herr := runHandler(ctx, handler, *j); herr != nil {
		_ = svc.Fail(ctx, j.ID, FailOpts{
			Error: herr.Error(),
			Retry: true,
		})
		return herr
	}

	return svc.Complete(ctx, j.ID, nil)
}

func cooldownPath(dir string) string {
	if dir == "" {
		dir = os.TempDir()
	}
	return filepath.Join(dir, cooldownFile)
}
