package cli

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/viper"

	"hop.top/kit/go/console/cli/svcconfig"
	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
)

// Middleware block keys for timeouts. The block is
// services.<svc>.timeouts, with shared defaults under
// services.all.timeouts. read_header, read, write and idle are the
// HTTP listener's server timeouts; command is the per-command
// deadline every surface arms at the capacity slot.
const (
	timeoutsBlock      = "timeouts"
	timeoutsReadHeader = "read_header"
	timeoutsRead       = "read"
	timeoutsWrite      = "write"
	timeoutsIdle       = "idle"
	timeoutsCommand    = "command"
)

// serveServerTimeouts resolves svc's HTTP server timeouts, per key:
// services.<svc>.timeouts.*, then services.all.timeouts.*, then
// fallback — the service's code default.
func serveServerTimeouts(v *viper.Viper, svc string, fallback api.ServerTimeouts) (api.ServerTimeouts, error) {
	cfg := svcconfig.New(v)
	if err := cfg.ValidateBlock(timeoutsBlock, svc, svcconfig.Shared); err != nil {
		return api.ServerTimeouts{}, err
	}
	t := fallback
	for _, f := range []struct {
		key string
		dst *time.Duration
	}{
		{timeoutsReadHeader, &t.ReadHeader},
		{timeoutsRead, &t.Read},
		{timeoutsWrite, &t.Write},
		{timeoutsIdle, &t.Idle},
	} {
		d, set, err := timeoutValue(cfg, svc, f.key)
		if err != nil {
			return api.ServerTimeouts{}, err
		}
		if set {
			*f.dst = d
		}
	}
	return t, nil
}

// serveCommandTimeout resolves svc's per-command deadline default,
// timeouts.command: the deadline of a command that declares no
// kit/timeout. Zero, the default, is none.
func serveCommandTimeout(v *viper.Viper, svc string) (time.Duration, error) {
	cfg := svcconfig.New(v)
	if err := cfg.ValidateBlock(timeoutsBlock, svc, svcconfig.Shared); err != nil {
		return 0, err
	}
	d, _, err := timeoutValue(cfg, svc, timeoutsCommand)
	return d, err
}

// timeoutValue reads one timeouts key: a duration ("5s", "2m"), or 0
// for none. set is false when neither the service nor services.all
// sets it.
func timeoutValue(cfg svcconfig.Resolver, svc, key string) (d time.Duration, set bool, err error) {
	raw, from, ok := cfg.Lookup(svc, timeoutsBlock, key)
	if !ok {
		return 0, false, nil
	}
	d, err = durationValue(raw)
	if err == nil && d < 0 {
		err = fmt.Errorf("%s is negative; use 0 for no timeout", d)
	}
	if err != nil {
		return 0, false, fmt.Errorf("%s: %w", from, err)
	}
	return d, true, nil
}

// durationValue reads a duration config value: a time.Duration, a
// string Go's duration syntax parses, or a zero number. A bare
// non-zero number is refused rather than read in some unit nobody
// wrote.
func durationValue(raw any) (time.Duration, error) {
	switch x := raw.(type) {
	case time.Duration:
		return x, nil
	case string:
		s := strings.TrimSpace(x)
		if s == "0" {
			return 0, nil
		}
		d, err := time.ParseDuration(s)
		if err != nil {
			return 0, fmt.Errorf("%q is not a duration; want one such as 5s or 2m", x)
		}
		return d, nil
	case int, int64, int32, float64:
		if n, err := wholeBytes(x); err == nil && n == 0 {
			return 0, nil
		}
	}
	return 0, fmt.Errorf("%v is not a duration; want one such as 5s or 2m", raw)
}

// validateServeTimeouts refuses svc's timeouts block when a key is
// unknown or does not parse, and any command in the tree whose
// kit/timeout annotation does not parse. It runs in Validate, before
// anything binds.
func validateServeTimeouts(r *Root, svc string) error {
	var v *viper.Viper
	if r != nil {
		v = r.Viper
	}
	if _, err := serveServerTimeouts(v, svc, api.ServerTimeouts{}); err != nil {
		return err
	}
	if _, err := serveCommandTimeout(v, svc); err != nil {
		return err
	}
	if r == nil {
		return nil
	}
	return cmdsurface.ValidateCommandTimeouts(r.Cmd)
}

// ConfigureServeHTTP applies service svc's server settings to srv,
// the http.Server of one kit HTTP listener: the timeouts block's
// read_header, read, write and idle keys, each falling back to the
// matching field of fallback (the service's code default,
// [api.DefaultServerTimeouts] for the kit-shipped services). Call it
// once srv carries its Handler, before it serves. Validate has
// already refused a block that does not parse; were one to reach
// here, the error is returned rather than serving on timeouts nobody
// wrote.
func ConfigureServeHTTP(r *Root, svc string, srv *http.Server, fallback api.ServerTimeouts) error {
	var v *viper.Viper
	if r != nil {
		v = r.Viper
	}
	t, err := serveServerTimeouts(v, svc, fallback)
	if err != nil {
		return err
	}
	t.Apply(srv)
	return nil
}
