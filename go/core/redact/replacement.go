package redact

import (
	"crypto/sha256"
	"encoding/hex"
)

// format dispatches to the active strategy and renders a replacement for m.
// Custom panics are recovered and degrade to Mask so a buggy formatter
// can't take down a log path.
func (r *Redactor) format(rule *Rule, m Match) string {
	switch r.strategy {
	case Tag:
		return tag(rule)
	case Hash:
		return hash(m)
	case Custom:
		if r.custom == nil {
			return mask(m)
		}
		return r.safeCustom(r.custom, m)
	default:
		return mask(m)
	}
}

func mask(_ Match) string { return "***REDACTED***" }

// tag returns the rule's preferred replacement template if non-empty, else
// "<rule-id>", honoring the per-rule label loaded from gitleaks/Presidio
// TOML.
func tag(rule *Rule) string {
	if rule.spec.Replacement != "" {
		return rule.spec.Replacement
	}
	return "<" + rule.spec.ID + ">"
}

// hash returns sha256:<first-8-hex-bytes-of-original>. Stable across runs.
func hash(m Match) string {
	sum := sha256.Sum256([]byte(m.Original))
	return "sha256:" + hex.EncodeToString(sum[:4])
}

// safeCustom invokes fn under a recover so a panic falls back to Mask.
// The panic is logged via the redactor's logger at warn level (no
// Original text included — that would defeat the point of redaction).
// Method on *Redactor so the injected logger (see WithLogger) is in
// scope without changing the format dispatch signature.
func (r *Redactor) safeCustom(fn func(Match) string, m Match) (out string) {
	defer func() {
		if rec := recover(); rec != nil {
			r.logger.Warn("redact: custom formatter panicked; falling back to Mask",
				"rule", m.RuleID, "panic", rec)
			out = mask(m)
		}
	}()
	return fn(m)
}
