package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/viper"

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
const (
	serveAllScope          = "all"
	auditRedactBlock       = "audit.redact"
	auditRedactSecretFlags = "secret_flags"
	auditRedactPatterns    = "patterns"
)

// auditRedactKeys are the keys the audit.redact block accepts.
var auditRedactKeys = []string{auditRedactPatterns, auditRedactSecretFlags}

// auditRedactKey resolves which config key supplies key of the
// audit.redact block for svc: the service's own key from any source,
// then the services.all key. It returns "" when neither is set, and
// the kit default (nothing added) applies. Key lookup lives here
// alone, so a shared middleware resolver can replace it.
func auditRedactKey(v *viper.Viper, svc, key string) string {
	for _, scope := range []string{svc, serveAllScope} {
		k := serveKeyPrefix + scope + "." + auditRedactBlock + "." + key
		if v.IsSet(k) {
			return k
		}
	}
	return ""
}

// serveAuditRedaction resolves the audit.redact block for svc into
// the extra redaction its bridge applies. An unknown key in the block
// (enabled included) or a pattern that does not compile is a
// configuration error, reported at validation.
func serveAuditRedaction(v *viper.Viper, svc string) (cmdsurface.AuditRedaction, error) {
	var out cmdsurface.AuditRedaction
	if v == nil {
		return out, nil
	}
	for _, scope := range []string{svc, serveAllScope} {
		block := serveKeyPrefix + scope + "." + auditRedactBlock
		var unknown []string
		for k := range v.GetStringMap(block) {
			if k != auditRedactSecretFlags && k != auditRedactPatterns {
				unknown = append(unknown, k)
			}
		}
		if len(unknown) > 0 {
			sort.Strings(unknown)
			return out, fmt.Errorf("%s: unknown key %s; the block takes %s, and secret-flag redaction cannot be switched off",
				block, strings.Join(unknown, ", "), strings.Join(auditRedactKeys, " and "))
		}
	}
	if k := auditRedactKey(v, svc, auditRedactSecretFlags); k != "" {
		out.SecretFlags = v.GetStringSlice(k)
	}
	if k := auditRedactKey(v, svc, auditRedactPatterns); k != "" {
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
