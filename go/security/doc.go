// Package security answers one question: can I trust this artifact
// and this execution?
//
// Rule of thumb: identity is keys you own; security is claims made by
// things you did not produce or cannot fully control — a signature on
// a download, code you run in a sandbox, a log someone might tamper
// with.
//
// The package covers exactly four families:
//
//   - Artifact signing and verification — sigstore/cosign keyless
//     bundles, minisign, SLSA provenance attestations — consumed by
//     the upgrade module before it installs a downloaded release.
//   - Sandboxed exec, as an adapter behind the Exec seam of
//     hop.top/kit/go/runtime/sideeffect, for code the tool does not
//     trust (adopter plugins, agent-suggested commands).
//   - A tamper-evident, hash-chained, append-only audit log feeding
//     hop.top/kit/go/runtime/provenance.
//   - SARIF normalization, so scanner findings (gosec, semgrep,
//     trivy, grype, govulncheck, osv-scanner) render through the
//     standard output layer. Not a scanner runner.
//
// Neighbors it defers to, by import path:
//
//   - keys and tokens: hop.top/kit/go/core/identity
//   - secret material: hop.top/kit/go/storage/secret
//   - egress and TLS: hop.top/kit/go/core/netpolicy
//   - rules: hop.top/kit/go/runtime/policy and hop.top/kit/go/core/scope
//   - data hygiene: hop.top/kit/go/core/redact
//
// Repository trust scoring (OSSF Scorecard, deps.dev and the like) is
// out of scope for kit; that job belongs to a dedicated tool such as
// rsx.
//
// There is no importable API yet. Each family ships as its own change;
// gaps_test.go pins one skipped test per family until then.
package security
