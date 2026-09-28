package cli

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cast"
	"github.com/spf13/viper"

	"hop.top/kit/go/console/cli/svcconfig"
	"hop.top/kit/go/core/xdg"
	"hop.top/kit/go/security"
	"hop.top/kit/go/transport/cmdsurface"
)

// The audit.sinks list adds sinks to a service's audit pipeline from
// configuration, beside the ones cli.WithAuditSinks registers in code.
// The only type today is chain: a tamper-evident, hash-chained log
// that `<tool> audit verify` checks. Entries use the vocabulary of the
// per-command sinks list (type, path, on, surfaces, paths); a bare
// string is an entry of that type with every default.
//
//	services:
//	  all:
//	    audit:
//	      sinks: [chain]                  # $XDG_STATE_HOME/<tool>/audit.chain
//	  api:
//	    audit:
//	      sinks:                          # replaces the services.all list for api
//	        - type: chain
//	          path: /var/log/mytool/audit.chain
//	          fsync: always               # never (default) | always | <duration>
//	          max_bytes: 67108864         # rotate past 64 MiB; 0 never rotates
//	          max_files: 30               # rotated files kept; 0 keeps all
//	          on: [success, error]        # default both
//	          surfaces: [rest]            # default every surface
//	          paths: ["widget *"]         # default every command
//
// Records reach these sinks redacted, like every audit sink's.
//
// The list is the sinks key of the audit block registered in
// svcconfig, which also holds the keys an entry accepts; the resolver
// takes the service's list from any source, then the services.all
// list.
const (
	auditBlock     = "audit"
	auditSinksKey  = "sinks"
	auditSinkChain = "chain"
	auditChainFile = "audit.chain"
)

// auditSinkKeys are the keys a sink entry accepts, from the registry.
var auditSinkKeys = func() []string {
	b, _ := svcconfig.Lookup(auditBlock)
	return b.Lists[auditSinksKey]
}()

// auditSinkConfig is one resolved audit.sinks entry.
type auditSinkConfig struct {
	key      string // config key of the entry, for messages
	path     string // absolute
	opts     security.AuditLogOptions
	onOK     bool
	onError  bool
	surfaces []cmdsurface.Surface
	paths    []string
}

// serveAuditSinkConfigs resolves svc's audit.sinks list. A malformed
// entry — an unknown type or key, an unparseable value — is a
// configuration error, reported at validation.
func serveAuditSinkConfigs(v *viper.Viper, tool, svc string) ([]auditSinkConfig, error) {
	if v == nil {
		return nil, nil
	}
	raw, key, ok := svcconfig.New(v).Lookup(svc, auditBlock, auditSinksKey)
	if !ok {
		return nil, nil
	}
	if s, ok := raw.(string); ok && s != "" {
		raw = strings.Fields(strings.ReplaceAll(s, ",", " "))
	}
	entries, err := cast.ToSliceE(raw)
	if err != nil {
		if strs, serr := cast.ToStringSliceE(raw); serr == nil {
			entries = make([]any, len(strs))
			for i, s := range strs {
				entries[i] = s
			}
		} else {
			return nil, fmt.Errorf("%s: want a list of sinks", key)
		}
	}
	out := make([]auditSinkConfig, 0, len(entries))
	for i, e := range entries {
		c, err := parseAuditSink(fmt.Sprintf("%s[%d]", key, i), tool, e)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// parseAuditSink parses one audit.sinks entry.
func parseAuditSink(key, tool string, entry any) (auditSinkConfig, error) {
	c := auditSinkConfig{key: key, onOK: true, onError: true}
	fields := map[string]any{}
	switch e := entry.(type) {
	case string:
		fields["type"] = e
	default:
		m, err := cast.ToStringMapE(entry)
		if err != nil {
			return c, fmt.Errorf("%s: want a sink type or a map", key)
		}
		fields = m
	}
	var unknown []string
	for k := range fields {
		if !slices.Contains(auditSinkKeys, k) {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return c, fmt.Errorf("%s: unknown key %s; a sink takes %s",
			key, strings.Join(unknown, ", "), strings.Join(auditSinkKeys, ", "))
	}
	if typ := cast.ToString(fields["type"]); typ != auditSinkChain {
		return c, fmt.Errorf("%s.type: unknown sink type %q; the list takes %q", key, typ, auditSinkChain)
	}

	path := cast.ToString(fields["path"])
	if path == "" {
		dir, err := xdg.RawStateDir(tool)
		if err != nil {
			return c, fmt.Errorf("%s.path: %w", key, err)
		}
		path = filepath.Join(dir, auditChainFile)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return c, fmt.Errorf("%s.path: %w", key, err)
	}
	c.path = abs

	if err := parseAuditFsync(key, fields["fsync"], &c.opts); err != nil {
		return c, err
	}
	if c.opts.MaxBytes, err = nonNegative[int64](key+".max_bytes", fields["max_bytes"], cast.ToInt64E); err != nil {
		return c, err
	}
	if c.opts.MaxSegments, err = nonNegative[int](key+".max_files", fields["max_files"], cast.ToIntE); err != nil {
		return c, err
	}
	if on, ok := fields["on"]; ok {
		tokens, err := cast.ToStringSliceE(on)
		if err != nil {
			return c, fmt.Errorf("%s.on: want a list of success and error", key)
		}
		c.onOK, c.onError = false, false
		for _, t := range tokens {
			switch t {
			case "success":
				c.onOK = true
			case "error":
				c.onError = true
			default:
				return c, fmt.Errorf("%s.on: unknown outcome %q; want success or error", key, t)
			}
		}
	}
	if raw, ok := fields["surfaces"]; ok {
		names, err := cast.ToStringSliceE(raw)
		if err != nil {
			return c, fmt.Errorf("%s.surfaces: want a list of surfaces", key)
		}
		for _, n := range names {
			s := cmdsurface.Surface(n)
			if !s.IsValid() {
				return c, fmt.Errorf("%s.surfaces: unknown surface %q", key, n)
			}
			c.surfaces = append(c.surfaces, s)
		}
	}
	if raw, ok := fields["paths"]; ok {
		if c.paths, err = cast.ToStringSliceE(raw); err != nil {
			return c, fmt.Errorf("%s.paths: want a list of command patterns", key)
		}
	}
	return c, nil
}

// parseAuditFsync maps the fsync key onto the log's sync policy.
func parseAuditFsync(key string, raw any, opts *security.AuditLogOptions) error {
	s := strings.TrimSpace(cast.ToString(raw))
	switch s {
	case "", "never":
		opts.Sync = security.SyncNever
	case "always":
		opts.Sync = security.SyncAlways
	default:
		d, err := time.ParseDuration(s)
		if err != nil || d <= 0 {
			return fmt.Errorf("%s.fsync: %q; want never, always, or a positive duration", key, s)
		}
		opts.Sync, opts.SyncInterval = security.SyncPeriodic, d
	}
	return nil
}

func nonNegative[T int | int64](key string, raw any, conv func(any) (T, error)) (T, error) {
	if raw == nil {
		return 0, nil
	}
	n, err := conv(raw)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%s: %v; want a non-negative integer", key, raw)
	}
	return n, nil
}

// auditChains is the process's set of open audit chains, keyed by
// absolute path. Every service that names the same file shares one
// log, so their records form one chain instead of two writers forking
// it.
type auditChains struct {
	mu   sync.Mutex
	open map[string]*openChain
}

type openChain struct {
	opts security.AuditLogOptions
	sink *cmdsurface.ChainSink
}

// serveConfiguredAuditSinks opens (or reuses) the logs svc's
// audit.sinks list names and returns their sink specs.
func (r *Root) serveConfiguredAuditSinks(svc string) ([]cmdsurface.SinkSpec, error) {
	cfgs, err := serveAuditSinkConfigs(r.Viper, r.Config.Name, svc)
	if err != nil || len(cfgs) == 0 {
		return nil, err
	}
	ch := &r.serveAuth.chains
	ch.mu.Lock()
	defer ch.mu.Unlock()
	if ch.open == nil {
		ch.open = map[string]*openChain{}
	}
	specs := make([]cmdsurface.SinkSpec, 0, len(cfgs))
	for _, c := range cfgs {
		oc, ok := ch.open[c.path]
		if ok && !sameChainOptions(oc.opts, c.opts) {
			return nil, fmt.Errorf("%s: %s is already open with different fsync, max_bytes or max_files", c.key, c.path)
		}
		if !ok {
			l, err := security.OpenAuditLog(c.path, c.opts)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", c.key, err)
			}
			oc = &openChain{opts: c.opts, sink: &cmdsurface.ChainSink{Log: l}}
			ch.open[c.path] = oc
		}
		specs = append(specs, cmdsurface.SinkSpec{
			Sink: oc.sink, OnOK: c.onOK, OnError: c.onError,
			Surfaces: c.surfaces, Paths: c.paths,
		})
	}
	return specs, nil
}

// sameChainOptions reports whether two entries naming one file ask for
// the same durability, rotation and retention.
func sameChainOptions(a, b security.AuditLogOptions) bool {
	return a.Sync == b.Sync && a.SyncInterval == b.SyncInterval &&
		a.MaxBytes == b.MaxBytes && a.MaxSegments == b.MaxSegments
}

// closeAuditChains closes every chain the services opened. Called once
// the supervisor has stopped every service.
func (r *Root) closeAuditChains() error {
	ch := &r.serveAuth.chains
	ch.mu.Lock()
	defer ch.mu.Unlock()
	var errs []error
	for path, oc := range ch.open {
		errs = append(errs, oc.sink.Log.Close())
		delete(ch.open, path)
	}
	return errors.Join(errs...)
}

// serveAuditChainPaths returns the chain files configuration names for
// any of the services, deduplicated and sorted. It is what
// `audit verify` checks when no --file is given.
func serveAuditChainPaths(v *viper.Viper, tool string, services []string) ([]string, error) {
	seen := map[string]bool{}
	for _, svc := range append([]string{svcconfig.Shared}, services...) {
		cfgs, err := serveAuditSinkConfigs(v, tool, svc)
		if err != nil {
			return nil, err
		}
		for _, c := range cfgs {
			seen[c.path] = true
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}
