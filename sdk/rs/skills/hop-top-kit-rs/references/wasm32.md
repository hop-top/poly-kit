# wasm32

Only `wasm32-unknown-unknown` is built, with `--no-default-features`. CI builds these: `bus`,
`netpolicy`, `domain`, `upgrade`, `sideeffect`, `breaker`, `sync`, `consent`, `llm`, `llm-picker`,
`redact`, `policy`, `notify`, `authn`, `identity`. File, process and clock code is compiled out
there: pass the clock, seeds and transport yourself. A feature not listed is not built for wasm32 in
CI: treat it as native (`cli`, `serve`, `api`, `telemetry`, `-sqlite`, `-fsdir`, `-postgres`,
`-real`, `-http`, `-server`).
