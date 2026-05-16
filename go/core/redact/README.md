# redact

Egress filter for kit-based tools. Strips secrets, tokens, and PII from text
before it leaves the process — log lines, telemetry payloads, LLM prompts,
error reports, anything user-facing that may have absorbed sensitive data.

## When to use

| Situation | API |
|-----------|-----|
| Build a redactor in code | `redact.New().AddRule(...)` |
| Use the package-level default (gitleaks + Presidio loaded) | `redact.Default()` |
| Redact a string | `Redactor.Apply(s)` |
| Redact bytes (no string conversion) | `Redactor.ApplyBytes(b)` |
| Audit-mode: find without replacing | `Redactor.Scan(s)` |
| Pick a replacement strategy | `Redactor.SetReplacement(redact.Tag)` |
| Pass-through known-safe values | `Redactor.Allow("sk-test", "AKIA...EXAMPLE")` |
| Audit hook (per match) | `Redactor.OnMatch(func(m){...})` |
| Snapshot counters | `Redactor.Stats()` |
| Add a rule from a Go literal | `Redactor.AddRule(id, pattern, replacement)` |
| Bulk-add pre-compiled rules | `Redactor.AddRules(rules...)` |
| Override embedded gitleaks corpus | `KIT_REDACT_RULES_PATH=/path/to/file.toml` |
| Override embedded Presidio corpus | `KIT_REDACT_PII_RULES_PATH=/path/to/file.toml` |

## Engine choice (RE2)

The engine is stdlib `regexp` (RE2). Linear-time matching is the entire
reason this can run on adversarial input — LLM outputs, scraped pages,
attacker-supplied prompts. PCRE-style features (backrefs, lookaround) are
intentionally absent: they enable catastrophic backtracking, which is a
remote DoS vector when the redactor sits on every log line.

Trade-offs:

- A handful of upstream gitleaks / Presidio patterns use RE2-incompatible
  features. Loader skips them silently rather than refusing the corpus.
  See `loader.go::LoadGitleaks` for the policy.
- No PCRE / Hyperscan / `go-regexp/v2` dependency. Zero new runtime deps
  beyond what kit/scope already pulls in (`BurntSushi/toml`).
- Allowlists are substring-only — never regex. Regex allowlists invite
  ReDoS back in via the side door.

## Replacement strategies

Per-Redactor, not per-Rule. Pick one:

| Strategy | Output for `OPENAI_API_KEY=sk-abc123def...` | Use when |
|----------|---------------------------------------------|----------|
| `Mask` (default) | `OPENAI_API_KEY=***REDACTED***` | Compliance / aggressive scrub |
| `Tag` | `OPENAI_API_KEY=<openai-api-key>` | Diagnosable logs (recommended) |
| `Hash` | `OPENAI_API_KEY=sha256:6ca13d52` | Log correlation across runs |
| `Custom` | user-supplied `func(Match) string` | Bespoke routing / metrics |

`Tag` reads each rule's preferred label (e.g. `<OPENAI_KEY>`) from the
loaded TOML when present; otherwise falls back to `<rule-id>`. `Custom`
panics are recovered and degraded to `Mask` so a buggy formatter cannot
take down the egress path.

## Allowlists

Two layers, both string-substring (no regex):

1. **Global** — `Redactor.Allow("sk-test", "AKIA...EXAMPLE")`. Any match
   whose original contains an allowed substring passes through unchanged
   with no observer fire.
2. **Per-rule** — loaded from TOML `allowlist = [...]` keys. Same
   semantics, scoped to that rule.

Use for:

- Documentation examples (`sk-test123`, `AKIA...EXAMPLE`).
- Local-dev placeholders (`OPENAI_API_KEY=test`).
- Fixture data in tests.

Never use to whitelist a real customer's secret prefix — that is a
backdoor disguised as ergonomics.

## Examples

### Log line redaction (slog handler wrap)

Pseudocode:

```
// Wrap any io.Writer (e.g. os.Stderr) so writes pass through Default().
type RedactWriter struct{ inner io.Writer }
func (w RedactWriter) Write(p []byte) (int, error) {
    return w.inner.Write(redact.Default().ApplyBytes(p))
}

handler := slog.NewTextHandler(RedactWriter{os.Stderr}, nil)
slog.SetDefault(slog.New(handler))
```

> [!CAUTION]
> Per-log-line wrapping is **NOT** safe for high-volume hot paths today —
> `Apply` is currently ~880x over the documented 50µs budget on a clean
> 4KB line. See [PERF.md](PERF.md). Use it for low-frequency emitters
> (CLI output, error reports, daily summary lines) until the
> Aho-Corasick pre-screen optimization lands.

### LLM prompt cleaning

Pseudocode:

```
func sendPrompt(p string) (string, error) {
    cleaned := redact.Default().Apply(p)
    return llmClient.Complete(cleaned)
}
```

Prompts can absorb env vars, file contents, and prior tool output. The
default policy catches gitleaks-tracked secrets + Presidio PII before
they reach the model.

### Telemetry filtering with audit observer

Pseudocode:

```
r := redact.Default().OnMatch(func(m redact.Match) {
    // Never log m.Original — that defeats the point.
    metrics.Counter("redact.match", "rule", m.RuleID).Inc()
})

batch := buildTelemetryBatch()
clean := r.Apply(batch)
telemetryClient.Send(clean)
```

The observer fires after allowlist filtering, before substitution, so it
sees the actual matched text. Treat that text as toxic — increment
counters, never persist it.

### Custom strategy (route by rule)

Pseudocode:

```
r := redact.New()
r.AddRule("internal-id", `INTERNAL-\d{8}`, "")
r.SetReplacement(redact.Custom, func(m redact.Match) string {
    if m.RuleID == "internal-id" {
        return lookupAnonymisedAlias(m.Original) // hashed externally
    }
    return "***REDACTED***"
})
```

## Rule sources

| Corpus | Path | Refresh |
|--------|------|---------|
| Gitleaks content rules (~211) | `../scope/rules/gitleaks-content.toml` (shared with kit/scope) | `make refresh-secret-rules` |
| Presidio PII pack (~11) | `rules/presidio-pii.toml` | `make refresh-pii-rules` |

`Default()` loads both at first use via `sync.Once`. Gitleaks loads
first; conflicting Presidio rule ids get the `-pii` suffix.

## Limits

- **Performance** — current `Apply` cost is ~44ms on a 4KB clean payload
  with the full ~250-rule default policy. ~880x over the design budget.
  Roadmap in [PERF.md](PERF.md). Until Aho-Corasick pre-screen lands,
  use audit-mode (`Scan`) or amortise across heavyweight payloads.
- **No verified-secret check** — pattern matching only. We do not call
  provider APIs to confirm a match is a live key. That is trufflehog's
  territory, deliberately out of scope.
- **No volume / rate limiting** — kit/breaker (separate primitive) owns
  egress rate control.
- **No path/host filtering** — kit/scope owns FS guardrails;
  kit/netscope (future) owns network host filtering.
- **No structured-field awareness** — `Apply` operates on flat strings.
  Callers wrapping a structured logger should redact the value side
  only, not keys (per kit/log conventions).
- **Cross-language ports do not use RE2-equivalent engines.** v1 ships
  with documented input-size guidance. See ADR-0005.

## Maintaining PII rules

The Presidio PII pack lives at `rules/presidio-pii.toml`. It is
AUTO-GENERATED by `tools/vendor-presidio` and refreshed via:

```
make refresh-pii-rules
```

The refresh fetches the latest tagged Presidio release, stamps its tag
into the rule file's provenance header, and rewrites
`rules/{presidio-pii.toml,SOURCES.md,NOTICE,LICENSE}`. Same tag +
unchanged `tools/vendor-presidio/curated.go` → byte-identical output.

### When to refresh

- New Presidio release announces a new recognizer covering a pattern
  class we don't already redact (e.g. a new national-ID format).
- Quarterly hygiene: bump the pin even with no curated changes so the
  attribution + LICENSE bytes stay current.
- After a leak postmortem identifies a pattern we missed.

### How to vet diffs

1. Read the upstream changelog at
   <https://github.com/microsoft/presidio/releases> for new recognizers.
2. If a new pattern is added: edit
   `tools/vendor-presidio/curated.go` to include it. Verify it is
   RE2-clean by running `go test ./go/core/redact/...` after refresh.
3. Watch for over-broad patterns: anything that would redact common
   non-PII text (e.g. a 9-digit-number rule that swallows phone numbers
   AND order IDs). Either tighten the regex with anchors / word
   boundaries, or add a global allowlist substring via
   `Redactor.Allow(...)`.
4. If the upstream TOML schema or recognizer API changes (it has been
   stable since 2.x), update `loader.go::presidioRule` to match.

### How to handle Presidio API changes

The `vendor-presidio` tool deliberately does not parse Python source.
Recognizer files are scattered across
`presidio-analyzer/presidio_analyzer/predefined_recognizers/` in
inconsistent shapes; auto-extraction is brittle and pulls in a Python
toolchain at vendor time. Instead, the curated rule body lives in
`tools/vendor-presidio/curated.go` and the tool stamps provenance
headers around it.

Trade-off: refresh is partly manual (must read the changelog and update
`curated.go` for new recognizers). Benefit: the tool is dependency-free
and idempotent — same tag → byte-identical output.

## Refreshing all rule corpora

```
make refresh-rules     # gitleaks + Presidio in one step
```

Both refresh targets are independent and safe to run in any order.

## See also

- [PERF.md](PERF.md) — performance budget + optimization roadmap
- [kit/scope](../scope/README.md) — sibling guardrail for FS paths
