# Capability parity

> Per-SDK parity for every capability in [`capability-index`](capability-index.md).
> Go is the reference; TypeScript and Python are the other first-class SDKs;
> Rust and PHP are experimental. Every SDK is expected to reach `done` as more
> apps adopt it.

## Status vocabulary

| Status | Meaning |
|---|---|
| done | A parity contract, shared corpus or cross-language test pins the SDK's behavior to Go |
| partial | The SDK ships a subset: a documented gap, one leg of the capability, or a stub |
| none | No module for the capability in the SDK tree |
| n/a | The capability is a language seam that has no counterpart in that SDK |
| unverified | A module exists but nothing in the repo pins it to Go; depth not checked |

Status is evidence-based. The Evidence column names the guard (a `make
test-parity-*` target, a contract under `contracts/`, or `serve.json`) or the
module path. Path prefixes: `ts:` is `sdk/ts/src/`, `py:` is
`sdk/py/hop_top_kit/`, `rs:` is `sdk/experimental/rs/src/`, `php:` is
`sdk/experimental/php/src/`. Guards are defined in the root `Makefile` and in
[`contracts/parity`](../../../contracts/parity/README.md); the contracts are
described in [`parity-contract`](parity-contract.md).

## Summary

| SDK | Counts |
|---|---|
| Go | 74 done |
| TypeScript | 37 none, 19 unverified, 11 done, 6 partial, 1 n/a |
| Python | 39 none, 19 unverified, 9 done, 6 partial, 1 n/a |
| Rust | 42 none, 14 partial, 11 done, 6 unverified, 1 n/a |
| PHP | 60 none, 5 unverified, 4 partial, 4 done, 1 n/a |

Rust and PHP serve lifecycle parity is complete for the 13 behaviors in
`contracts/parity/serve.json`; the MCP wire is pinned for all four SDKs.
Most other `done` rows come from the shared corpora under `contracts/*-v1`,
which only the Go and Rust (and sometimes TypeScript and Python) runners read.

## CLI and console

| Capability | Go | TS | Py | Rust | PHP | Evidence |
|---|---|---|---|---|---|---|
| CLI factory | done | done | done | partial | partial | make test-parity-cli (Go/TS/Py); ts:cli.ts py:cli.py rs:cli/root.rs php:Cli/KitCommand.php; rs+php wire no global-flag layer (cli-parity-guide) |
| Output and errors | done | done | done | done | done | make test-parity-taxonomy (5 ports); ts:output/ py:output/ rs:output/ php:Output/ |
| Logging | done | unverified | unverified | none | none | ts:log.ts py:log.py; no rs/php module |
| Markdown rendering | done | none | none | none | none | no module in any SDK |
| Command annotations | done | n/a | n/a | n/a | n/a | cobra-only seam; SDKs use commander/typer/clap/symfony |
| Shell completion | done | done | unverified | done | none | make test-parity-completion (Go/Rs/TS) + test-parity-completion-spec (Go/Rs); py:completion.py (no corpus runner) |
| Aliases | done | done | done | done | none | make test-parity-alias (Go/Rs/TS/Py); ts:alias.ts py:alias.py rs:cli/alias.rs |
| Flag helpers | done | unverified | unverified | unverified | none | ts:setflag.ts textflag.ts flagregister.ts py:setflag.py textflag.py flagregister.py rs:cli/enums.rs |
| Help rendering | done | done | done | none | none | contracts/parity/parity.json help block (ts:tui/parity.ts py:parity.py) |
| Autocorrect | done | unverified | unverified | none | none | ts:errcorrect.ts py:errcorrect.py |
| Config path commands | done | none | none | none | none | rs-sdk.md: config command not ported; no ts/py/php module |
| URI scheme handlers | done | unverified | unverified | unverified | unverified | ts:uri.ts py:uri.py rs:uri.rs php:Uri/ (facades over the cite lib) |
| Fuzzy matching | done | none | none | none | none | no module in any SDK |
| Process status | done | none | none | none | none | no module in any SDK |
| Wizard | done | unverified | unverified | none | none | ts:wizard.ts py:wizard.py; rs-sdk.md: wizard not ported |
| TUI components | done | partial | partial | none | none | parity.json blocks (status, spinner, anim, help, verbosity, streams) only; ts:tui/ py:tui/; rs tui.rs and php Tui.php are stubs |
| Progress events | done | unverified | unverified | none | none | ts:progress.ts py:progress.py |

## Config, storage and secrets

| Capability | Go | TS | Py | Rust | PHP | Evidence |
|---|---|---|---|---|---|---|
| Layered config | done | unverified | unverified | partial | none | make test-parity-config (Go/Rs, contracts/config-v1); rs:config/ lacks PKL wizard and config command; ts:config.ts py:config.py |
| XDG paths | done | unverified | unverified | partial | none | ts:xdg.ts py:xdg.py rs:xdg/ (home and config home only, rs-sdk.md) |
| Project registry | done | none | none | none | none | no module in any SDK |
| Key-value store | done | none | none | partial | none | make test-parity-kv (Go<->Rs, contracts/kv-v1); rs:kv.rs (SQLite only; etcd/TiDB absent by design) |
| Blob store | done | none | none | partial | none | rs:blob/ (local backend; S3 absent, rs-sdk.md) |
| SQLite connections | done | none | none | unverified | none | rs:sqldb.rs |
| SQLite JSON store | done | unverified | unverified | partial | none | ts:sqlstore.ts py:sqlstore.py rs:sqlstore/ (identity-v1 key derivation covered by rs tests/sqlstore.rs) |
| HTTP cache | done | none | none | done | none | contracts/httpcache-v1 via rs tests/httpcache_contract.rs; rs:httpcache/ |
| Secrets | done | none | none | none | none | no secret module in any SDK |

## Serving and transports

| Capability | Go | TS | Py | Rust | PHP | Evidence |
|---|---|---|---|---|---|---|
| Serve lifecycle | done | done | done | done | done | contracts/parity/serve.json: 13 behaviors SHIPPED in all five ports |
| Service command group | done | none | none | none | none | ports mount `serve` only (serve.json) |
| Command surface bridge | done | partial | partial | partial | partial | MCP leg only: ts:mcp/bridge.ts py:mcp/bridge.py rs:mcp/bridge.rs php:Mcp/Bridge.php |
| Transport service seam | done | none | none | none | none | no module in any SDK |
| REST API toolkit | done | unverified | none | unverified | unverified | ts:api.ts rs:api.rs (guarded HTTP client) php:Api/ApiClient.php; no py module |
| ConnectRPC | done | unverified | none | none | none | ts:rpc.ts gen/ |
| Unix socket | done | none | none | none | none | no module in any SDK |
| MCP server | done | done | done | done | done | make test-parity-mcp (mcp-wire.json, 23 cases + 1 sequence; PHP runs when toolchain present) |
| Served authentication | done | none | none | none | none | no module in any SDK |
| Served middleware | done | unverified | unverified | unverified | unverified | MCP audit/safety only: ts:mcp/audit.ts py:mcp/audit.py rs:mcp/audit.rs php:Mcp/Auditor.php; serve.json mcp_refusal_audit |
| Served observability | done | none | none | none | none | no module in any SDK |
| Bridge payloads | done | none | none | none | none | contracts/bridge.proto and bridge.schema.json only; no SDK module |

## Events, jobs and runtime

| Capability | Go | TS | Py | Rust | PHP | Evidence |
|---|---|---|---|---|---|---|
| Event bus | done | unverified | unverified | partial | none | make test-parity-bus + bus-network (Go<->Rs); rs:bus/ omits SQLite adapter, remote streams, CloudEvents outbound (rs-sdk.md); ts:bus.ts py:bus.py |
| Job queue | done | none | none | none | none | no module in any SDK |
| Notifications | done | none | none | none | none | no module in any SDK |
| Domain building blocks | done | none | none | partial | none | make test-parity-statemachine (Go/Rs); rs:domain/ has state machine only |
| Entity sync | done | none | none | none | none | no module in the five SDKs; engine/sdk has separate ts and py engine clients (make test-parity-engine) |
| Peer mesh | done | none | none | none | none | no module in any SDK |
| Telemetry | done | unverified | unverified | unverified | unverified | ts:telemetry/ py:telemetry/ rs:telemetry/ php:Telemetry/ |

## Guardrails and trust

| Capability | Go | TS | Py | Rust | PHP | Evidence |
|---|---|---|---|---|---|---|
| Path scope | done | done | done | none | none | contracts/parity/scope-defaults.json copied into ts:scope-defaults.json py:scope-defaults.json; TestScopeDefaultsContractSync |
| Breaker | done | none | none | done | none | make test-parity-breaker (Go/Rs); rs:breaker/ |
| Network policy | done | unverified | unverified | done | unverified | make test-parity-netpolicy (Go/Rs); ts:netpolicy.ts py:netpolicy.py php:Net/ |
| Redaction | done | partial | partial | done | partial | make test-parity-redact (Go/Rs); ts:telemetry/redact.ts py:telemetry/redact.py php:Telemetry/Redactor.php cover telemetry only |
| Delegation safety | done | unverified | unverified | none | none | ts:safety.ts py:safety.py |
| Event policy engine | done | none | none | none | none | no module in any SDK |
| Operating mode | done | none | none | none | none | no module in any SDK |
| Consent grants | done | partial | partial | done | partial | make test-parity-consent (Go/Rs, contracts/consent-v1); ts:telemetry/consent.ts py:telemetry/consent.py php:Telemetry/Consent.php are telemetry consent only |
| Side-effect seams | done | none | none | none | none | no module in any SDK |
| Provenance | done | unverified | unverified | none | none | ts:provenance.ts py:provenance.py |
| Audit log | done | none | none | none | none | no module in any SDK |
| Identity | done | none | none | partial | none | contracts/identity-v1 derive-key corpus consumed by rs:sqlstore/crypto.rs only |
| TypeID | done | done | done | done | done | make test-parity-typeid (5 ports, contracts/typeid-v1); ts:id/ py:id/ rs:id/ php:Id/ |
| Upgrade and migrations | done | unverified | unverified | partial | none | make test-parity-upgrade (Go/Rs); rs:upgrade/ is migrations only (self-update not ported); ts:upgrade.ts py:upgrade.py |
| Utilities | done | none | none | partial | none | make test-parity-retry (Go/Rs); rs:util/retry.rs |
| Avatars | done | none | none | none | none | no module in any SDK |

## AI and agents

| Capability | Go | TS | Py | Rust | PHP | Evidence |
|---|---|---|---|---|---|---|
| LLM client | done | partial | partial | partial | none | make test-parity-llm (Go/Rs/Py/TS, contracts/llm-v1); ts:llm.ts py:llm.py rs:llm/ |
| LLM providers | done | partial | partial | partial | none | ts:llm-anthropic.ts llm-openai.ts triton/ py:llm_anthropic.py llm_openai.py rs:llm/anthropic/ (Anthropic only); no Google or Ollama adapter |
| LLM routing | done | unverified | unverified | none | none | ts:router/ routellm.ts py:routellm_adapter.py routellm_grpc.py |
| Toolspec | done | done | unverified | none | none | toolspec-manifest and spec cross-lang fixtures (ts tests: toolspec-manifest.test.ts toolspec-spec.test.ts); py:toolspec.py |
| Command reflection | done | none | none | none | none | no module in any SDK |
| Agent CLI invocation | done | none | none | none | none | no module in any SDK |
| Extensions | done | none | none | none | none | no module in any SDK |
| 12-factor compliance check | done | done | done | none | none | sdk/tests/cross-lang run-compliance.sh + compare_compliance.py; ts:compliance.ts py:compliance.py |

## Conformance and integrations

| Capability | Go | TS | Py | Rust | PHP | Evidence |
|---|---|---|---|---|---|---|
| Conformance assertions | done | none | none | none | none | no module in any SDK |
| Conformance harness | done | none | none | none | none | no module in any SDK |
| Scenario and story DSL | done | none | none | none | none | no module in any SDK; contracts/story-schema.json scenario-rules.json |
| Cassettes and grading client | done | none | none | none | none | no module in any SDK |
| Repo host drivers | done | none | none | none | none | no module in any SDK |
