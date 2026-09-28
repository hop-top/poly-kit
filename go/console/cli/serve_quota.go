package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"

	"hop.top/kit/go/console/cli/svcconfig"
	"hop.top/kit/go/transport/cmdsurface"
)

// The quota block bounds how much one caller may use a service over a
// window: invocation-plane slot 9, counted after each successful run
// (slot 13). Counts persist in the usage store — the one policy
// budgets use, $XDG_STATE_HOME/<tool>/usage.db unless WithUsageStore
// names another — so a restart resets nothing. A refused call is
// quota_exceeded: HTTP 429 with Retry-After at the window's reset,
// Connect ResourceExhausted, an MCP isError result, the socket's
// QUOTA_EXCEEDED; exit class 64.
//
//	services:
//	  all:
//	    quota:
//	      per: principal      # or tenant; default principal
//	      window: 24h         # default 1h; aligned to the epoch (UTC days)
//	      ops: 10000          # calls per window; 0 or unset: no call limit
//	      bytes: 104857600    # output bytes per window; 0 or unset: no byte limit
//	  api:
//	    quota:
//	      enabled: false      # this service only
//
// The quota is on when ops or bytes is set, unless enabled is false;
// enabled: true with neither is a configuration error. Each service
// counts its own callers. The keys resolve per key, the service's
// before services.all.
const (
	quotaBlock   = "quota"
	quotaEnabled = "enabled"
	quotaPer     = "per"
	quotaWindow  = "window"
	quotaOps     = "ops"
	quotaBytes   = "bytes"

	defaultQuotaWindow = time.Hour
)

// serveQuota resolves svc's quota block. on reports whether the gate
// is installed. An unknown key, a value of the wrong type, a window
// under a second, or enabled with no limit is a configuration error
// naming the key, whether or not the gate is on.
func serveQuota(v *viper.Viper, svc string) (q cmdsurface.Quota, on bool, err error) {
	q = cmdsurface.Quota{Scope: svc, Per: cmdsurface.QuotaPerPrincipal, Window: defaultQuotaWindow}
	if v == nil {
		return q, false, nil
	}
	r := svcconfig.New(v)
	if err := r.ValidateBlock(quotaBlock, svc, svcconfig.Shared); err != nil {
		return q, false, err
	}
	enabled, enabledKey := true, ""
	if raw, key, ok := r.Lookup(svc, quotaBlock, quotaEnabled); ok {
		b, err := boolValue(raw)
		if err != nil {
			return q, false, fmt.Errorf("%s: %w", key, err)
		}
		enabled, enabledKey = b, key
	}
	if raw, key, ok := r.Lookup(svc, quotaBlock, quotaPer); ok {
		switch per := cmdsurface.QuotaPer(strings.TrimSpace(fmt.Sprint(raw))); per {
		case cmdsurface.QuotaPerPrincipal, cmdsurface.QuotaPerTenant:
			q.Per = per
		default:
			return q, false, fmt.Errorf("%s: %q; want %q or %q",
				key, raw, cmdsurface.QuotaPerPrincipal, cmdsurface.QuotaPerTenant)
		}
	}
	if raw, key, ok := r.Lookup(svc, quotaBlock, quotaWindow); ok {
		d, err := durationValue(raw)
		if err == nil && d < time.Second {
			err = fmt.Errorf("%s is under a second; want a window such as 1h or 24h", d)
		}
		if err != nil {
			return q, false, fmt.Errorf("%s: %w", key, err)
		}
		q.Window = d
	}
	for _, k := range []string{quotaOps, quotaBytes} {
		raw, key, ok := r.Lookup(svc, quotaBlock, k)
		if !ok {
			continue
		}
		n, err := wholeBytes(raw)
		if err != nil && k == quotaOps {
			err = fmt.Errorf("%s", strings.Replace(err.Error(), " of bytes", " of calls", 1))
		}
		if err == nil && n < 0 {
			err = fmt.Errorf("%d is negative; 0 means no limit", n)
		}
		if err != nil {
			return q, false, fmt.Errorf("%s: %w", key, err)
		}
		if k == quotaOps {
			q.Ops = n
		} else {
			q.Bytes = n
		}
	}
	limited := q.Ops > 0 || q.Bytes > 0
	if enabledKey != "" && enabled && !limited {
		return q, false, fmt.Errorf("%s: true with no limit; set %s or %s",
			enabledKey, svcconfig.Key(svc, quotaBlock, quotaOps), svcconfig.Key(svc, quotaBlock, quotaBytes))
	}
	return q, enabled && limited, nil
}

// serveQuotaOptions returns the bridge option installing svc's quota,
// or none when it is off.
func (r *Root) serveQuotaOptions(svc string) ([]cmdsurface.Option, error) {
	q, on, err := serveQuota(r.Viper, svc)
	if err != nil || !on {
		return nil, err
	}
	ledger, err := usageLedgerOf(r)
	if err != nil {
		return nil, fmt.Errorf("services.%s.%s: %w", svc, quotaBlock, err)
	}
	return []cmdsurface.Option{cmdsurface.WithQuota(q, ledger)}, nil
}
