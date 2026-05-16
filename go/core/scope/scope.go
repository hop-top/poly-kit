// Package scope provides allow/deny path policy guardrails for kit primitives
// that touch the filesystem.
//
// A Policy is a small bundle of:
//   - A Mode (Strict, Warn, Prompt) that governs how denied paths are
//     surfaced to the caller.
//   - An ordered list of Rules: each rule pairs a set of glob patterns with a
//     bitset of operations and an Allow/Deny verdict.
//   - An optional prompt callback used in Prompt mode.
//
// Decision algorithm (deny-wins):
//
//  1. If any matching deny rule matches the (path, op) pair, the result is
//     Denied.
//  2. Else if any matching allow rule matches, the result is Allowed.
//  3. Otherwise the result is Unknown, treated as Denied in Strict mode and
//     Allowed in the other modes.
//
// Patterns use bmatcuk/doublestar/v4 syntax. Symlinks are resolved at Check
// time to defeat ~/foo -> /etc/passwd style escapes; ENOENT paths are matched
// as-is so "intent to write" still triggers rules.
package scope

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"charm.land/log/v2"
)

// ErrDenied is returned by Enforce when a path is denied in Strict mode or in
// Prompt mode after the prompt callback returns false.
var ErrDenied = errors.New("scope: path denied")

// Path is a canonical absolute filesystem path. Construct via NewPath, which
// runs filepath.Clean and expands a leading "~" to the user's home dir.
// Symlinks are NOT resolved at construction; resolution happens lazily in
// Check so policies can reference paths that don't yet exist.
type Path string

// Pattern is a glob pattern interpreted by github.com/bmatcuk/doublestar/v4.
// A leading "~" is expanded to the user's home dir at match time, so
// ~/.ssh/** and /Users/x/.ssh/** behave identically for user x.
type Pattern string

// Mode controls how Enforce reacts to a Denied decision.
type Mode int

const (
	// Strict denies → returns ErrDenied. The default.
	Strict Mode = iota
	// Warn denies → logs a warning via kit/log and returns nil.
	Warn
	// Prompt denies → invokes the prompt callback. If unset or it returns
	// false, returns ErrDenied; if true, returns nil.
	Prompt
)

// Op is a bitset of filesystem operations.
type Op int

const (
	// Read covers stat, open-for-read, readdir, readlink.
	Read Op = 1 << iota
	// Write covers create, open-for-write, mkdir, rename, remove, chmod.
	Write
	// Exec covers exec syscalls and pseudo-exec like dlopen.
	Exec
)

// Decision is the outcome of Check.
type Decision int

const (
	// Unknown means no rule matched.
	Unknown Decision = iota
	// Allowed means at least one allow rule matched and no deny did.
	Allowed
	// Denied means at least one deny rule matched.
	Denied
)

// Rule pairs glob patterns with a bitset of operations and an Allow/Deny verdict.
type Rule struct {
	Patterns []Pattern
	Ops      Op
	Allow    bool
}

// PromptFunc is invoked by Enforce in Prompt mode when a Denied decision is
// returned. The implementation typically asks the user; returning true means
// the operation is permitted for this call only (the policy is not mutated).
type PromptFunc func(path Path, op Op) bool

// Policy holds an ordered list of rules, a Mode, and an optional prompt callback.
// The zero value is unsafe; use New or Default.
type Policy struct {
	mu     sync.RWMutex
	rules  []Rule
	mode   Mode
	prompt PromptFunc
	logger *log.Logger
}

// New returns an empty Policy in Strict mode. With no rules registered,
// every Check returns Unknown which Strict treats as Denied — call Allow to
// open holes.
//
// Apply Options to override defaults; WithLogger swaps the kit/log logger
// used for warn-mode and stat-error diagnostics.
func New(opts ...Option) *Policy {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(cfg)
	}
	return &Policy{mode: Strict, logger: cfg.logger}
}

var (
	defaultOnce sync.Once
	defaultP    *Policy
)

// Default returns the package-level singleton policy used by kit primitives
// that don't accept an explicit policy. Lazy-initialized on first call.
//
// The bare singleton is a Strict empty policy. The defaults package wires up
// a default deny list at its init time, so importers of go/core/scope/defaults
// see a hardened singleton.
func Default() *Policy {
	defaultOnce.Do(func() {
		defaultP = New()
	})
	return defaultP
}

// Allow registers an allow rule covering Read|Write|Exec for the given patterns.
func (p *Policy) Allow(patterns ...Pattern) *Policy {
	return p.AllowOp(Read|Write|Exec, patterns...)
}

// AllowOp registers an allow rule for the given operations and patterns.
func (p *Policy) AllowOp(op Op, patterns ...Pattern) *Policy {
	if len(patterns) == 0 {
		return p
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rules = append(p.rules, Rule{Patterns: append([]Pattern(nil), patterns...), Ops: op, Allow: true})
	return p
}

// Deny registers a deny rule covering Read|Write|Exec for the given patterns.
func (p *Policy) Deny(patterns ...Pattern) *Policy {
	return p.DenyOp(Read|Write|Exec, patterns...)
}

// DenyOp registers a deny rule for the given operations and patterns.
func (p *Policy) DenyOp(op Op, patterns ...Pattern) *Policy {
	if len(patterns) == 0 {
		return p
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rules = append(p.rules, Rule{Patterns: append([]Pattern(nil), patterns...), Ops: op, Allow: false})
	return p
}

// SetMode sets the policy's enforcement mode. Returns p for chaining.
func (p *Policy) SetMode(m Mode) *Policy {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.mode = m
	return p
}

// Mode returns the current enforcement mode.
func (p *Policy) Mode() Mode {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.mode
}

// SetPromptFunc registers the callback invoked in Prompt mode on a Denied
// decision. Returns p for chaining.
func (p *Policy) SetPromptFunc(fn PromptFunc) *Policy {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prompt = fn
	return p
}

// Rules returns a defensive copy of the policy's rules in registration order.
func (p *Policy) Rules() []Rule {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]Rule, len(p.rules))
	copy(out, p.rules)
	for i := range out {
		out[i].Patterns = append([]Pattern(nil), p.rules[i].Patterns...)
	}
	return out
}

// Check evaluates (path, op) against the policy. Pure — no prompt, no
// mutation. Resolves symlinks (via filepath.EvalSymlinks) before matching to
// defeat symlink escapes; on ENOENT the cleaned path is used so "intent to
// write" still matches deny rules.
//
// The returned error is non-nil only on filesystem errors other than ENOENT
// (e.g. permission denied during stat). Decision is meaningful even when
// err != nil — callers may prefer to fail closed.
func (p *Policy) Check(path Path, op Op) (Decision, error) {
	resolved, err := resolvePath(string(path))
	if err != nil {
		return Unknown, err
	}

	p.mu.RLock()
	rules := p.rules
	p.mu.RUnlock()

	var sawAllow bool
	for _, r := range rules {
		if r.Ops&op == 0 {
			continue
		}
		matched, mErr := matchAny(r.Patterns, resolved)
		if mErr != nil {
			return Unknown, mErr
		}
		if !matched {
			continue
		}
		if !r.Allow {
			return Denied, nil
		}
		sawAllow = true
	}
	if sawAllow {
		return Allowed, nil
	}
	return Unknown, nil
}

// Enforce calls Check then translates the Decision according to Mode:
//   - Allowed or Unknown (in non-Strict modes): nil.
//   - Strict + (Denied or Unknown): ErrDenied.
//   - Warn + Denied: logs and returns nil.
//   - Prompt + Denied: invokes the prompt callback; nil on true, ErrDenied on false.
func (p *Policy) Enforce(path Path, op Op) error {
	dec, err := p.Check(path, op)
	if err != nil {
		// Filesystem error during stat. Fail closed in Strict, log + allow elsewhere.
		if p.Mode() == Strict {
			return fmt.Errorf("scope: enforce %q: %w", path, err)
		}
		p.log().Warn("scope: stat error during enforce", "path", string(path), "err", err)
		return nil
	}

	mode := p.Mode()
	switch dec {
	case Allowed:
		return nil
	case Denied:
		return p.handleDeny(path, op, mode)
	case Unknown:
		if mode == Strict {
			return p.handleDeny(path, op, mode)
		}
		return nil
	default:
		return nil
	}
}

func (p *Policy) handleDeny(path Path, op Op, mode Mode) error {
	switch mode {
	case Warn:
		p.log().Warn("scope: path denied (warn mode, allowing)", "path", string(path), "op", opString(op))
		return nil
	case Prompt:
		p.mu.RLock()
		fn := p.prompt
		p.mu.RUnlock()
		if fn == nil {
			return fmt.Errorf("%w: %s (op=%s)", ErrDenied, path, opString(op))
		}
		if fn(path, op) {
			return nil
		}
		return fmt.Errorf("%w: %s (op=%s)", ErrDenied, path, opString(op))
	default: // Strict
		return fmt.Errorf("%w: %s (op=%s)", ErrDenied, path, opString(op))
	}
}

// log returns the configured logger, falling back to a fresh kit/log
// logger keyed off the global viper if none was set (zero-value Policy).
func (p *Policy) log() *log.Logger {
	p.mu.RLock()
	l := p.logger
	p.mu.RUnlock()
	if l != nil {
		return l
	}
	fallback := defaultConfig().logger
	p.mu.Lock()
	if p.logger == nil {
		p.logger = fallback
	}
	l = p.logger
	p.mu.Unlock()
	return l
}

// Snapshot returns a deep copy of the policy. The copy shares no state with
// the original; further mutations to either are independent. Useful for
// per-test isolation paired with SetDefault.
func (p *Policy) Snapshot() *Policy {
	p.mu.RLock()
	defer p.mu.RUnlock()
	cp := &Policy{mode: p.mode, prompt: p.prompt, logger: p.logger}
	cp.rules = make([]Rule, len(p.rules))
	for i, r := range p.rules {
		cp.rules[i] = Rule{
			Patterns: append([]Pattern(nil), r.Patterns...),
			Ops:      r.Ops,
			Allow:    r.Allow,
		}
	}
	return cp
}

// SetDefault swaps the package-level singleton policy with p and returns a
// restore func that puts the previous policy back. Intended for tests:
//
//	func TestThing(t *testing.T) {
//	    restore := scope.SetDefault(scope.New().Allow("~/tmp/**"))
//	    t.Cleanup(restore)
//	    // ... test code using scope.Default() ...
//	}
//
// SetDefault forces lazy initialisation of Default() if it has not yet
// occurred, ensuring later calls to Default() see p.
func SetDefault(p *Policy) (restore func()) {
	prev := Default()
	defaultP = p
	return func() { defaultP = prev }
}

// NewPath cleans s and expands a leading "~" to the user home directory.
// Returns Path("") and an error when s is empty or "~" cannot be resolved.
func NewPath(s string) (Path, error) {
	if s == "" {
		return "", fmt.Errorf("scope: empty path")
	}
	expanded, err := expandHome(s)
	if err != nil {
		return "", err
	}
	return Path(filepath.Clean(expanded)), nil
}

// opString returns a human label for an Op bitset.
func opString(op Op) string {
	parts := make([]string, 0, 3)
	if op&Read != 0 {
		parts = append(parts, "read")
	}
	if op&Write != 0 {
		parts = append(parts, "write")
	}
	if op&Exec != 0 {
		parts = append(parts, "exec")
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, "|")
}
