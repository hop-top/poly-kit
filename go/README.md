# go

core polyglot library implementations.

## Packages

- [ai/](ai/README.md): AI-facing kernel packages.
- [bridge/](bridge/README.md): kit/bridge payload types and manifest loader.
- [conformance/](conformance/README.md): Layer-A adopter conformance test helper.
- [console/](console/README.md): human-facing CLI/TUI behavior.
- [core/](core/README.md): operational primitives.
- [integrations/](integrations/README.md): cross-cutting adapters; repo-host SPI.
- [runtime/](runtime/README.md): execution logic and mesh networking.
- [security/](security/) — placeholder: no code ships here yet, so it
  carries no README. Reserved for security tooling wrappers (scorecard,
  sast adapters); the intended surface is pinned by the skipped gap test
  in `security/gaps_test.go`.
- [storage/](storage/README.md): persistence layers.
- [tools/](tools/README.md): static-analysis helpers for adopters.
- [transport/](transport/README.md): network exposure.
