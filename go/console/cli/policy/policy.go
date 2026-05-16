// Package policy implements kit's delegation-safety policy engine
// (cli-conventions-with-kit.md §8.6). Adopter tools wire enforcement
// once via cli.WithPolicy; this package owns:
//
//   - Policy: the loaded YAML shape (allow / max_ops / require_confirm)
//   - Engine: per-invocation enforcement state — Authorize gates the
//     side-effect tag, RecordOp accounts mutating-op budget.
//   - Load: reads $XDG_CONFIG_HOME/<tool>/policies/<name>.yaml.
//
// The Policy values are inert; the Engine is mutable and lives for one
// command invocation only — kit constructs a fresh Engine inside the
// RunE middleware on every Execute. Engine is not safe for concurrent
// use; the cli middleware never calls into it from multiple goroutines.
//
// SideEffect is mirrored as a local string type so this package can
// be imported by cli without an import cycle. The values must match
// cli.SideEffect{Read,Write,Destructive,Interactive}.
package policy

import (
	"errors"
)

// SideEffect mirrors cli.SideEffect as a local type so the Policy
// schema is a plain string-keyed map. The cli package converts
// between the two transparently.
type SideEffect string

// Side-effect class constants — must align with cli.SideEffect*.
const (
	SideEffectRead        SideEffect = "read"
	SideEffectWrite       SideEffect = "write"
	SideEffectDestructive SideEffect = "destructive"
	SideEffectInteractive SideEffect = "interactive"
)

// Policy declares per-side-effect-class rules. Loaded from YAML in
// $XDG_CONFIG_HOME/<tool>/policies/<name>.yaml per §8.6.
//
// Field semantics (locked by §8.6):
//   - Allow: per-side-effect verb glob list. The empty list under a
//     class categorically refuses that class (e.g.
//     `destructive: []` blocks every destructive command).
//   - MaxOps: per-invocation cap on mutating ops. 0 means unlimited.
//   - RequireConfirm: command-path globs that always require explicit
//     confirmation regardless of --confirm value. A typed-confirmation
//     command is recognized via the kit/destructive-token annotation;
//     this list adds extra command paths beyond annotation-driven
//     ones.
type Policy struct {
	Name           string                  `yaml:"name"`
	Allow          map[SideEffect][]string `yaml:"allow"`
	MaxOps         int                     `yaml:"max_ops"`
	RequireConfirm []string                `yaml:"require_confirm"`
}

// ErrMaxOpsExceeded is returned by Engine.RecordOp when the per-
// invocation budget has been hit. The cli middleware translates this
// into output.RateLimitedError so the process exits with
// EXIT_RATE_LIMITED (64).
var ErrMaxOpsExceeded = errors.New("policy: max-ops budget exceeded")
