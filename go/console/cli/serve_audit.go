package cli

import (
	"fmt"

	"github.com/spf13/viper"

	"hop.top/kit/go/console/cli/svcconfig"
	"hop.top/kit/go/core/redact"
	"hop.top/kit/go/transport/cmdsurface"
)

// The audit.redact block adds audit redaction for one service, or for
// every service under services.all. Redaction of secret flags is a
// security floor, so the block has no enabled key and nothing in it
// can switch redaction off: it only adds flags and content rules.
//
//	services:
//	  all:
//	    audit:
//	      redact:
//	        secret_flags: [dsn]           # masked on every service
//	        patterns: ['acme_[a-z0-9]{32}']
//	  api:
//	    audit:
//	      redact:
//	        secret_flags: [dsn, conn]     # replaces the services.all list for api
//
// The block's keys are registered in svcconfig. Each resolves on its
// own: the service's key from any source, then the services.all key,
// then the kit default (nothing added).
const (
	auditRedactBlock       = "audit.redact"
	auditRedactSecretFlags = "secret_flags"
	auditRedactPatterns    = "patterns"
)

// serveAuditRedaction resolves the audit.redact block for svc into
// the extra redaction its bridge applies. An unknown key in the block
// (enabled included) or a pattern that does not compile is a
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
	return out, nil
}
