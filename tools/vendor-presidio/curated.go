package main

import "fmt"

// piiBody is the curated rule TOML body, byte-identical to what we want
// committed under go/core/redact/rules/presidio-pii.toml minus the
// AUTO-GENERATED provenance header (which is stamped per-tag at render
// time).
//
// Edit this when refreshing for a new Presidio release: review the
// upstream changelog at https://github.com/microsoft/presidio/releases
// for new recognizers, add corresponding rules here, then re-run the
// tool. Patterns must be RE2-clean — verify in
// go/core/redact/loader_test.go (patterns that fail to compile are
// skipped silently at load time, but tests should still pass).
const piiBody = `
[[rule]]
id = "email"
description = "Email address (RFC 5322 simplified)."
pattern = '''([a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,})'''
replacement = "<EMAIL>"

[[rule]]
id = "phone-e164"
description = "International phone number in E.164 format (+CCNNNNNNNNN)."
pattern = '''\+[1-9]\d{6,14}\b'''
replacement = "<PHONE>"

[[rule]]
id = "phone-us"
description = "US phone number with optional country code, dashes/dots/parens."
pattern = '''\b(?:\+?1[\s.\-]?)?\(?\d{3}\)?[\s.\-]\d{3}[\s.\-]\d{4}\b'''
replacement = "<PHONE>"

[[rule]]
id = "us-ssn"
description = "US Social Security Number (NNN-NN-NNNN)."
pattern = '''\b\d{3}-\d{2}-\d{4}\b'''
replacement = "<SSN>"

[[rule]]
id = "us-itin"
description = "US Individual Taxpayer Identification Number (9XX-NN-NNNN)."
pattern = '''\b9\d{2}-\d{2}-\d{4}\b'''
replacement = "<ITIN>"

[[rule]]
id = "credit-card"
description = "Credit card number (Visa/MC/Amex/Discover; 13-19 digits with optional separators). Luhn validation deferred to v2."
pattern = '''\b(?:\d[ \-]?){12,18}\d\b'''
replacement = "<CREDIT_CARD>"

[[rule]]
id = "iban"
description = "International Bank Account Number (ISO 13616, 15-34 alphanumeric chars after country prefix)."
pattern = '''\b[A-Z]{2}\d{2}[A-Z0-9]{11,30}\b'''
replacement = "<IBAN>"

[[rule]]
id = "ipv4"
description = "IPv4 dotted-quad address."
pattern = '''\b(?:(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\b'''
replacement = "<IP>"

[[rule]]
id = "ipv6"
description = "IPv6 address (full or compressed). Compressed form approximation: ::-prefix or middle ::."
pattern = '''\b(?:[0-9a-fA-F]{1,4}:){7}[0-9a-fA-F]{1,4}\b|\b(?:[0-9a-fA-F]{1,4}:){1,7}:|\b(?:[0-9a-fA-F]{1,4}:){1,6}:[0-9a-fA-F]{1,4}\b'''
replacement = "<IP>"

[[rule]]
id = "us-passport"
description = "US passport number (9 digits, may be prefixed with letter)."
pattern = '''\b[A-Z]?\d{9}\b'''
replacement = "<US_PASSPORT>"

[[rule]]
id = "us-driver-license"
description = "US driver license — generic 7-13 alphanumeric. State-specific patterns vary widely; this is a coarse catch."
pattern = '''\b[A-Z]\d{7,13}\b'''
replacement = "<US_DRIVER_LICENSE>"
`

func renderTOML(tag string) string {
	return fmt.Sprintf(`# Vendored Presidio PII patterns. AUTO-GENERATED — refresh via
# `+"`make refresh-pii-rules`"+` when bumping the Presidio pin.
#
# Source:  github.com/%s/%s @ %s
# License: MIT (see LICENSE in this dir; original Microsoft Presidio © 2020)
# Refresh: make refresh-pii-rules
#
# Patterns hand-curated from
# presidio-analyzer/presidio_analyzer/predefined_recognizers/*.py and
# verified RE2-clean. Patterns that used backrefs or lookaround in
# upstream were dropped (see SOURCES.md "Dropped patterns" section).
%s`, owner, repo, tag, piiBody)
}

func renderSources(tag string) string {
	return fmt.Sprintf(`# Vendored Sources

| File              | Source                                                  | License |
|-------------------|---------------------------------------------------------|---------|
| presidio-pii.toml | github.com/%s/%s @ %s                 | MIT     |
| LICENSE           | github.com/%s/%s @ %s                 | MIT     |

## Pinned Commit

- Tag:    %s
- URL:    https://github.com/%s/%s/tree/%s/presidio-analyzer/presidio_analyzer/predefined_recognizers

## Refresh

`+"```"+`
make refresh-pii-rules
`+"```"+`

The Makefile target re-runs `+"`tools/vendor-presidio`"+` against the latest
tagged release. Same tag → byte-identical output (idempotent). See
`+"`go/core/redact/README.md`"+` "Maintaining PII rules" for vetting guidance.

## Coverage (v1)

email, phone (US + E.164), us-ssn, us-itin, credit-card (regex-only,
no Luhn validation), iban, ipv4, ipv6, us-passport, us-driver-license.

## Dropped Patterns

The following upstream Presidio patterns were dropped because they use
RE2-incompatible features (backreferences or lookaround) or because
their false-positive rate in unstructured text is unacceptable:

- `+"`CRYPTO`"+` (Bitcoin/Eth address detection): too noisy without context
  classifier; bring back when we have entropy filtering.
- `+"`MEDICAL_LICENSE`"+`: state-specific patterns differ wildly; punt to v2.
- `+"`UK_NHS`"+`: locale-specific; not in v1 coverage target.
- `+"`URL`"+`: belongs to a separate URL-redact rule, not PII.

## Conflict Resolution

When loaded alongside the gitleaks corpus, rule-id collisions resolve
in favour of gitleaks (its corpus is broader and updated more often).
PII rules with conflicting ids get the suffix `+"`-pii`"+` at load time
(see `+"`loader.go::Default()`"+`).

## Provenance Note

Patterns were hand-translated rather than mined automatically because
Presidio's recognizers are Python `+"`Pattern`"+` objects, not portable
regex strings. The TS + Python ports of kit/redact load this same TOML
verbatim — keeps the three runtimes in sync without re-derivation.
`, owner, repo, tag, owner, repo, tag, tag, owner, repo, tag)
}

func renderNotice(tag string) string {
	return fmt.Sprintf(`kit/redact vendored PII rules

This directory contains regex patterns hand-translated from the
Microsoft Presidio project (https://github.com/%s/%s), pinned to
release %s.

Original work Copyright (c) Microsoft Corporation. Licensed under the
MIT License (see LICENSE).

Modifications by the kit project: regex strings extracted from
Presidio's Python recognizers and reformatted into a Go-friendly TOML
schema. RE2-incompatible patterns dropped (see SOURCES.md). No rule
semantics changed for retained patterns.
`, owner, repo, tag)
}
