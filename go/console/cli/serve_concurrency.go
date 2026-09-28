package cli

import (
	"fmt"
	"math"

	"github.com/spf13/viper"

	"hop.top/kit/go/console/cli/svcconfig"
	"hop.top/kit/go/transport/cmdsurface"
)

// The concurrency block bounds how many invocations a service runs at
// once: invocation-plane slot 11, once every gate has admitted a call
// and any person asked has confirmed it. A call takes an in-flight
// slot, or waits for one in a first-come-first-served queue; with
// every slot taken and the queue full it is refused as overloaded:
// HTTP 503 with Retry-After, Connect Unavailable, an MCP isError
// result, the socket's OVERLOADED; exit class 6.
//
//	services:
//	  all:
//	    concurrency:
//	      enabled: true      # default: on, loopback or not
//	      max_inflight: 32   # calls running at once
//	      max_queue: 64      # calls waiting for a slot; 0 refuses at once
//	  socket:
//	    concurrency:
//	      max_queue: 8       # socket only; max_inflight still comes from all
//
// Every key resolves on its own: the service's key from any source,
// then the services.all key, then the kit default (the numbers above,
// cmdsurface.DefaultConcurrency). The per-command deadline
// (timeouts.command, kit/timeout) covers the wait. Slots are per
// service.
//
// max_inflight is an upper bound. A service without a root factory
// (cli.WithRootFactory) runs every invocation on one shared tree, one
// at a time, so it runs one and queues the rest; with a root factory
// up to max_inflight run in parallel.
const (
	concurrencyBlock       = "concurrency"
	concurrencyEnabled     = "enabled"
	concurrencyMaxInflight = "max_inflight"
	concurrencyMaxQueue    = "max_queue"
)

// serveConcurrency resolves svc's concurrency block. on reports
// whether the gate is installed: the enabled key when set, else true
// wherever the service listens — the queue bounds unbounded work, which
// a local caller can pile up as well as a remote one. An unknown key,
// a value of the wrong type, a max_inflight below 1 or a max_queue
// below 0 is a configuration error naming the key, whether or not the
// gate is on.
func serveConcurrency(v *viper.Viper, svc string) (cfg cmdsurface.Concurrency, on bool, err error) {
	cfg, on = cmdsurface.DefaultConcurrency(), true
	if v == nil {
		return cfg, on, nil
	}
	r := svcconfig.New(v)
	if err := r.ValidateBlock(concurrencyBlock, svc, svcconfig.Shared); err != nil {
		return cfg, false, err
	}
	if raw, key, ok := r.Lookup(svc, concurrencyBlock, concurrencyEnabled); ok {
		b, err := boolValue(raw)
		if err != nil {
			return cfg, false, fmt.Errorf("%s: %w", key, err)
		}
		on = b
	}
	if raw, key, ok := r.Lookup(svc, concurrencyBlock, concurrencyMaxInflight); ok {
		n, err := positiveCount(raw)
		if err != nil {
			return cfg, false, fmt.Errorf("%s: %w", key, err)
		}
		cfg.MaxInflight = n
	}
	if raw, key, ok := r.Lookup(svc, concurrencyBlock, concurrencyMaxQueue); ok {
		n, err := wholeCount(raw)
		if err == nil && (n < 0 || n > math.MaxInt32) {
			err = fmt.Errorf("%d is out of range; want 0 (no queue) to %d", n, math.MaxInt32)
		}
		if err != nil {
			return cfg, false, fmt.Errorf("%s: %w", key, err)
		}
		cfg.MaxQueue = int(n)
	}
	return cfg, on, nil
}

// serveConcurrencyOptions returns the bridge option installing svc's
// capacity gate, or none when it is off.
func (r *Root) serveConcurrencyOptions(svc string) ([]cmdsurface.Option, error) {
	cfg, on, err := serveConcurrency(r.Viper, svc)
	if err != nil || !on {
		return nil, err
	}
	return []cmdsurface.Option{cmdsurface.WithConcurrency(cfg)}, nil
}
