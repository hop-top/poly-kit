# Vendored Sources

| File              | Source                                                  | License |
|-------------------|---------------------------------------------------------|---------|
| presidio-pii.toml | github.com/microsoft/presidio @ 2.2.355                 | MIT     |
| LICENSE           | github.com/microsoft/presidio @ 2.2.355                 | MIT     |

## Pinned Commit

- Tag:    2.2.355
- URL:    https://github.com/microsoft/presidio/tree/2.2.355/presidio-analyzer/presidio_analyzer/predefined_recognizers

## Refresh

```
make refresh-pii-rules
```

The Makefile target re-runs `tools/vendor-presidio` against the latest
tagged release. Same tag → byte-identical output (idempotent). See
`go/core/redact/README.md` "Maintaining PII rules" for vetting guidance.

## Coverage (v1)

email, phone (US + E.164), us-ssn, us-itin, credit-card (regex-only,
no Luhn validation), iban, ipv4, ipv6, us-passport, us-driver-license.

## Dropped Patterns

The following upstream Presidio patterns were dropped because they use
RE2-incompatible features (backreferences or lookaround) or because
their false-positive rate in unstructured text is unacceptable:

- `CRYPTO` (Bitcoin/Eth address detection): too noisy without context
  classifier; bring back when we have entropy filtering.
- `MEDICAL_LICENSE`: state-specific patterns differ wildly; punt to v2.
- `UK_NHS`: locale-specific; not in v1 coverage target.
- `URL`: belongs to a separate URL-redact rule, not PII.

## Conflict Resolution

When loaded alongside the gitleaks corpus, rule-id collisions resolve
in favour of gitleaks (its corpus is broader and updated more often).
PII rules with conflicting ids get the suffix `-pii` at load time
(see `loader.go::AddPresidio`).

## Provenance Note

Patterns were hand-translated rather than mined automatically because
Presidio's recognizers are Python `Pattern` objects, not portable
regex strings. The TS + Python ports of kit/redact load this same TOML
verbatim — keeps the three runtimes in sync without re-derivation.
