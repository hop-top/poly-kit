package cli

import (
	"fmt"
	"math"

	"github.com/spf13/viper"

	"hop.top/kit/go/console/cli/svcconfig"
	"hop.top/kit/go/core/redact"
	"hop.top/kit/go/transport/cmdsurface"
)

// The audit.redact block adds audit redaction for one service, or for
// every service under services.all. Redaction of secret flags is a
// security floor, so the block has no enabled key and nothing in it
// can switch redaction off: it only adds flags and content rules, and
// sets the longest field scanned (a longer one is withheld whole).
//
//	services:
//	  all:
//	    audit:
//	      redact:
//	        secret_flags: [dsn]           # masked on every service
//	        patterns: ['acme_[a-z0-9]{32}']
//	        max_field_bytes: 8192         # default 4096
//	  api:
//	    audit:
//	      redact:
//	        secret_flags: [dsn, conn]     # replaces the services.all list for api
//
// The block's keys are registered in svcconfig. Each resolves on its
// own: the service's key from any source, then the services.all key,
// then the kit default (nothing added; the cmdsurface scan limit).
const (
	auditRedactBlock         = "audit.redact"
	auditRedactSecretFlags   = "secret_flags"
	auditRedactPatterns      = "patterns"
	auditRedactMaxFieldBytes = "max_field_bytes"
)

// serveAuditRedaction resolves the audit.redact block for svc into
// the extra redaction its bridge applies. An unknown key in the block
// (enabled included), a pattern that does not compile, or a
// max_field_bytes that is not a positive whole number is a
// configuration error, reported at validation.
func serveAuditRedaction(v *viper.Viper, svc string) (cmdsurface.AuditRedaction, error) {
	var out cmdsurface.AuditRedaction
	if v == nil {
		return out, nil
	}
	cfg := svcconfig.New(v)
	if err := cfg.ValidateBlock(auditRedactBlock, svc, svcconfig.Shared); err != nil {
		return out, err
	}
	if _, k, ok := cfg.Lookup(svc, auditRedactBlock, auditRedactSecretFlags); ok {
		out.SecretFlags = v.GetStringSlice(k)
	}
	if _, k, ok := cfg.Lookup(svc, auditRedactBlock, auditRedactPatterns); ok {
		for i, p := range v.GetStringSlice(k) {
			rule, err := redact.NewRule(fmt.Sprintf("config-pattern-%d", i+1), p, "")
			if err != nil {
				return cmdsurface.AuditRedaction{}, fmt.Errorf("%s[%d]: %w", k, i, err)
			}
			out.Rules = append(out.Rules, rule)
		}
	}
	if raw, k, ok := cfg.Lookup(svc, auditRedactBlock, auditRedactMaxFieldBytes); ok {
		n, err := wholeBytes(raw)
		if err == nil && (n <= 0 || n > math.MaxInt32) {
			err = fmt.Errorf("%d is out of range; want 1 to %d bytes (default %d)", n, math.MaxInt32, cmdsurface.DefaultAuditMaxFieldBytes)
		}
		if err != nil {
			return cmdsurface.AuditRedaction{}, fmt.Errorf("%s: %w", k, err)
		}
		out.MaxFieldBytes = int(n)
	}
	return out, nil
}

// validateServeAudit refuses svc's audit configuration when either
// block does not parse: the audit.redact block or the audit.sinks list.
func validateServeAudit(r *Root, svc string) error {
	if _, err := serveAuditRedaction(r.Viper, svc); err != nil {
		return err
	}
	_, err := serveAuditSinkConfigs(r.Viper, r.Config.Name, svc)
	return err
}
