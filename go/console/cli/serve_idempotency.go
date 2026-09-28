package cli

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cast"
	"github.com/spf13/viper"

	"hop.top/kit/go/console/cli/idemstore"
	"hop.top/kit/go/console/cli/svcconfig"
	"hop.top/kit/go/core/xdg"
	"hop.top/kit/go/transport/cmdsurface"
)

// The idempotency block turns replay of Idempotency-Key calls on or
// off for one service, or for every service under services.all, and
// sets how long a recorded answer replays:
//
//	services:
//	  all:
//	    idempotency:
//	      ttl: 1h            # default 24h
//	  socket:
//	    idempotency:
//	      enabled: false     # default true
//
// Replay is on by default on every service and inert for a call that
// carries no key. Each key resolves on its own: the service's key from
// any source, then the services.all key, then the kit default.
const (
	idempotencyBlock   = "idempotency"
	idempotencyEnabled = "enabled"
	idempotencyTTL     = "ttl"

	// serveIdempotencyFile is the store's file in the tool's state
	// directory. It is not the CLI's own --idempotency-key store: a
	// served record is scoped to its caller and replayed only to it.
	serveIdempotencyFile = "serve-idempotency.db"
)

// serveIdempotencyConfig is one service's resolved idempotency block.
type serveIdempotencyConfig struct {
	enabled bool
	ttl     time.Duration
}

// serveIdempotencyState is the ledger every kit-shipped service of
// the process shares, so a key in flight on one transport is in
// flight on all of them.
type serveIdempotencyState struct {
	mu     sync.Mutex
	store  idemstore.Store // adopter's store, from WithServeIdempotencyStore
	ledger *cmdsurface.IdempotencyLedger
	owned  bool // the ledger opened its own store, and closes it
}

// WithServeIdempotencyStore records the served transports' idempotency
// replays in store instead of the sqlite file kit opens in the tool's
// state directory (serve-idempotency.db). Pass idemstore.Memory() in
// tests, or a store every replica of the service shares. Kit does not
// close a store given here.
//
// This is the served path's store. The CLI's own --idempotency-key
// replay is [WithIdempotencyStore]; a served record is scoped to its
// caller, so the two never share records.
func WithServeIdempotencyStore(store idemstore.Store) func(*Root) {
	return func(r *Root) { r.serveAuth.idem.store = store }
}

// serveIdempotency resolves svc's idempotency block. An unknown key,
// an enabled that is not a boolean, or a ttl that is not a positive
// duration is a configuration error, reported at validation.
func serveIdempotency(v *viper.Viper, svc string) (serveIdempotencyConfig, error) {
	out := serveIdempotencyConfig{enabled: true, ttl: idemstore.DefaultTTL}
	if v == nil {
		return out, nil
	}
	cfg := svcconfig.New(v)
	if err := cfg.ValidateBlock(idempotencyBlock, svc, svcconfig.Shared); err != nil {
		return out, err
	}
	if raw, key, ok := cfg.Lookup(svc, idempotencyBlock, idempotencyEnabled); ok {
		on, err := boolValue(raw)
		if err != nil {
			return out, fmt.Errorf("%s: %w", key, err)
		}
		out.enabled = on
	}
	if raw, key, ok := cfg.Lookup(svc, idempotencyBlock, idempotencyTTL); ok {
		d, err := cast.ToDurationE(raw)
		if err == nil && d <= 0 {
			err = errors.New("must be positive")
		}
		if err != nil {
			return out, fmt.Errorf("%s: %v is not a positive duration such as 1h (default %s): %w",
				key, raw, shortDuration(idemstore.DefaultTTL), err)
		}
		out.ttl = d
	}
	return out, nil
}

// shortDuration renders d without zero minutes and seconds: 24h, not
// 24h0m0s.
func shortDuration(d time.Duration) string {
	s := d.String()
	s = strings.TrimSuffix(s, "0s")
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// serveIdempotencyOptions returns the bridge option that turns replay
// on for svc, or none when its block switches it off.
func (r *Root) serveIdempotencyOptions(svc string) ([]cmdsurface.Option, error) {
	cfg, err := serveIdempotency(r.Viper, svc)
	if err != nil || !cfg.enabled {
		return nil, err
	}
	return []cmdsurface.Option{cmdsurface.WithIdempotency(r.serveIdempotencyLedger(), cfg.ttl)}, nil
}

// serveIdempotencyLedger returns the process's ledger, creating it on
// first use. Kit's own store is opened on the first keyed call, not
// here, so a service that never sees a key creates no file.
func (r *Root) serveIdempotencyLedger() *cmdsurface.IdempotencyLedger {
	st := &r.serveAuth.idem
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.ledger != nil {
		return st.ledger
	}
	if st.store != nil {
		st.ledger = cmdsurface.NewIdempotencyLedger(st.store)
		return st.ledger
	}
	tool := r.serveToolName()
	st.ledger = cmdsurface.OpenIdempotencyLedger(func() (idemstore.Store, error) {
		path, err := xdg.StateFile(tool, serveIdempotencyFile)
		if err != nil {
			return nil, err
		}
		// Each service applies its own ttl when it reads a record; the
		// store keeps, and purges past, the longest of them.
		return idemstore.OpenSQLite(path, r.serveIdempotencyStoreTTL())
	})
	st.owned = true
	return st.ledger
}

// serveIdempotencyStoreTTL is how long kit's own store keeps a record:
// the longest ttl among the registered services that replay, so the
// store's purge never deletes a record a service would still answer
// with. With none, it is [idemstore.DefaultTTL].
func (r *Root) serveIdempotencyStoreTTL() time.Duration {
	var longest time.Duration
	if r.serveReg != nil {
		for _, svc := range r.serveReg.Names() {
			cfg, err := serveIdempotency(r.Viper, svc)
			if err != nil || !cfg.enabled {
				continue
			}
			longest = max(longest, cfg.ttl)
		}
	}
	if longest <= 0 {
		return idemstore.DefaultTTL
	}
	return longest
}

// serveToolName names the tool's state directory.
func (r *Root) serveToolName() string {
	if name := strings.TrimSpace(r.Config.Name); name != "" {
		return name
	}
	if r.Cmd != nil {
		return r.Cmd.Name()
	}
	return "kit"
}

// closeServeIdempotency closes the store kit opened for the served
// transports. Called once every service has stopped.
func (r *Root) closeServeIdempotency() error {
	st := &r.serveAuth.idem
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.ledger == nil || !st.owned {
		return nil
	}
	err := st.ledger.Close()
	st.ledger, st.owned = nil, false
	return err
}
