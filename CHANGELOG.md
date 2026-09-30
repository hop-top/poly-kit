# Changelog

## [0.5.0-alpha.18](https://github.com/hop-top/poly-kit/compare/kit/v0.5.0-alpha.17...kit/v0.5.0-alpha.18) (2026-09-30)


### Features

* **ext:** expose full --ext-info payload from discovery ([45b5f54](https://github.com/hop-top/poly-kit/commit/45b5f54b21cb7bb6a958c96bc98aaf7b7225ef0e))
* **scope:** add Policy.CheckLexical for rules as written ([3ffdfff](https://github.com/hop-top/poly-kit/commit/3ffdfffac7407e92fa0da4e2c166dfc087d8cdc3))
* **scope:** per-op allow/deny entries in scope.yaml ([47a388d](https://github.com/hop-top/poly-kit/commit/47a388d59b89052be94db8c19f4a2dbd7bf3f06b))

## [0.5.0-alpha.17](https://github.com/hop-top/poly-kit/compare/kit/v0.5.0-alpha.16...kit/v0.5.0-alpha.17) (2026-09-29)

The hop-top team is happy to announce Kit 0.5.0-alpha.17. This release includes new features and bug fixes.


### ⚠ BREAKING CHANGES

* **deps:** Go module graph moves `github.com/restatedev/sdk-go` from 0.24 to 1.1 (minimum version selection lifts adopters that import restate directly): restate 1.0 breaking changes apply to their own code (`TerminalError` is a type; testing module split out). `github.com/invopop/jsonschema` 0.14 swaps its ordered-map type and changes reflection for `omitzero` and `json:",string"` fields for adopters reflecting schemas themselves. Migration: follow restate's 1.0 upgrade notes; re-check reflected schemas.
* **engine-ts:** `ts-kit-engine` declares `engines.node >=22.12` (was `>=20`). Migration: run on Node 22.12+ or 24.
* **deps:** RPC telemetry from `observability.RPCInterceptor` follows OTel RPC semconv 1.43, no opt-out. `rpc.server.call.duration` (s) replaces `rpc.server.duration` (ms); `rpc.server.request.size`, `rpc.server.response.size`, `rpc.server.requests_per_rpc`, `rpc.server.responses_per_rpc` removed; `rpc.system` -> `rpc.system.name` (value `connect_rpc` -> `connectrpc`); `rpc.method` fully qualified (`cmdsurface.v1.Commands/Invoke`), `rpc.service` removed; `rpc.response.status_code` replaces the gRPC/Connect code attributes. Migration: move dashboards and alerts to the new names.
* **ts:** `@hop-top/kit` requires Node >=22.12. `commander` 15 and `@clack/prompts` 1.x ship ESM-only; CJS dist loads them via require(esm), unflagged from Node 22.12 (commander itself declares >=22.12). Node 20 EOL 2026-04-30. Older runtimes: `@hop-top/kit`, `./cli`, `./alias`, `./tui` throw ERR_REQUIRE_ESM. Migration: upgrade to Node 22.12+ or 24; TS consumers on `module: node16` without `skipLibCheck` move to `nodenext` or `bundler`.
* **deps:** otelhttp 0.70 emits only stable HTTP semconv. Scraped names change: `http_server_duration_milliseconds` -> `http_server_request_duration_seconds`, `http_server_request_size_bytes_total` / `http_server_response_size_bytes_total` -> `http_server_request_body_size_bytes` / `http_server_response_body_size_bytes` histograms. `OTEL_SEMCONV_STABILITY_OPT_IN` no longer applies. Migration: move dashboards and alerts to the new names.

### Features

* **go:** `Root.IsQuiet()` accessor next to `VerboseCount()` ([0c2526a](https://github.com/hop-top/poly-kit/commit/0c2526a36590b1df37a6d23251cdce09f4481efa))
* **py:** `to_typer_autocompletion` completion bridge ([e03e805](https://github.com/hop-top/poly-kit/commit/e03e805f2cd6a99f2273ea3da99eef7e81a08940))
* **py:** public `is_quiet()` accessor next to `verbose_count()` ([0cd6ec0](https://github.com/hop-top/poly-kit/commit/0cd6ec0f0b11f710cd043cc5e6bb8567fc83b814))
* **release:** ship kit binaries for engine SDK auto-download ([9ff1653](https://github.com/hop-top/poly-kit/commit/9ff1653ff5cd3ff994d4b59e866d9c44ff2f6421))
* **ts:** `isQuiet(cmd)` accessor next to `verboseCount(cmd)` ([faa6776](https://github.com/hop-top/poly-kit/commit/faa6776ef07df6230cf1aeaf2dac6751b4cd2503))


### Bug Fixes

* **engine-py:** extract only the kit binary from release archives ([84b1ad6](https://github.com/hop-top/poly-kit/commit/84b1ad6c507efc9bc0d889f4794099a588958e84))
* **engine-py:** fail closed when kit binary checksums unavailable ([9a3e28a](https://github.com/hop-top/poly-kit/commit/9a3e28a842475593b84b10d90713b6a46e06c6d8))
* **engine-py:** narrow kit version-probe exception handling ([71377b8](https://github.com/hop-top/poly-kit/commit/71377b8a7f3fa1ed3ffb939e292fc0149f8c6295))
* **engine-ts:** extract only the kit binary, without a shell ([2cfe052](https://github.com/hop-top/poly-kit/commit/2cfe0521878443a3f9486c2e64f7d1cca44cf9a2))
* **engine-ts:** fail closed when kit binary checksums unavailable ([dd1a465](https://github.com/hop-top/poly-kit/commit/dd1a4656051d01e3585ef19d19b944cda109e7d5))
* **engine-ts:** run kit install on npm postinstall ([2837a80](https://github.com/hop-top/poly-kit/commit/2837a8033862eefcaa25048a3efe0a1ff11c0bd1))
* **llm:** keep anthropic adapter off SDK credential autoload ([fb77f7e](https://github.com/hop-top/poly-kit/commit/fb77f7ea684f980acd6da61902557617515d74df))
* **py:** build Click objects from the layer typer runs on ([a4d2a20](https://github.com/hop-top/poly-kit/commit/a4d2a208f2986e9dedbc15902540cee72378166b))
* **py:** read root `--quiet` from typer-injected context in spaced ([02e470b](https://github.com/hop-top/poly-kit/commit/02e470bee215afb4c4a1f516cec0fc7fd5680994))
* **scripts:** enforce full node floor in preflight ([7c144f4](https://github.com/hop-top/poly-kit/commit/7c144f42b52fef42cffeea01b60c6409727b70a7))
* **templates:** allow cli-ts dependency builds under pnpm 11 ([c2d9567](https://github.com/hop-top/poly-kit/commit/c2d9567ea590f5c0a357616430c5f1aca1c78c06))
* **templates:** declare `@hop-top/kit` in cli-ts scaffold ([b2efb07](https://github.com/hop-top/poly-kit/commit/b2efb077f55e1327847a0993556938396f8fe53e))
* **templates:** move cli-ts scaffold to commander 15 + node &gt;=22.12 ([8ebe6f3](https://github.com/hop-top/poly-kit/commit/8ebe6f3aa812f90888e5c10ab141b879c9ba86f5))
* **ts:** declare `@types/better-sqlite3` as optional peer ([8f62908](https://github.com/hop-top/poly-kit/commit/8f6290870013c60ad9abe6df6aec816fb90883d1))


### Build

* **deps:** bump Go deps ([d88c618](https://github.com/hop-top/poly-kit/commit/d88c618de630876d63dfcbecf1cc5f8eb71acdd1))
* **deps:** bump Go modules; otel 1.46, otelhttp stable HTTP metrics ([c0a2b21](https://github.com/hop-top/poly-kit/commit/c0a2b215c065cf1716271634957ac60e591b66f5))
* **deps:** bump otelconnect 0.10; RPC metrics on semconv 1.43 ([99a07ed](https://github.com/hop-top/poly-kit/commit/99a07ed61380203f87ba89ebb9f1909336504d48))
* **engine-ts:** require node &gt;=22.12 in ts-kit-engine ([684eaa1](https://github.com/hop-top/poly-kit/commit/684eaa157d5d7b38c66018055d7257e4855fe680))
* **ts:** require node &gt;=22.12; bump commander 15 ([68905f6](https://github.com/hop-top/poly-kit/commit/68905f6f0c664f56994508fdd8f4779f627ae565))

Full diff: [kit/v0.5.0-alpha.16...kit/v0.5.0-alpha.17](https://github.com/hop-top/poly-kit/compare/kit/v0.5.0-alpha.16...kit/v0.5.0-alpha.17)

## [0.5.0-alpha.16](https://github.com/hop-top/poly-kit/compare/kit/v0.5.0-alpha.15...kit/v0.5.0-alpha.16) (2026-09-29)

The hop-top team is happy to announce Kit 0.5.0-alpha.16. This release includes new features and bug fixes.


### ⚠ BREAKING CHANGES

* **serve:** services.<svc>.cors.* and services.all.cors.* were accepted and ignored; they are now applied on the api, mcp and rpc listeners and validated, and cors is a reserved block under every service. A config carrying stale cors keys either takes effect or is refused at exit 2; remove the keys or fix them to the block's shape.
* **api:** api.CORS answers only real preflights; an OPTIONS without Access-Control-Request-Method now reaches the handler instead of a blanket 204. A "*" grant answers Access-Control-Allow-Origin: * rather than echoing the origin, and Access-Control-Allow-Credentials is sent to granted origins only. Routers relying on the blanket OPTIONS answer route OPTIONS themselves.
* **cli:** services.socket.auth.mode was never read; any value but peer (mtls included) is now refused at serve validation with exit 2. Migration: remove services.socket.auth.mode, or set it to peer.
* **serve:** a non-loopback api, rpc or mcp service with no --policy no longer fails validation; it starts and enforces kit-default, so unauthenticated callers can no longer write and destructive commands without a kit/permissions annotation are refused to everyone. Migration: name a --policy with the rules you want, or set services.<svc>.insecure_no_policy: true (APIConfig/rpcserve.Config/mcpserve.Config.InsecureNoPolicy, --insecure-no-policy for api) to keep an unbounded surface; add kit/permissions to destructive commands meant to run remotely under kit-default.
* **policy:** a policy class entry now also governs its expanded tiers: `write: []` refuses write-local and write-shared commands, `destructive: []` refuses destructive-local and destructive-shared ones, on the CLI and on served surfaces. Policies that relied on the old literal match must add explicit entries (e.g. `write-shared: ["*"]`). Discovery for a verified caller now lists commands the caller would be refused as invocable=false; clients treating the listing as caller-independent should re-read it per credential.
* **cli:** a tool with WithAPI and WithIdentity now gets the kit-reserved top-level token command even without APIConfig.Auth; rename an adopter command named token.
* **cmdsurface:** callers on a transport-established surface (owner-only socket, stdio, cron, IAM-signed Lambda) share one idempotency scope and one rate-limit bucket per transport whatever Caller they claim. To separate them, install a verifier (SocketConfig.Auth) so the caller is established by it.
* **cli:** a kit-shipped service now refuses a call that finds every in-flight slot taken and 64 calls already queued, with 503 overloaded and Retry-After (Connect Unavailable, socket OVERLOADED, MCP isError). Without cli.WithRootFactory one call runs at a time, so more than 65 concurrent callers see refusals where they used to wait. To keep the previous unbounded wait set services.all.concurrency.enabled: false, or raise services.<svc>.concurrency.max_queue (and max_inflight with a root factory).
* **cmdsurface:** a command annotated kit/permissions is now refused on served surfaces (403 insufficient_scope) unless the caller's verified credential carries every listed scope; previously the default PermitAll ran it. Migrate by returning the scopes from the verifier (api.Claims.Scopes, a claims map scopes entry, socket.Identity.Scopes), or drop the annotation from commands that should stay open. A cli.WithPermission gate that re-implemented the check can be removed; it now runs after the built-in check and can only narrow.
* **cli:** serve exits 2 on services.socket.timeouts.{read_header,read,write,idle}, services.socket.auth.mode: mtls, and services.<svc>.cache for any svc but api, all previously accepted and ignored. Migration: delete those keys, or move shared values under services.all.
* **cmdsurface:** a served call repeating an Idempotency-Key its caller already completed now answers with the recorded result instead of running again, and a key reused for a different command or flags is refused 422 idempotency_key_reused. Send a fresh key per logical operation, or set services.<svc>.idempotency.enabled: false to run every call.
* **api:** the 401 body code changes from "unauthorized" to "unauthenticated" for api.Auth (every api, mcp HTTP and projection route), webhook signature refusals, and the kit serve engine protocol. Clients branching on "unauthorized" must match "unauthenticated" (or the 401 status).
* **transport:** a leaf declaring kit/auth-required no longer runs on a bare Authorization header, a claimed meta.caller, or a loopback api listener without Auth. Migrate: set APIConfig.Auth / rpcserve.Config.Auth / mcpserve.Config.Auth; bare mounts pass WithRESTAuth, WithSSEAuth, WithWSAuth, WithBusAuth, an api.Auth on the router (MountMCP), WithRPCAuthenticated or an established WithRPCCallMeta (MountRPC), WithCallMeta/WithAuthenticated (mcpsdk); Lambda API Gateway mappings need a gateway authorizer.
* **cli:** the rpc and mcp listeners now check Host and Origin like the api service. A proxy that forwards another Host to a loopback bind needs services.<svc>.host_check.allow; a cross-origin browser client needs services.<svc>.origin_check.allow. HTTP-plane blocks (security_headers, health, host_check, origin_check, body_limit, compression, metrics.scrape) set under services.socket are refused at exit 2: remove them or move them under services.all.
* **cli:** a kit-shipped service listening beyond loopback, and any service built from cli.ServeBridgeOptions, now refuses a caller over its per-tier rate with 429 rate_limited (ResourceExhausted, RATE_LIMITED). To keep the previous unlimited behavior set services.all.rate_limit.enabled: false, or raise services.<svc>.rate_limit.<tier>.per_minute and burst; a loopback service calls cli.ServeBridgeOptionsFor(r, svc, true).
* **serve:** tls, tls.acme, auth and auth.mtls are registered middleware blocks under every services.<svc> and services.all; an unknown key inside them, an adopter service's own included, is refused at exit 2. Migration: move such keys out of the tls and auth blocks.
* **cli:** serve refuses at exit 2 anything under services.all other than a registered middleware block (lifecycle keys, addr, path, insecure opt-ins, unknown blocks), and an unknown key inside a registered block of any service, not only api. Migration: move service-owned keys from services.all to services.<name>; fix or drop misspelled block keys.
* **cli:** with APIConfig.Auth set, the OpenAPI document, /docs, /schemas, huma operations registered through APIConfig.Resources, and unmatched paths answer 401 without credentials (previously 200 or 404). An oversized body to an unmatched path is 413, not 404. To keep the spec public, return (nil, nil) from the AuthFunc for GET /openapi.json (and /docs, /schemas/ if wanted); every other route still requires a credential.
* **cli:** the api service now answers 403 host_rejected to a Host its listener does not answer for (a loopback bind accepts localhost, 127.0.0.1, [::1]; a named or IP bind accepts that host; a wildcard bind checks nothing by default) and 403 origin_rejected to a POST/PUT/PATCH/DELETE from another browser origin. Opt out or widen with services.api.host_check.allow / .enabled: false and services.api.origin_check.allow / .enabled: false.
* **cmdsurface:** cli.ServeBridgeOptions(r) and cli.ValidateServeBridge(r) are now ServeBridgeOptions(r, svc) and ValidateServeBridge(r, svc); pass the service's registered name.
* **transport:** REST, projection (MountProjection included), MountMCP, MountRPC and RPCResource bodies over 1 MiB were accepted and are now refused (413 or ResourceExhausted); mcpsdk drops from the SDK's 4 MiB to 1 MiB. Raise per entry point with the new options or services.<svc>.body_limit.max_bytes; a negative Go option or body_limit.enabled: false removes the cap.
* **templates:** `kit init --update` moves a scaffolded project's mise.toml pins: go 1.26 -> 1.26.1 (exact), pnpm 9 -> 11, uv 0.5 -> 0.12, golangci-lint 2.12 -> 2.11.4, ruff 0.8 -> 0.15.11, lychee 0.18 -> 0.24.2, npm:release-please 16 -> 17; new pins buf 1.73.0 and npm:markdownlint-cli2 0.23.3. Migration: run `kit init --update`, then `mise install`, then re-run the project's lint and lockfile checks under the new tools; lychee 0.24 rejects a `[config]` table and a boolean `verbose` in lychee.toml (use top-level keys and a level string such as "info").
* **init:** kit init --update rewrites docker-compose service volumes from named volumes to ./.data/<service> bind mounts. Existing dev data stays in the old named volume; copy it over before restarting (e.g. docker run --rm -v <project>_pgdata:/from -v $PWD/.data/postgres:/to alpine cp -a /from/. /to/) or keep the old volume by editing the generated compose file.
* **mcpsdk:** a 2026-07-28 client (SDK clients probe server/discover first) is now served per request without a session instead of being downgraded to a 2025-11-25 session. Handlers registered via WithServerConfigurator that issue server-to-client requests (sampling, roots, session elicitation) get no channel for such a client; return InputRequests (multi round-trip) instead, as over stdio. For session-only serving, wrap Surface.Server() in mcp.NewStreamableHTTPHandler directly.
* **cli:** a socket service with a non-empty SocketConfig.Expose now refuses (NOT_ENABLED) every command outside those patterns, as documented; before, Expose was ignored and the whole tree was reachable. Add the patterns the socket must reach to Expose, or leave it empty for the whole tree.
* **cmdsurface:** a PermissionFunc refusal on MountWebhooks, MountOAuth or a Lambda API Gateway handler now answers 403 permission_denied (was 500 internal_error), and a binding to an interactive or self-hosting leaf answers 500 not_invocable (was 500 internal_error). Bus error envelopes carry code permission_denied / not_invocable where they carried internal.
* **cmdsurface:** MountREST answers a PermissionFunc refusal with 403 permission_denied and a call to an interactive or self-hosting leaf with 404 not_invocable, where both answered 500 internal_error. Clients that retried or alerted on those 500s should switch on the code instead.
* **cmdsurface:** served argv gains "--" before positional args. Commands reading cmd.ArgsLenAtDash() see 0, and a subprocess's os.Args carries the "--", for every served invocation with args. SubprocessRunner argv form is now <bin> <path...> [--flag=v...] [-- args...]; callers driving a non-kit binary with options in Args move them into Path or Flags. InvokeArgs single-dash tokens ("-v") are positional, no longer cobra shorthand flags; use the long form. Commands that parse their own argv (DisableFlagParsing) unaffected.
* **cmdsurface:** streaming calls over MountSSE, MountWS, RPC InvokeStream and StreamArgs now pass the permission and invocability gates. A call the PermissionFunc refuses, or one to an interactive or self-hosting leaf, is refused instead of streamed: SSE 403 permission_denied / 404 not_invocable, WS error frame permission_denied / not_invocable, RPC PermissionDenied / NotFound, StreamArgs ErrPermissionDenied / ErrNotInvocable. RPC Invoke and InvokeStream answer PermissionDenied / NotFound where they answered Internal. Remote streams are now audited once per refusal or run.
* **cmdsurface:** application/proto on /cmdsurface.v1.Commands/* now means binary protobuf; JSON callers send application/json. Result.data numbers that do not survive a double round trip arrive as strings (exact digits also in data_json). Events and results gain typed fields (result, data_json).
* **cmdsurface:** the socket service no longer invokes as `rpc`. A `SocketConfig.Policy` whose `AllowDestructiveOn` names `SurfaceRPC` no longer permits destructive commands over the socket; name `cmdsurface.SurfaceSocket` instead. Audit records, sink surface filters, and refusal messages for socket calls carry `socket`; update any `surfaces: [rpc]` sink filter meant for the socket.

### Features

* **api:** /healthz and /readyz on the api service ([f1e6247](https://github.com/hop-top/poly-kit/commit/f1e6247fcf84016c71cfec7a70e5ab47292f2692))
* **api:** add `HostCheck`, `OriginCheck` and `SecurityHeaders` middleware ([0bef225](https://github.com/hop-top/poly-kit/commit/0bef225d418d776fc30e92071fbc40c23245a3b1))
* **api:** body limit refusal in the listener's protocol ([52f52e4](https://github.com/hop-top/poly-kit/commit/52f52e47457b3da58453a19f20a691d90d340ea6))
* **api:** declare result cache revalidation in OpenAPI specs ([8d66c90](https://github.com/hop-top/poly-kit/commit/8d66c90c7459f2b4ec2a7a3f1c954f5f6f8ece91))
* **api:** ETag, 304 and Cache-Control on cached projected reads ([04fd10b](https://github.com/hop-top/poly-kit/commit/04fd10b8c885148cc09d549b5431e562f027c3f4))
* **api:** gzip/zstd response compression middleware ([e815f94](https://github.com/hop-top/poly-kit/commit/e815f94897d56fc758d0e6eb51503d05099cebb5))
* **api:** mark declared vs guessed side-effect in discovery and OpenAPI ([747c20b](https://github.com/hop-top/poly-kit/commit/747c20b703b350598ba4bf3c024033052d04c0dd))
* **api:** record HTTP-plane refusals for metrics ([c6c1038](https://github.com/hop-top/poly-kit/commit/c6c10385d347a51bf91fbdf23801b45efd2ed62a))
* **api:** resolve the client address behind trusted proxies ([9957b09](https://github.com/hop-top/poly-kit/commit/9957b091666ec88e84c75c2c9dd44d26f973dc24))
* **api:** router-wide middleware via WithOuterMiddleware ([cde1a1e](https://github.com/hop-top/poly-kit/commit/cde1a1ee2a085da4fbf91b44b1955678af801818))
* **api:** stream projected commands over SSE ([4f03760](https://github.com/hop-top/poly-kit/commit/4f03760fb4a142d16db27018a57c83454429fa08))
* **cli/policy:** carry a permissions: block in the policy file ([146c700](https://github.com/hop-top/poly-kit/commit/146c70096ef033019a92d94cfe87f675cd323f3c))
* **cli:** API keys with scopes, tenant, expiry and revocation ([ac37879](https://github.com/hop-top/poly-kit/commit/ac37879dc2e1180e081ba71cf07280653894d651))
* **cli:** audit chains from services.&lt;svc&gt;.audit.sinks and audit verify ([72fa80a](https://github.com/hop-top/poly-kit/commit/72fa80a7c0adcff63334465ee9bb19d2207390fe))
* **cli:** built-in mcp service over streamable HTTP ([46fa1bd](https://github.com/hop-top/poly-kit/commit/46fa1bda3435c0b61a9c552f56e48bee475aa37c))
* **cli:** check Host and Origin, set security headers on the api service ([2944f56](https://github.com/hop-top/poly-kit/commit/2944f5672edb11ca4b937afd620f3bbad5453b33))
* **cli:** compression block for the api service ([fb47c67](https://github.com/hop-top/poly-kit/commit/fb47c6738da1657c674b95abdc6a4bc255e17114))
* **cli:** concurrency block, on by default for every service ([3b6fd62](https://github.com/hop-top/poly-kit/commit/3b6fd62c24aa684dae60c6e0faa32480ff2a5fcb))
* **cli:** evaluate --policy permissions: rules in the served permission gate ([5bab095](https://github.com/hop-top/poly-kit/commit/5bab0954c0b4237d59e32ae2a306904bdc366be6))
* **cli:** HTTP-plane middleware on the mcp and rpc listeners ([dc173fd](https://github.com/hop-top/poly-kit/commit/dc173fda6f06066ac0d0aa0dac0248a0c8c6ea91))
* **cli:** point side-effect validation failures at spec coverage ([55e376f](https://github.com/hop-top/poly-kit/commit/55e376f5599a3b3e268a75e9be0dc0509f14299b))
* **cli:** rate_limit block, on by default beyond loopback ([fa82a71](https://github.com/hop-top/poly-kit/commit/fa82a7161984348d5ec8d1c9a8c39a95f596d4b8))
* **cli:** serve the mcp service over stdio ([3319c74](https://github.com/hop-top/poly-kit/commit/3319c7479738e7180cdf62681381b5372eccd197))
* **cli:** services.&lt;svc&gt;.trusted_proxies on every kit HTTP listener ([4648c17](https://github.com/hop-top/poly-kit/commit/4648c17be122d94caf315e663e46b1b8042d93df))
* **cli:** services.api.cache block and kit/cache-ttl validation ([ab71b54](https://github.com/hop-top/poly-kit/commit/ab71b546ec9ff82799fd0c0409234d560471a8ec))
* **cli:** services.socket.auth.mode peer ([2da67c4](https://github.com/hop-top/poly-kit/commit/2da67c46929e5ac87a0d75e0cb095ccda9461536))
* **cli:** shared resolver for services middleware config ([f3b5f6c](https://github.com/hop-top/poly-kit/commit/f3b5f6c82139f728674a452117390bb646911c3e))
* **cli:** stream served commands on the api service ([9bc8457](https://github.com/hop-top/poly-kit/commit/9bc84571286ba73c3a909b1f6470955394659540))
* **cli:** token create signs, token verify checks ([b3593f3](https://github.com/hop-top/poly-kit/commit/b3593f35b8286f7c955c63d0842860d297ea3f36))
* **cli:** WithServeRateLimit code defaults for rate_limit ([a6389b6](https://github.com/hop-top/poly-kit/commit/a6389b68e78d813060eed7bd9acb3884f2bf7aeb))
* **cli:** WithTokenCheck revocation hook for config-chosen verifiers ([588ed0a](https://github.com/hop-top/poly-kit/commit/588ed0a427ccc930f7d64b39dae7c8710a802b72))
* **cmdreflect:** reflect undeclared positional args; carry args in toolspec ([7a139e5](https://github.com/hop-top/poly-kit/commit/7a139e5823ea3d4c0ad1ecb14976eba1615e8c53))
* **cmdsurface:** admit invocation before streaming it ([893ebce](https://github.com/hop-top/poly-kit/commit/893ebcecad92b4983bba081ef583a1aaa1702299))
* **cmdsurface:** bridge-level command projection mount ([2204cf4](https://github.com/hop-top/poly-kit/commit/2204cf4a57adfa0a8e71509466eda7b577ce651d))
* **cmdsurface:** capacity gate on the invocation plane ([3d0c960](https://github.com/hop-top/poly-kit/commit/3d0c9601e96e5126b97fb840cab586972cabbfce))
* **cmdsurface:** ChainSink appends audits to a tamper-evident log ([3c0a1ed](https://github.com/hop-top/poly-kit/commit/3c0a1ed85db389adc5731d021377ca43b1ae612d))
* **cmdsurface:** Cloud Run projection switch and mount seam ([2348053](https://github.com/hop-top/poly-kit/commit/2348053c08b5c764d40a5fa57d7e8dbf55018aa0))
* **cmdsurface:** configurable audit field scan limit ([09f5694](https://github.com/hop-top/poly-kit/commit/09f56941b219b03a8226bd14174061fdb2e65a19))
* **cmdsurface:** deprecate `MountREST` and `MountMCP` ([f804e14](https://github.com/hop-top/poly-kit/commit/f804e14372463fb6ad89e35b2751e289cfe86307))
* **cmdsurface:** enforce kit/permissions scopes in the permission gate ([dab3b29](https://github.com/hop-top/poly-kit/commit/dab3b2909a45452e8b29296a4e2e81a30fb99652))
* **cmdsurface:** MountRPC call-meta, auth and handler options ([25cbaa4](https://github.com/hop-top/poly-kit/commit/25cbaa415045abd97d13a9f3f8efdd994b7805ab))
* **cmdsurface:** MountRPC on generated cmdsurface.v1.Commands handler ([59f6622](https://github.com/hop-top/poly-kit/commit/59f6622214d70794dcb43651be25a22b6ceafa61))
* **cmdsurface:** opt-in RPC response compression ([f1bd23b](https://github.com/hop-top/poly-kit/commit/f1bd23bc068dc44d2917fc72fb1522153b6dd996))
* **cmdsurface:** publish cmdsurface.v1.Commands proto + Go stubs ([bc1cb7b](https://github.com/hop-top/poly-kit/commit/bc1cb7b896fab4c1156a5488ba39a1211aeaf8f3))
* **cmdsurface:** rate-limit gate on the invocation plane ([f1193c5](https://github.com/hop-top/poly-kit/commit/f1193c53b41ef01a180f93a406b832f3cee21fe6))
* **cmdsurface:** read-tier result cache at invocation slot 8 ([bf8969a](https://github.com/hop-top/poly-kit/commit/bf8969ae9094e643f55f319f1fc62be46c2560b5))
* **cmdsurface:** replay Idempotency-Key on the served path ([8f5fbe8](https://github.com/hop-top/poly-kit/commit/8f5fbe8d3c5bd6fcc4bf5dd9b56349622d7be3ba))
* **cmdsurface:** show the permission gate the invocation and verified scopes ([381d4ba](https://github.com/hop-top/poly-kit/commit/381d4bac93acd2c0016b917934f68d955d0d99eb))
* **cmdsurface:** translate the declarative sinks: block ([1aece8a](https://github.com/hop-top/poly-kit/commit/1aece8aa0deec0ff21bf5e2474521ecf1f6c1a24))
* **cmdsurface:** translate webhook, bus and cron config blocks into mount inputs ([4119ba6](https://github.com/hop-top/poly-kit/commit/4119ba6c4f255df254f55c6e5bb1ae0b4ac37978))
* **examples:** long-running item watch in served fixture ([0f92f64](https://github.com/hop-top/poly-kit/commit/0f92f64001ebc052501381c263cddcc7b8f63971))
* **kv:** in-memory driver with expiry and LRU eviction ([30df34b](https://github.com/hop-top/poly-kit/commit/30df34b96bfcf2f80bc1ba91d11c4a1707b3261d))
* **mcp:** pass declared positional args as an args array ([f7c9707](https://github.com/hop-top/poly-kit/commit/f7c97072ac72d1cea24472aef9b5dea480469ff6))
* **mcpsdk:** 403 insufficient_scope step-up challenge over HTTP ([e8cb81d](https://github.com/hop-top/poly-kit/commit/e8cb81d26d7469e5149b0cafeb17d1b5663f152e))
* **mcpsdk:** add `WithOriginAllowlist` Origin validation ([c614d70](https://github.com/hop-top/poly-kit/commit/c614d7065c8c88d1fdd6c5714f8b9266c27c99d9))
* **mcpsdk:** export JSON-RPC refusal writer, opt out of SDK rebinding check ([659b7ca](https://github.com/hop-top/poly-kit/commit/659b7ca47880f0ddd4cae220ddd6c7ef26575ff6))
* **mcpsdk:** host-supplied call meta, auth verdict, elicited confirmation ([d9f3785](https://github.com/hop-top/poly-kit/commit/d9f3785d724bd1a9e1e7aed9d50030c14a399d81))
* **mcpsdk:** serve 2026-07-28 statelessly beside sessions on one endpoint ([c99bf85](https://github.com/hop-top/poly-kit/commit/c99bf8501784d533457d5e8cf8be609abe3ecff2))
* **mcpserve:** answer 2026-07-28 over HTTP beside sessions ([9ce0003](https://github.com/hop-top/poly-kit/commit/9ce0003d3f404dcf7835499a7b0a47eb1b8f514d))
* **observability:** OpenTelemetry spans and metrics for served commands ([3d1366c](https://github.com/hop-top/poly-kit/commit/3d1366cb9dab05a5dc6497f0bf8e077cf900443a))
* **observability:** Prometheus scrape endpoint at HTTP slot 7 ([d9d7bae](https://github.com/hop-top/poly-kit/commit/d9d7bae642c56369828c268f32bc9e89842961d8))
* **policy:** per-caller delegation policy, persisted budgets, per-caller discovery ([9a88e4e](https://github.com/hop-top/poly-kit/commit/9a88e4e64ff0569bf2bac576988d4c718a793b66))
* **rpc:** Authenticate interceptor for unary and streaming calls ([df0af01](https://github.com/hop-top/poly-kit/commit/df0af01253a84bc939501099f030bf369c64df37))
* **rpc:** serve h2c alongside HTTP/1.1 in ListenAndServe ([2c9fa88](https://github.com/hop-top/poly-kit/commit/2c9fa88de9dca5cd27055b246b1438d413da1206))
* **rpcserve:** adopter interceptors inside kit's gates ([495f3a4](https://github.com/hop-top/poly-kit/commit/495f3a4496f872c6e9aac5d3f9067717bd826985))
* **rpcserve:** built-in rpc service on the transport seam ([31d0312](https://github.com/hop-top/poly-kit/commit/31d0312ccd224352d8d1fc44d2a0ba7908ef1a43))
* **rpcserve:** serve RFC 9728 protected resource metadata ([809bd86](https://github.com/hop-top/poly-kit/commit/809bd86df93ff1799b81703fdc0af1797209be63))
* **security:** hash-chained audit log with verifier ([726c1f1](https://github.com/hop-top/poly-kit/commit/726c1f11fa141fc4c613b92855c0b77b7fe2fa2e))
* **serve:** check client certificates against auth.mtls.crl_file ([6fcf30a](https://github.com/hop-top/poly-kit/commit/6fcf30aea7b8066b86bd59ce9bc3a2a6573340c0))
* **serve:** configurable server timeouts and per-command deadlines ([cf8ff3b](https://github.com/hop-top/poly-kit/commit/cf8ff3b4851d96436f35ae1c8b95ba9a3d1de4c7))
* **serve:** cors block at HTTP slot 9 ([c031de0](https://github.com/hop-top/poly-kit/commit/c031de07ff413be79c1af9ccc154a02638dbc717))
* **serve:** expose supervisor run record to services as RunView ([35957ad](https://github.com/hop-top/poly-kit/commit/35957adc826926212af648d3eb8bdfcbdb002e99))
* **serve:** log and count failed TLS handshakes ([d675247](https://github.com/hop-top/poly-kit/commit/d6752473298ebcc7c9250058b5068c8ea11a8ec8))
* **serve:** persisted per-caller quotas over time windows ([de057dc](https://github.com/hop-top/poly-kit/commit/de057dc56c8185ff118c92eee3c09bd36619f583))
* **serve:** reload TLS certificate and CA files on change ([1f38762](https://github.com/hop-top/poly-kit/commit/1f38762fb81eb9e9a50ec90004c23b4845d2fe83))
* **serve:** ship kit-default policy, the remote default without --policy ([c6a0b24](https://github.com/hop-top/poly-kit/commit/c6a0b240df133a6b3c22024ea4f5a90956d137e2))
* **serve:** TLS and mTLS client-certificate auth on api, rpc and mcp listeners ([dc36ba1](https://github.com/hop-top/poly-kit/commit/dc36ba1aa384ed05a9d9f8a2371065d7901c5705))
* **socket:** peer-credential authenticator ([7390af2](https://github.com/hop-top/poly-kit/commit/7390af23308f8a9f896d5f9b54f4fb684e737cd6))
* **socket:** scope source for peer-authenticated callers ([f02511f](https://github.com/hop-top/poly-kit/commit/f02511f1a0d1d640dcbc9aaf75aff8f40caa5ea6))
* **templates:** cli-go scaffold sets served middleware defaults ([52cf3ab](https://github.com/hop-top/poly-kit/commit/52cf3ab865c938c040d30f64a7dc5f31d3634089))
* **templates:** mount spec and spec coverage in cli-go root ([e3e8f89](https://github.com/hop-top/poly-kit/commit/e3e8f89f995cac0d2ddb97c6a08f537ea963b0b9))
* **templates:** register the mcp service in the cli-go scaffold ([41d5986](https://github.com/hop-top/poly-kit/commit/41d59861885f2b43e191eb17f6a92c075dccfcd6))
* **templates:** register the rpc service in the cli-go scaffold ([bb59737](https://github.com/hop-top/poly-kit/commit/bb5973793f829816fd64302282db7e8a59796828))
* **toolspec:** negotiate &lt;tool&gt; spec schema version from KIT_TOOLSPEC_SCHEMA ([e1bdd60](https://github.com/hop-top/poly-kit/commit/e1bdd60dd7146cdca06fb077fdd1b60a34054136))
* **transport:** built-in bearer verifiers: jwt, jwks, oidc ([32638da](https://github.com/hop-top/poly-kit/commit/32638da9951835f8e29a4dd781132c7aa08d864f))
* **transport:** cap request bodies on every HTTP entry point ([7170c70](https://github.com/hop-top/poly-kit/commit/7170c701dd6e2acb454ffdce7a64836e8a89b991))
* **transport:** carry W3C trace context to runners and child processes ([2d0176f](https://github.com/hop-top/poly-kit/commit/2d0176f2a93cf124bc4a35d97255b1c16b3153ff))
* **transport:** MCP authorization flow at the HTTP edge ([48dd9af](https://github.com/hop-top/poly-kit/commit/48dd9af275ce76797fda44aed4fef275e384a476))


### Bug Fixes

* **api:** api.Auth refuses as unauthenticated with a challenge, counted ([495bd55](https://github.com/hop-top/poly-kit/commit/495bd55790992ce88a5660a97a7e6afe01e13dfe))
* **api:** CORS follows the Fetch standard ([a9e0511](https://github.com/hop-top/poly-kit/commit/a9e0511470596d396f4362ea9a469c5d0fa4e1e3))
* **api:** name protected resource metadata in REST 403 insufficient_scope ([e9bee6c](https://github.com/hop-top/poly-kit/commit/e9bee6c94078e14185aea7bdd06a947123b526d7))
* **api:** pass flush and deadline control through logger writer ([cc9abb9](https://github.com/hop-top/poly-kit/commit/cc9abb95e0d56eabb2070394b2e497d3e499ff0e))
* **changelog:** keep commit and PR links on rewritten bullets ([af5ed1e](https://github.com/hop-top/poly-kit/commit/af5ed1efe198b42894b220972a5522a9ec104c7f))
* **cli:** body limit, compression and auth cover every api route ([5e407be](https://github.com/hop-top/poly-kit/commit/5e407bed3afd4a4e2f0b0b4b60ae79d92fcb2185))
* **cli:** charge max_ops budget only once slot 6 admits ([9434450](https://github.com/hop-top/poly-kit/commit/94344505e1ec4b035008035211cfe74d3c8b468c))
* **cli:** close the result-cache store when api start fails ([7612ff5](https://github.com/hop-top/poly-kit/commit/7612ff59dfe4ecaa6923ff06a5e9c018c05a5c58))
* **cli:** create API key store owner-only ([c3bc850](https://github.com/hop-top/poly-kit/commit/c3bc850a121f6c5aa976d22cb450bbe8d88392be))
* **cli:** idempotency capture no longer pins the leaf's writer ([c1a87d1](https://github.com/hop-top/poly-kit/commit/c1a87d1bb73e5fb15750749735ee3500ff03b9ca))
* **cli:** metrics scrape endpoint behind the Host and Origin checks ([373a46e](https://github.com/hop-top/poly-kit/commit/373a46e6d8d0611e6ba2a5c427ac5bafb51654c2))
* **cli:** name the missing jwks url or oidc issuer before the audience ([89fea12](https://github.com/hop-top/poly-kit/commit/89fea12bb21a5aae0a15ea389f5edd2d91b930fa))
* **cli:** narrow the socket service to SocketConfig.Expose ([cd521b8](https://github.com/hop-top/poly-kit/commit/cd521b83575b1c819fe8dbce08651d495aa6ad62))
* **cli:** quota and caller policy name a transport-vouched caller by nothing it claims ([b322dc0](https://github.com/hop-top/poly-kit/commit/b322dc03f1ab4d47489c51500d79ca7129bd08d5))
* **cli:** refuse bearer auth modes under services.socket ([8d08a68](https://github.com/hop-top/poly-kit/commit/8d08a68a85603abee4833931ba73798ed4adefea))
* **cli:** refuse settings no service applies ([e379352](https://github.com/hop-top/poly-kit/commit/e37935280ddb39094896956f1309c63dca904eb4))
* **cli:** refuse tls, tls.acme and auth.mtls under services.socket ([c578392](https://github.com/hop-top/poly-kit/commit/c578392243f38ddb35e308c221200a62a1059e1b))
* **cli:** report empty kit/side-effect as missing in Root.Validate ([2d26586](https://github.com/hop-top/poly-kit/commit/2d265869690c9af49d34ee341ee2908de06e56b9))
* **cli:** scope served --idempotency-key replay to the caller ([b5ed2fc](https://github.com/hop-top/poly-kit/commit/b5ed2fc16610604ec9b45d0cf1a43ca53dcf0df2))
* **cli:** served idempotency store purges past the longest service ttl ([6e19a47](https://github.com/hop-top/poly-kit/commit/6e19a47b85505e49cc3d27094ef48cca5ba86b8b))
* **cli:** services.* keys from env, -c and config files reach serve ([37334b6](https://github.com/hop-top/poly-kit/commit/37334b6509b5b1c92e6e7bc3c3c7553a42504b91))
* **cli:** stop pre-parse flag scans at "--" ([917a833](https://github.com/hop-top/poly-kit/commit/917a833d3d3f5d758a87810e30c5ffb824139a04))
* **cli:** tell a malformed kit/side-effect tag from a missing one on --dry-run ([deef800](https://github.com/hop-top/poly-kit/commit/deef8007b291cbb0c697876fa1e69eb9d213f42c))
* **cli:** warn on runnable command groups without a side-effect tier ([247aeff](https://github.com/hop-top/poly-kit/commit/247aeffc127817f14a3eb4ac527f5b3e7377ed9c))
* **cmdsurface:** add `SurfaceSocket`; socket service stops pinning rpc ([072228a](https://github.com/hop-top/poly-kit/commit/072228acc9c2135d0d7964682f3fd9d49fe045bc))
* **cmdsurface:** admit streamed invocations through the bridge gates ([05e546c](https://github.com/hop-top/poly-kit/commit/05e546c21318eff67045bdb8b73abcb7724c2dfc))
* **cmdsurface:** an idempotency replay takes no capacity slot ([0091412](https://github.com/hop-top/poly-kit/commit/009141288e8b7c16d5617871353f948ee3a55eaa))
* **cmdsurface:** end options before served positional args ([26e7994](https://github.com/hop-top/poly-kit/commit/26e79940d7b6ef9b5f3a465043e7be031fed7438))
* **cmdsurface:** idempotency scope follows the established identity ([62d71e7](https://github.com/hop-top/poly-kit/commit/62d71e762c5cba376c86a43b6d3ef14afbee003a))
* **cmdsurface:** label the D2 initialize wire fixture legacy ([cdf6a51](https://github.com/hop-top/poly-kit/commit/cdf6a517202363e36c1a65e454e1698be0418748))
* **cmdsurface:** map permission and invocability refusals on MountREST ([b5b0bfe](https://github.com/hop-top/poly-kit/commit/b5b0bfe69a34f8d790164b9f5cb4da4dbdac3050))
* **cmdsurface:** MountMCP carries the idempotency key and marks replays ([fe22e34](https://github.com/hop-top/poly-kit/commit/fe22e3474684ca45e4c32f7df73f1aaba373018c))
* **cmdsurface:** name permission and invocability refusals on passthrough surfaces ([9bb0d20](https://github.com/hop-top/poly-kit/commit/9bb0d20ac27d28d4513f5dd71a5966f132fdf797))
* **cmdsurface:** one idempotency scope; transport-vouched callers are the owner ([fa54ef2](https://github.com/hop-top/poly-kit/commit/fa54ef2131d9a958554461c7226314847260b647))
* **cmdsurface:** per-command deadline bounds a result-cache miss ([fc7ea26](https://github.com/hop-top/poly-kit/commit/fc7ea2604f2bed56dab42236b71b5c4cf9206215))
* **cmdsurface:** rate-limit and result-cache keys trust only an established identity ([82c429d](https://github.com/hop-top/poly-kit/commit/82c429d627e36992c7e382d57493a1b4c912a69e))
* **cmdsurface:** redact audit records before sink fan-out ([12b2b17](https://github.com/hop-top/poly-kit/commit/12b2b170539f4cbed1aa41e44accc5a0f80248f9))
* **cmdsurface:** Retry-After on Lambda API Gateway 429 ([2296ec9](https://github.com/hop-top/poly-kit/commit/2296ec98413be3b51a84c99f238ba64542bd8e47))
* **cmdsurface:** scope result-cache key as idempotency scopes it ([5b45f4b](https://github.com/hop-top/poly-kit/commit/5b45f4b08e7244b7d1cd9bd22545b420332ed052))
* **examples:** make spaced lychee.toml loadable by lychee 0.24 ([04113ed](https://github.com/hop-top/poly-kit/commit/04113ed4c81f484e9110b727b31c517d849dc7cf))
* **examples:** spaced ts .nvmrc on node 22 ([67a38f3](https://github.com/hop-top/poly-kit/commit/67a38f377b9387b80e10d7281298ca5846b76087))
* **idemstore:** purge expired sqlite rows on open and periodically ([9943a67](https://github.com/hop-top/poly-kit/commit/9943a6778962388b8a38ab28f2dfe9062145c0c8))
* **init:** resync kit init managed assets with templates/shared ([24f77d1](https://github.com/hop-top/poly-kit/commit/24f77d14da38363c22619f83440c88c1399628a4))
* **mcp:** answer stdio requests read before end of input ([398f93f](https://github.com/hop-top/poly-kit/commit/398f93f590312175e669523fccf6f2169a375697))
* **mcpsdk:** a task-augmented call carries no idempotency key ([8817461](https://github.com/hop-top/poly-kit/commit/8817461c10c277b80f9978fc6bc87e5ea8dcf188))
* **mcpsdk:** admit a task once and run that admission ([91394e2](https://github.com/hop-top/poly-kit/commit/91394e2936318f1791d6018650becc062efc2ea9))
* **mcpsdk:** ask for confirmation only after the machine gates ([ce7c37b](https://github.com/hop-top/poly-kit/commit/ce7c37bc06d2104c5c3647a42b4c998fafdfdcdb))
* **mcpsdk:** run progress-streamed calls through the bridge gates ([c340c97](https://github.com/hop-top/poly-kit/commit/c340c97a616f9396d91f3f410ca0fb0e00b1e793))
* **mcpserve:** end a stalled request body read on stop ([d216052](https://github.com/hop-top/poly-kit/commit/d216052e8fc4e113c448714555599d95b9a09534))
* **mcpserve:** withhold management-only commands from the tool list ([711c7ef](https://github.com/hop-top/poly-kit/commit/711c7eff8a77e85df449c9f1da994aa77ffbd6b8))
* **observability:** record the resolved client address on HTTP spans ([e562643](https://github.com/hop-top/poly-kit/commit/e562643a1be051c5fd2b473f510db042fc426824))
* **observability:** second-scale buckets for the request duration histogram ([805520e](https://github.com/hop-top/poly-kit/commit/805520e3b969f0af599ea709d05d276f0b07b548))
* **rpc:** map ResourceExhausted to 429 and rate_limited with Retry-After ([9ebe849](https://github.com/hop-top/poly-kit/commit/9ebe84943cadc7e15d23cf1b0b7d3c2edd50ea17))
* **serve:** no-auth exposure refusal names auth.mode ([e4de35e](https://github.com/hop-top/poly-kit/commit/e4de35e3b7852e27b2ce9a11d6bcf330c45b8d99))
* **serve:** refusal before the body waits for a body still arriving ([1c0442d](https://github.com/hop-top/poly-kit/commit/1c0442dc15d44659306d718a1ba6c0314be9d1a2))
* **serve:** stalled clients no longer hold stop or refused connections ([409c53a](https://github.com/hop-top/poly-kit/commit/409c53a2604f24bb48dfa635f7c5527318e15d2a))
* **transport:** kit/auth-required admits only an established caller ([d9a85bd](https://github.com/hop-top/poly-kit/commit/d9a85bd882f2ab8f3273c7c6ee861d6da1e8bc4e))


### Performance

* **redact:** screen rules by literal and length before regex ([9b02352](https://github.com/hop-top/poly-kit/commit/9b02352437e7484695816ea5a304256559b5ac99))


### Build

* **templates:** pin scaffold tools to the versions kit's CI runs ([2464b2c](https://github.com/hop-top/poly-kit/commit/2464b2c24e210a7a43155c2bf7ff277e712a53b4))

Full diff: [kit/v0.5.0-alpha.15...kit/v0.5.0-alpha.16](https://github.com/hop-top/poly-kit/compare/kit/v0.5.0-alpha.15...kit/v0.5.0-alpha.16)

## [0.5.0-alpha.15](https://github.com/hop-top/poly-kit/compare/kit/v0.5.0-alpha.14...kit/v0.5.0-alpha.15) (2026-09-26)


### Features

* **cli:** compact GLOBAL FLAGS and --help-&lt;group&gt; row in Go help ([57dfe86](https://github.com/hop-top/poly-kit/commit/57dfe86aa19497e30a4feb8f9fa125aa93ebc9dd))


### Bug Fixes

* **cli:** keep --no-hints in default GLOBAL FLAGS ([45bcdc8](https://github.com/hop-top/poly-kit/commit/45bcdc867ba1e0545b4d9dffda10e2f2871faafd))
* **cli:** keep root GLOBAL FLAGS split on repeat Execute ([a796930](https://github.com/hop-top/poly-kit/commit/a796930f29d203650e487573a4510e4983871a17))
* **cli:** short --offline usage, matching TS/Python ([299e20a](https://github.com/hop-top/poly-kit/commit/299e20a8daa1951afc57ead6864f0d992dce5b29))

## [0.5.0-alpha.14](https://github.com/hop-top/poly-kit/compare/kit/v0.5.0-alpha.13...kit/v0.5.0-alpha.14) (2026-09-25)


### Bug Fixes

* **cli:** no command-group header without visible commands ([325e0db](https://github.com/hop-top/poly-kit/commit/325e0db4ea154598055cfe86533fbb3a6173ea74))
* **cli:** title root's ungrouped commands COMMANDS in cobra help ([bde77a9](https://github.com/hop-top/poly-kit/commit/bde77a927a87eb1ab6324b5883daa238c956794a))

## [0.5.0-alpha.13](https://github.com/hop-top/poly-kit/compare/kit/v0.5.0-alpha.12...kit/v0.5.0-alpha.13) (2026-09-25)


### ⚠ BREAKING CHANGES

* **toolspec:** manifest `side_effect` and `idempotent` carry `"unknown"` where they previously carried `""` for an unannotated command. Consumers matching on the empty string must accept both.

### Features

* **toolspec:** undeclared side-effect is unknown, not read ([2c53c89](https://github.com/hop-top/poly-kit/commit/2c53c897196db3fa0fc319d4c8f8b4c8417c4fc8))


### Bug Fixes

* **build:** pin Go toolchain and keep the push gate finishable ([b8d4e9a](https://github.com/hop-top/poly-kit/commit/b8d4e9a01c1b0ed8ad9f2c660d609e48e52ff838))
* **toolspec:** render MCP tools per leaf, matching live server ([be86dec](https://github.com/hop-top/poly-kit/commit/be86dec0ff58252f04ab444795bd398b89d9fcf5))

## [0.5.0-alpha.12](https://github.com/hop-top/poly-kit/compare/kit/v0.5.0-alpha.11...kit/v0.5.0-alpha.12) (2026-09-23)


### Bug Fixes

* **cli:** render status under tag-driven formats ([8a48e1d](https://github.com/hop-top/poly-kit/commit/8a48e1d6871d2a591c48bd352a68a1decfcc9151))

## [0.5.0-alpha.11](https://github.com/hop-top/poly-kit/compare/kit/v0.5.0-alpha.10...kit/v0.5.0-alpha.11) (2026-09-22)


### ⚠ BREAKING CHANGES

* **cli:** root --help moves persistent globals out of FLAGS into a GLOBAL FLAGS section for every downstream CLI. Scripts scraping the root FLAGS block need updating. HelpConfig.SplitGlobals no longer exists; drop the field from any Config literal setting it.

### Bug Fixes

* **cli:** split GLOBAL FLAGS on root help by default ([dcd2b88](https://github.com/hop-top/poly-kit/commit/dcd2b88d7926ad408c00fbbb9f64d834c98cf117))
* **cli:** strip GLOBAL FLAGS heading styling under --no-color ([15fd049](https://github.com/hop-top/poly-kit/commit/15fd0497f3e61dbfd6a788349779d5274a7819b1))

## [0.5.0-alpha.10](https://github.com/hop-top/poly-kit/compare/kit/v0.5.0-alpha.9...kit/v0.5.0-alpha.10) (2026-09-22)


### Features

* **cli:** opt-in GLOBAL FLAGS split on root help ([44fd503](https://github.com/hop-top/poly-kit/commit/44fd503f5a13a2346290d59021c734aa1d1e9b5b))

## [0.5.0-alpha.9](https://github.com/hop-top/poly-kit/compare/kit/v0.5.0-alpha.8...kit/v0.5.0-alpha.9) (2026-09-21)


### Features

* **routellm:** accept tier names in model field ([84db50a](https://github.com/hop-top/poly-kit/commit/84db50a9258883a86e67cefc52133d955ce797ae))

## [0.5.0-alpha.8](https://github.com/hop-top/poly-kit/compare/kit/v0.5.0-alpha.7...kit/v0.5.0-alpha.8) (2026-09-18)


### ⚠ BREAKING CHANGES

* **mcpsdk:** the tasks extension module path changes from mcpext.example/tasks to hop.top/mcp-tasks. The old path never resolved for consumers, so only in-repo importers are affected.

### Features

* **parity:** pin the exit-code taxonomy across all five ports ([#319](https://github.com/hop-top/poly-kit/issues/319)) ([dbe68c5](https://github.com/hop-top/poly-kit/commit/dbe68c5987738f62954597a8038e7b2929688005))


### Bug Fixes

* **ci:** detect promotions by value, not diff text ([649f634](https://github.com/hop-top/poly-kit/commit/649f6342da607171e9b46c6ff048292ed61f2bf3))
* **deps:** require mcp-tasks v0.1.0-alpha.1 ([e6bb95d](https://github.com/hop-top/poly-kit/commit/e6bb95dc186b35cd4d52709e77a8b34b9ff32e38))
* **mcpsdk:** rename tasks extension to hop.top/mcp-tasks ([9e0ecec](https://github.com/hop-top/poly-kit/commit/9e0ecec18c7ac2c294e2a418c498ff7bcd15e6a1))
* **sdk:** add CONSENT_REFUSED and PREREQUISITE to the four SDK ports ([#314](https://github.com/hop-top/poly-kit/issues/314)) ([c937bef](https://github.com/hop-top/poly-kit/commit/c937befdab1030f77bc13ee9f641430b44f43705))
* **transport:** map CONSENT_REFUSED and PREREQUISITE off 500 ([#318](https://github.com/hop-top/poly-kit/issues/318)) ([66a9c54](https://github.com/hop-top/poly-kit/commit/66a9c540ffde0c1d5a8207a6d26f750fe950f7e3))

## [0.5.0-alpha.7](https://github.com/hop-top/poly-kit/compare/kit/v0.5.0-alpha.6...kit/v0.5.0-alpha.7) (2026-09-12)


### Features

* **output:** add CONSENT_REFUSED code at exit 7 ([#307](https://github.com/hop-top/poly-kit/issues/307)) ([210f5ef](https://github.com/hop-top/poly-kit/commit/210f5eff0b1d5664f0c172b140991849a543bcfc))
* **output:** add PREREQUISITE code at exit 70 ([#310](https://github.com/hop-top/poly-kit/issues/310)) ([0aebeba](https://github.com/hop-top/poly-kit/commit/0aebebaadfeb2bccc842c9970ee77d2087f547c3))


### Bug Fixes

* **cli:** code policy load failure as USAGE, not UNAUTHORIZED ([#312](https://github.com/hop-top/poly-kit/issues/312)) ([fc9ea14](https://github.com/hop-top/poly-kit/commit/fc9ea14427d033e83ec461a7f4452a39abef69e2))
* **output:** classify scenario-grader codes in TransienceForCode ([#311](https://github.com/hop-top/poly-kit/issues/311)) ([d85e829](https://github.com/hop-top/poly-kit/commit/d85e829dd4a1aa4ff804031d0283b00b56fd5e61))

## [0.5.0-alpha.6](https://github.com/hop-top/poly-kit/compare/kit/v0.5.0-alpha.5...kit/v0.5.0-alpha.6) (2026-09-12)


### Bug Fixes

* **cli:** classify confirm refusals as transient ([#302](https://github.com/hop-top/poly-kit/issues/302)) ([5fd0872](https://github.com/hop-top/poly-kit/commit/5fd0872f86201a444f36712cba1b6e7fb5a30c1a))

## [0.5.0-alpha.5](https://github.com/hop-top/poly-kit/compare/kit/v0.5.0-alpha.4...kit/v0.5.0-alpha.5) (2026-09-11)


### Bug Fixes

* **hooks:** unblock pre-push on branch deletion ([7e4a795](https://github.com/hop-top/poly-kit/commit/7e4a795abb4ee0f43f3cc4f4c22ef004845edbc2))
* **output:** truncate overlong table cells instead of dropping columns ([9c5ed6a](https://github.com/hop-top/poly-kit/commit/9c5ed6a02d93c62c2e26e246c8bf3fca77506290))

## [0.5.0-alpha.4](https://github.com/hop-top/poly-kit/compare/kit/v0.5.0-alpha.3...kit/v0.5.0-alpha.4) (2026-09-08)


### ⚠ BREAKING CHANGES

* **console:** kit conformance harness record exit codes change; scripts branching on 3, 4 or 5 need updating.
* **conformance:** `hop.top/kit/go/console/cli/conformance/harness` and `.../verifynoleak/...` move to `hop.top/kit/go/conformance/harness` and `hop.top/kit/go/conformance/verifynoleak/...`. Adopters importing the harness toolkit update the import path; no API change. No forwarding aliases — kit ships breaking changes directly on the alpha channel.
* **cli:** the api service refuses to start on a non-loopback address when no delegation policy is configured. Name a `--policy`, listen on loopback, or set `services.api.insecure_no_policy: true` to restore the previous behaviour.
* **kv:** `kv.Open` requires importing the driver package for the chosen backend; sqlite and badger were previously linked in unconditionally.

### Features

* **bridge:** match payloads to manifest accept rules ([5fcabd3](https://github.com/hop-top/poly-kit/commit/5fcabd384b65c623a8f7e892ba7f2eeffe4560da))
* **bridge:** match payloads to manifest accept rules ([af57a67](https://github.com/hop-top/poly-kit/commit/af57a67736c501ed701c5395631e95bee804fb62))
* **cli:** opt-in flag autocorrect for read-only commands ([#291](https://github.com/hop-top/poly-kit/issues/291)) ([aae90c4](https://github.com/hop-top/poly-kit/commit/aae90c462444c78c3198c6ec4a43974e9905dc8e))
* **cli:** suggest corrections on flag parse errors ([#290](https://github.com/hop-top/poly-kit/issues/290)) ([05a93d9](https://github.com/hop-top/poly-kit/commit/05a93d908d48f85b4ebf4e54c77bf3c9c6c7af75))
* **engine:** optional If-Match precondition on document PUT ([#280](https://github.com/hop-top/poly-kit/issues/280)) ([4433446](https://github.com/hop-top/poly-kit/commit/443344686d0cbf548c414fad7c9bc45bd72085b6))
* **kv:** add context-carrying opener alongside Opener ([6998bac](https://github.com/hop-top/poly-kit/commit/6998bac9414809b9d9a0dcdcdb8a3d99c024387f))
* **sdk:** port the consenting-telemetry factor to the TS and Python compliance checkers ([#281](https://github.com/hop-top/poly-kit/issues/281)) ([6e205b8](https://github.com/hop-top/poly-kit/commit/6e205b889a365e2e3a648dff60f975d1d9dba0cd))


### Bug Fixes

* **cli:** deny remote serving without a delegation policy ([217f60e](https://github.com/hop-top/poly-kit/commit/217f60ef412c11e73e841749159835db648250d3))
* **cli:** keep the discarded dispatch off `fang`'s terminal-probing error handler ([#295](https://github.com/hop-top/poly-kit/issues/295)) ([d48c404](https://github.com/hop-top/poly-kit/commit/d48c40454f49be105e1101131f4cb3e3a0fab7fc))
* **cli:** read the confirm prompt from `/dev/tty`, not the command's stdin ([#292](https://github.com/hop-top/poly-kit/issues/292)) ([c9322b0](https://github.com/hop-top/poly-kit/commit/c9322b0bde8a11619e65d5583e60e80b76cefc28))
* **console:** resolve four doc-versus-source drifts ([#274](https://github.com/hop-top/poly-kit/issues/274)) ([a6582ff](https://github.com/hop-top/poly-kit/commit/a6582ffe1ac6467d69ce3a4c4c0a25ef892caf53))
* **contracts:** correct buf es out path, drop orphan ts stubs ([#275](https://github.com/hop-top/poly-kit/issues/275)) ([6a8c185](https://github.com/hop-top/poly-kit/commit/6a8c18545a1ad3c2732eb4eecae485e3b30288ec))
* **hooks:** resolve the pre-push base to the nearest upstream ([#293](https://github.com/hop-top/poly-kit/issues/293)) ([7ef032c](https://github.com/hop-top/poly-kit/commit/7ef032cd93e6693dc1889ba3f270aaa2815cf7ae))
* **kv:** guard the TiDB driver dial ([d1c5d11](https://github.com/hop-top/poly-kit/commit/d1c5d11bdfbdbedc1167d5f49a06bd39ee0af011))
* **kv:** police the initial connect in all four drivers ([74b4d98](https://github.com/hop-top/poly-kit/commit/74b4d98d5ef3ed67e5157268fb0060700aa738d0))
* **kv:** police the initial connect through a context-carrying opener ([5d59531](https://github.com/hop-top/poly-kit/commit/5d5953116d5dc31b1ed4bfc94c3b201b3c73ba71))
* **kv:** reach etcd and tidb through Open via driver registry ([3695db6](https://github.com/hop-top/poly-kit/commit/3695db625fbb72a6a5d17a240d104f9da139bbe9))
* **netpolicy:** enforce --offline beyond net/http ([19d73b4](https://github.com/hop-top/poly-kit/commit/19d73b4656e8ca73ca6623bcaac36de3e7293241))
* **netpolicy:** extend offline enforcement past net/http ([d9f55cf](https://github.com/hop-top/poly-kit/commit/d9f55cf50d09f444a1589140cb6896cbd0417f38))
* **notify:** route SMTP dial through the offline guard ([db2d94f](https://github.com/hop-top/poly-kit/commit/db2d94f32c3816bb747ff344a26082bd73575764))
* route kit's own egress call sites through the netpolicy guards ([b164f05](https://github.com/hop-top/poly-kit/commit/b164f05e807d248bf89aaae1edf427411a01010b))
* **secret:** register file and openbao backends ([138957c](https://github.com/hop-top/poly-kit/commit/138957cc43348fabe13b5197c99828a66bad1a99))
* **secret:** register file and openbao backends ([91e33cf](https://github.com/hop-top/poly-kit/commit/91e33cf94799f52d95d32dd4704cf8c55a07c403))
* **transport,bus:** guard WebSocket handshakes explicitly ([db635ba](https://github.com/hop-top/poly-kit/commit/db635ba233e74d24a189971462cf59a0113b8e4a))


### Performance

* **engine:** drop fsync in the store property tests' SQLite databases ([#288](https://github.com/hop-top/poly-kit/issues/288)) ([7a27205](https://github.com/hop-top/poly-kit/commit/7a27205f869d21ee609c09597e4d51e001f2c299))
* **engine:** parallelize and short-gate the store property tests ([#287](https://github.com/hop-top/poly-kit/issues/287)) ([08463ca](https://github.com/hop-top/poly-kit/commit/08463cacc1e5f8223592d359091cb8ece0d9403b))


### Refactored

* **conformance:** move adopter libraries out of the command tree ([4efdce2](https://github.com/hop-top/poly-kit/commit/4efdce25b6a040039fe830ad04a6173186e6b627))

## [0.5.0-alpha.3](https://github.com/hop-top/poly-kit/compare/kit/v0.5.0-alpha.2...kit/v0.5.0-alpha.3) (2026-09-05)


### ⚠ BREAKING CHANGES

* **cli:** unresolvable `help <topic>` exits 2 (USAGE) instead of 1 (GENERIC).
* **cli:** WithAPI without Auth on a non-loopback Addr (":8080", "0.0.0.0:…") is refused at validation with exit 2 unless services.api.insecure_remote / --insecure-remote is set.

### Features

* **api:** adopter control over projected command policy and exposure ([fd63fc9](https://github.com/hop-top/poly-kit/commit/fd63fc99080a57aef2c8731a0f12dadb1acc5c46))
* **api:** project reflected commands onto versioned REST with OpenAPI ([53f9b76](https://github.com/hop-top/poly-kit/commit/53f9b769bddbc12e8dda290dec89e8c7a14c4e2c))
* **api:** project reflected commands onto versioned REST with OpenAPI ([b917f13](https://github.com/hop-top/poly-kit/commit/b917f13546a3cd47c2323b6a433c7d61fb14ae1b))
* **cli:** bind the api service to loopback and refuse unauthenticated remote serving ([cc440b4](https://github.com/hop-top/poly-kit/commit/cc440b49937f88a914ca36d46e2659f25fc7766d))
* **cli:** Kit-owned serve parent, service registry option, WithAPI as the api service ([e5290a6](https://github.com/hop-top/poly-kit/commit/e5290a6a39cc51e8d82c0b1323051b987b4fc889))
* **cli:** let adopters set the socket policy gate ([a253e64](https://github.com/hop-top/poly-kit/commit/a253e6403599dd54215fa27798d369ecbed7a623))
* **cli:** prepare trees without executing and serve on a root factory ([b1c3637](https://github.com/hop-top/poly-kit/commit/b1c3637396f6b56a1f762620a92554cb30af43f9))
* **cli:** serve socket over a unix domain socket ([207a10d](https://github.com/hop-top/poly-kit/commit/207a10d06e9a3cdf856ecd50669f4eae046012db))
* **cmdreflect:** classify self-hosting commands ([83dcbe8](https://github.com/hop-top/poly-kit/commit/83dcbe848f568bb651e94bc0b805e6895f67f38e))
* **cmdsurface:** central permission gate and audit emission on the bridge ([d2ca916](https://github.com/hop-top/poly-kit/commit/d2ca9164771a4bdf9690202a24a77e9ba4305868))
* **cmdsurface:** isolate invocations and decode declared output ([5e5d139](https://github.com/hop-top/poly-kit/commit/5e5d1396a3eec03a25dc66825eeb5068cb91d368))
* **cmdsurface:** isolate invocations and decode declared output ([f62164d](https://github.com/hop-top/poly-kit/commit/f62164da2513d95234d824e91f605f5dbc8bf0e2))
* **cmdsurface:** refuse interactive and self-hosting leaves at the bridge ([159e0c2](https://github.com/hop-top/poly-kit/commit/159e0c21d300dbb9f529223d9eb2e58c98c94eea))
* **kit:** serve the document engine as the api service ([74c7b1e](https://github.com/hop-top/poly-kit/commit/74c7b1e60136750f8accd6cb193404af4ccf7ff6))
* **output:** GenericError constructor and ExitGeneric for the exit-1 class ([524002d](https://github.com/hop-top/poly-kit/commit/524002d606a45765f398370757d0f7c4c2ce8ae1))
* **php:** port the serve supervisor and service selector ([d2d7564](https://github.com/hop-top/poly-kit/commit/d2d75647aa918cacf5a727e63cc14b2d810232b8))
* **py:** port the serve supervisor and service selector ([8386820](https://github.com/hop-top/poly-kit/commit/8386820112b64fc56bfe202440594e607bf6cc81))
* **reflect:** canonical command descriptor with non-invocable reasons ([de2b05c](https://github.com/hop-top/poly-kit/commit/de2b05cbf20565a6e2a45b0acaeb0395b20f42db))
* **reflect:** canonical command descriptor with non-invocable reasons ([1b73e4a](https://github.com/hop-top/poly-kit/commit/1b73e4a19228e34565701f91b9e8ceb971820f2c))
* **rs:** mount serve as a clap command ([60f6099](https://github.com/hop-top/poly-kit/commit/60f6099df9159d1c3821d9de642da787dadf0a04))
* **rs:** mount serve as a clap command ([eb8c140](https://github.com/hop-top/poly-kit/commit/eb8c140bff60cf74f31cdce4c3d16b30dda12e56))
* **rs:** serve supervisor and service lifecycle ([8a20b5d](https://github.com/hop-top/poly-kit/commit/8a20b5d17e9374400fd00e7ccf0cb2cc03101c49))
* **serve:** contract types, registry seam, selection resolution ([ba00b6f](https://github.com/hop-top/poly-kit/commit/ba00b6f7f0e869616204cbbc855bc46dd280a084))
* **serve:** supervisor with ordered start, readiness, and policy-driven stop ([3ac2898](https://github.com/hop-top/poly-kit/commit/3ac2898d3aa744621dad536675dc650f76d7f59d))
* **serve:** supervisor with ordered start, readiness, and policy-driven stop ([281656f](https://github.com/hop-top/poly-kit/commit/281656fd0694fb4d85c433d80962f7b919bb7e40))
* **serve:** transport-service registration seam ([9a9a7ba](https://github.com/hop-top/poly-kit/commit/9a9a7ba0b157fb06af9c62e4d5d690a8e5ce9c26))
* **serve:** transport-service registration seam ([cfa4dab](https://github.com/hop-top/poly-kit/commit/cfa4dab34cbeca16530e78067f2cbe09f8ab27d9))
* **socket:** verified identity, per-connection cancellation, DENIED code ([a52c0a1](https://github.com/hop-top/poly-kit/commit/a52c0a11b564ca6cc5605d97da427c5b0ec3cc9d))
* **templates:** make the cli-go tier-3 root a served kit root ([5fe0ac5](https://github.com/hop-top/poly-kit/commit/5fe0ac577e9c39e8ceabceabeb0c7abc7a47b42a))
* **templates:** make the cli-go tier-3 root a served kit root ([bab0557](https://github.com/hop-top/poly-kit/commit/bab055796b32010645a0513a17da299e3157f0d6))
* **templates:** ship an agent-facing AGENTS.md in cli-go projects ([de6ad86](https://github.com/hop-top/poly-kit/commit/de6ad86db2976b16942415bd401b3efa53f10158))
* **templates:** ship an agent-facing AGENTS.md in cli-go projects ([d10889d](https://github.com/hop-top/poly-kit/commit/d10889d43adc684d881746913c254ee7d0b291ec))
* **transport:** serve commands over a unix domain socket ([9e7f6c7](https://github.com/hop-top/poly-kit/commit/9e7f6c706d01a8ba34a1b6d01ab6b7c176146488))
* **transportsvc:** resolve bridge options at start ([6ca6e57](https://github.com/hop-top/poly-kit/commit/6ca6e578d88688620c32627730ddba95947f9a83))
* **ts:** serve cross-language contract and TypeScript implementation ([165a82a](https://github.com/hop-top/poly-kit/commit/165a82a97d8640d58448d67e88d9ec98e3e5fa05))


### Bug Fixes

* **api:** carry confirmation as the command's own flags over REST ([522bab5](https://github.com/hop-top/poly-kit/commit/522bab50074de5f41a941f701fd5c69a6024bdb7))
* **cli:** annotate the token leaves so an authenticating root validates ([c6e344c](https://github.com/hop-top/poly-kit/commit/c6e344cef1c7a9ed5076eff9eb6c2002a738d234))
* **cli:** assert the taxonomy exit code for a socket refusal ([6b5ed8e](https://github.com/hop-top/poly-kit/commit/6b5ed8eb661566648766e7233bb3eae2cfeeafff))
* **cli:** classify cobra argument and flag errors as USAGE ([38eeaba](https://github.com/hop-top/poly-kit/commit/38eeabadedf2f5e72531d518d2501ce965249389))
* **cli:** classify cobra argument and flag errors as USAGE ([fc308ea](https://github.com/hop-top/poly-kit/commit/fc308ea4958569a2b6ff00d99d98a48e1e5ab89d))
* **cli:** honour a help flag ahead of an unknown subcommand ([0885012](https://github.com/hop-top/poly-kit/commit/08850121e6e9b9d24a4632ec0d30ab4e8310a84f))
* **cli:** list the api service as the supervisor resolves it ([6209da5](https://github.com/hop-top/poly-kit/commit/6209da555d7523ae457defd21e6241a10f3ec2f1))
* **cli:** refuse unknown subcommand under a non-runnable parent ([93f7722](https://github.com/hop-top/poly-kit/commit/93f7722f353504a353f6e55bcbd003942ad8c2d0))
* **cli:** refuse unknown subcommand under a non-runnable parent ([7069c8b](https://github.com/hop-top/poly-kit/commit/7069c8b7e0a2434c86c5590907d79cf9cd46a2c0))
* **cli:** reset flags to defaults before a repeat Execute ([e80e8fa](https://github.com/hop-top/poly-kit/commit/e80e8fa324ddf000a34f8e61730f06aeab4ce041))
* **cli:** resolve `help <name>` as command path before group id ([38f0341](https://github.com/hop-top/poly-kit/commit/38f03418f59b2fb85dd539ac546f34e8159ff0f3))
* **cmdreflect:** classify serve as self-hosting at any depth ([d82b640](https://github.com/hop-top/poly-kit/commit/d82b6405a326b1253ac451f0b75c5149c47037ba))
* **cmdreflect:** skip nameless commands ([39c43a0](https://github.com/hop-top/poly-kit/commit/39c43a06626aafd00a793160295126eb6d4602fb))
* **cmdsurface:** clear Changed on callback-backed flags when resetting ([1281dfe](https://github.com/hop-top/poly-kit/commit/1281dfe6d91303a1ec059fc7794ab0f6fcd9d18b))
* **cmdsurface:** decode confirm-state MAC canonically ([a0ca7bf](https://github.com/hop-top/poly-kit/commit/a0ca7bfc41ac675daba3acc889e70351a8eba2fe))
* **cmdsurface:** derive in-process exit code from the error ([916d917](https://github.com/hop-top/poly-kit/commit/916d917906a6088576a11cb4ceea033644eb92d0))
* **make:** fail test-go-race when go list does not fully resolve ([4d82bb1](https://github.com/hop-top/poly-kit/commit/4d82bb13d93faebe46b7f7ca10708569bf058441))
* **parity:** record and implement serve --enable/--disable and timeout flags ([e38b143](https://github.com/hop-top/poly-kit/commit/e38b143c77771c7abe088557f058be81922dd70a))
* **parity:** record serve --enable/--disable and timeout flags as behaviors ([64b637a](https://github.com/hop-top/poly-kit/commit/64b637a921fb8a65d55cc90f3e5a531185f64159))
* **release:** let the python updater write PEP 440 into pyproject.toml ([1a20cf3](https://github.com/hop-top/poly-kit/commit/1a20cf3d0261a21b75f462cd43d71149a58aaaca))
* **routellm:** reject zero-length config reads in watcher ([319f44a](https://github.com/hop-top/poly-kit/commit/319f44aeb5eb1cc636f229f03f3a6c16d02a0546))
* **routellm:** reject zero-length config reads in watcher ([e9c93f6](https://github.com/hop-top/poly-kit/commit/e9c93f6fbe66fb3b4d4cbfb8dda4a0917ae59ad5))
* **serve:** make stop-abandonment observable, deflake supervisor tests ([ffe13e0](https://github.com/hop-top/poly-kit/commit/ffe13e00fb015f6b5df8a86781e2461577d9bb3a))
* **serve:** report a self-stopping service stopped once ([3f0600e](https://github.com/hop-top/poly-kit/commit/3f0600e8ce8e1c03ddbe50e37a9e3c3fa3b164e2))
* **serve:** run a second serve on the same Root under its own context ([c63d4d3](https://github.com/hop-top/poly-kit/commit/c63d4d3000a0ab3bcc6e8cc20cd664b8b5205301))
* **templates:** make check-mirror-sync portable to GNU diff ([15840bb](https://github.com/hop-top/poly-kit/commit/15840bbded82c871ba838d0a48d0fe5865027769))
* **templates:** ship version.go as .tmpl; mirror is a verbatim copy ([f68ce2c](https://github.com/hop-top/poly-kit/commit/f68ce2c591a9c9e3f2d54c70f05e1a4014bd841e))
* **templates:** ship version.go as .tmpl; mirror is a verbatim copy ([456f63f](https://github.com/hop-top/poly-kit/commit/456f63f7a31ee13291e5e3b0a8ad5939230d36d5))
* **templates:** stop the agent doc's opener from hanging its reader ([9729a92](https://github.com/hop-top/poly-kit/commit/9729a923f43fea45e9e3e69de7600ed8fd1a8efc))
* **transport:** drop redundant socket unlink on close ([a6c5976](https://github.com/hop-top/poly-kit/commit/a6c5976c3e1bb8fc689072fec463b1cb51e9b6ac))
* **xdg:** serialize adrg/xdg resolves to end global-state race ([dadf5f5](https://github.com/hop-top/poly-kit/commit/dadf5f5440006dbde49d08da200d3297ed10ae70))
* **xdg:** serialize adrg/xdg resolves to end global-state race ([e787673](https://github.com/hop-top/poly-kit/commit/e787673dc777fda386094c2ef0fc5a8c92bf93c0))

## [0.5.0-alpha.2](https://github.com/hop-top/poly-kit/compare/kit/v0.5.0-alpha.1...kit/v0.5.0-alpha.2) (2026-09-04)


### ⚠ BREAKING CHANGES

* **cli:** `--profile` and `--instance` are no longer accepted and now error as unknown flags. No caller referenced the removed Go helpers.
* **util:** observable behaviour of ParseUntil/ParseUntilAt/ParseSince/ ParseSinceAt changes for month and year units when the reference day-of-month does not exist in the target month. Previously rolled forward into the next month; now clamps to the last valid day. Examples: "in 1 month" on 2026-01-31 was 2026-03-03, now 2026-02-28; "1 year ago" on 2028-02-29 was 2027-03-01, now 2027-02-28. Unaffected when the day exists in the target month.
* **util:** observable behaviour of ParseUntil/ParseUntilAt/ParseSince/ ParseSinceAt changes for month and year units when the reference day-of-month does not exist in the target month. Previously rolled forward into the next month; now clamps to the last valid day. Examples: "in 1 month" on 2026-01-31 was 2026-03-03, now 2026-02-28; "1 year ago" on 2028-02-29 was 2027-03-01, now 2027-02-28. Unaffected when the day exists in the target month.
* **tasks:** `Extension.Handler` removed; `Extension.Attach` now returns error. Hosts mount the SDK handler directly.
* **conformance:** `kit conformance` and `kit conformance grade` exit codes renumbered; consumers pinning 2/3/4/5 must update or pin an earlier kit release.
* **output:** ExitProvenanceMissing now 65 (was 6); exit 6 now means transient/retryable. Consumers branching on exit 6 for provenance refusals must switch to 65.
* **output:** structured error envelopes now emit a transience key; consumers pinning exact stderr JSON/YAML must account for it.
* **redact:** `Redactor.Allow` is removed — call `AllowExact` with the full literal value each prefix stood in for. Rule packs using the TOML `allowlist` key still load, but the key is ignored and exempts nothing; migrate entries to `allowlist_exact`.
* **redact:** `Allow` and the TOML `allowlist` key are deprecated and will be removed. Migrate to `AllowExact` / `allowlist_exact`, replacing prefixes with the full literal values they stood in for.

### Features

* **cli:** drop unused `--profile` and `--instance` globals ([0121ecf](https://github.com/hop-top/poly-kit/commit/0121ecf8c52c3363bedad1e718ecbde8ddeb4991))
* **cli:** enforce --offline at the transport layer ([473c877](https://github.com/hop-top/poly-kit/commit/473c87757fbaeae3c149d148950b6198e5664ec1))
* **cli:** offline/profile/instance globals + offline opt-out override ([8fbbb53](https://github.com/hop-top/poly-kit/commit/8fbbb53a627797434ee9a9c7101ca6cd3f2f922a))
* **cmdsurface:** add WithMCPConfirmationKey option ([7735b7d](https://github.com/hop-top/poly-kit/commit/7735b7db538a0dc7c7551b1a41357fe6fdbb09ab))
* **cmdsurface:** enforce Mcp-Method/Mcp-Name header validation ([930300f](https://github.com/hop-top/poly-kit/commit/930300f7b9a987b00891c3b195d9612543d199f4))
* **cmdsurface:** era-detection dispatch seam for dual-spec MCP mount ([09e3b8a](https://github.com/hop-top/poly-kit/commit/09e3b8a8961a48b493bb7a4b691626c848f848b1))
* **cmdsurface:** implement 2026-07-28 modern MCP handler ([be95720](https://github.com/hop-top/poly-kit/commit/be95720425c65d6543c32cdc2cb1f27eb0cd8505))
* **cmdsurface:** mrtr elicitation confirmation on modern mcp calls ([4c667b5](https://github.com/hop-top/poly-kit/commit/4c667b53109db7b29785093119a9184749a64afb))
* **cmdsurface:** RFC 9207 issuer validation on OAuth callback ([21c4e98](https://github.com/hop-top/poly-kit/commit/21c4e98b94edbd4f02cce267ab3e4a6989501b1b))
* **conformance:** add cassette recorder library ([b3ba0ee](https://github.com/hop-top/poly-kit/commit/b3ba0ee18122a802a77e89d39927ae3c55e526af))
* **conformance:** align exit codes with shared taxonomy ([2822591](https://github.com/hop-top/poly-kit/commit/2822591d807bf4aeb1d620eeead2098fd8c70e8c))
* **conformance:** grade svc uploads with the real scenario grader ([1fe3faf](https://github.com/hop-top/poly-kit/commit/1fe3faf359fd3696ab6002f954bf18bf2e3b2880))
* **conformance:** implement harness record CLI leaf ([a75c41c](https://github.com/hop-top/poly-kit/commit/a75c41c33cc77d5035ed0d832ff27db24c183b74))
* **console/output:** add WithCols render option for column projection ([2326c78](https://github.com/hop-top/poly-kit/commit/2326c78fa36c228b538b6d7ab58f4c16475b1d9b))
* **httpcache:** pin cache-key derivation as cross-language fixture ([5081a69](https://github.com/hop-top/poly-kit/commit/5081a6936a429c95c3602d97ff99b779bd0561b7))
* **init:** 12fcc CI gate template + follow-up step; badge file -&gt; .12fc.json ([98994b6](https://github.com/hop-top/poly-kit/commit/98994b6adc055737c14fdaaa9f4dee810b4b0c57))
* **init:** compose shared template + managed blocks into scaffolds ([536e88b](https://github.com/hop-top/poly-kit/commit/536e88baff577b30ab632bcc030b5f54a92bc442))
* **init:** hop-aware augment — dirty-tree guard + branch in summary ([9fd32f1](https://github.com/hop-top/poly-kit/commit/9fd32f1b0a5fd2ce7a2dbd8a915ba01bf3066842))
* **llm:** classifier stage in front of provider picker ([02e7d4a](https://github.com/hop-top/poly-kit/commit/02e7d4a47c8ac90e996a304f24cff2daf9042bd6))
* **mcp-tasks:** add standalone SEP-2663 tasks extension module ([6dc08ce](https://github.com/hop-top/poly-kit/commit/6dc08ce2a41d102b918696601d9653e9e00ee7ec))
* **mcpsdk:** add SDK-backed MCP surface ([ab0c589](https://github.com/hop-top/poly-kit/commit/ab0c589b4d2929c21d6cf9dedc19cc8ad96beba5))
* **mcpsdk:** bind task-eligible leaves through tasks extension ([5c05733](https://github.com/hop-top/poly-kit/commit/5c05733e7d374345545ab68210ce4c38908b970d))
* **mcpsdk:** full SDK capability pass-through, live tool list, progress streaming ([b3ba755](https://github.com/hop-top/poly-kit/commit/b3ba755e13c8950f0dce4f6e8a2f436fffaac30f))
* merge offline-transport ([9c20087](https://github.com/hop-top/poly-kit/commit/9c20087cab95e8006929155d1c59c1a3afb20738))
* merge offline-transport ([6fa2303](https://github.com/hop-top/poly-kit/commit/6fa2303f7aa212d8ec2fb88ee1f200c54b9e2107))
* **netpolicy:** exempt logging-class egress from `--offline` ([10ac5cc](https://github.com/hop-top/poly-kit/commit/10ac5cc5abc458dd61297fa92f5fa465db575486))
* **output:** add transience class to structured error envelope ([cb54d56](https://github.com/hop-top/poly-kit/commit/cb54d565968071b54e53c64538dedf9c294f61e3))
* **output:** assign exit 6 to transient failures, move provenance to 65 ([deb2b3c](https://github.com/hop-top/poly-kit/commit/deb2b3cc78a3cfe641c36d33d4a07daee8dfcaee))
* **output:** retain wrapped error for errors.Is matching ([c6c11fa](https://github.com/hop-top/poly-kit/commit/c6c11fa4372e14693f2ccb6a8205d18807524d61))
* **output:** retain wrapped error for errors.Is matching ([f710706](https://github.com/hop-top/poly-kit/commit/f71070629887fc9074fb5607371fb8418a2ce991))
* **py:** enforce `--offline` at the urllib layer ([537c1f3](https://github.com/hop-top/poly-kit/commit/537c1f3d51b56df28e19af690ee650e14bf0c0bd))
* **redact:** add AllowExact, deprecate substring allowlists ([891fd2d](https://github.com/hop-top/poly-kit/commit/891fd2defe305e5f072abf9f2cb32d3d7f85b95e))
* **redact:** remove substring allowlists ([7397246](https://github.com/hop-top/poly-kit/commit/73972464fb425e66d87f034ce55d2ce3cfc26d10))
* **router:** classifier adapters for the picker seam ([8dbce9e](https://github.com/hop-top/poly-kit/commit/8dbce9e34bfb2b8e3cf56201649add42ca9f8ae4))
* **rs:** add sqlstore typed kv store over sqldb ([acbf0a6](https://github.com/hop-top/poly-kit/commit/acbf0a673573d6adb2403505096ddb6b4b258885))
* **ts:** enforce --offline at the fetch layer ([baf17b2](https://github.com/hop-top/poly-kit/commit/baf17b2ab53c893533f440cac56806c437bb5cb8))
* **ts:** serve dual-spec MCP surface ([e9f9ada](https://github.com/hop-top/poly-kit/commit/e9f9ada729d20164facffefcf11f86514887223f))


### Bug Fixes

* **blob/local:** atomic Put via temp file + rename ([0f4afd6](https://github.com/hop-top/poly-kit/commit/0f4afd6a3598a94137070cc1909531d14294dfcd))
* **build:** realign php composer.lock, guard drift in CI ([cd9faed](https://github.com/hop-top/poly-kit/commit/cd9faedfc0337dec4c0c94736680abad9e893838))
* **ci:** match release PRs by base+author, not exact head ref ([9f68eff](https://github.com/hop-top/poly-kit/commit/9f68eff6f48f6e9872e4e53445c1e31f071fa78e))
* **ci:** mirror-sync false-positives on the documented .go/.tmpl rename ([e3a2ca7](https://github.com/hop-top/poly-kit/commit/e3a2ca7e3b7e6ba928fa023790387906bd23d7ba))
* **ci:** promote gate skips PRs that leave prerelease-type untouched ([62f0937](https://github.com/hop-top/poly-kit/commit/62f09372bcaeb945b250750fb4b14cc995899d4b))
* **ci:** repair release promotion gate trigger path and stage compare ([eb1897e](https://github.com/hop-top/poly-kit/commit/eb1897ed8dfbe0084fa2da5fd5683bf02d163784))
* **ci:** repair release promotion gate trigger path and stage compare ([3427c8a](https://github.com/hop-top/poly-kit/commit/3427c8a71877df66ee8f2c7a85abd9deb35fa6ad))
* **cli:** drop lipgloss compat import; resolve adaptive colors lazily ([bb8f2ed](https://github.com/hop-top/poly-kit/commit/bb8f2ed16295173c1d14390b314478ed2dd631fb))
* **cli:** retain sentinel through AsCLIError passthrough ([23f7c81](https://github.com/hop-top/poly-kit/commit/23f7c81ce47025b10665d37c56ee3841ea08505d))
* **cli:** scope netglobals to the go parity contract ([74f6a04](https://github.com/hop-top/poly-kit/commit/74f6a0457339a08ca7233b8a85fc7d65eb84c924))
* **cmdsurface:** run V7 header check before params decode; reject conflicting duplicate headers ([197faa3](https://github.com/hop-top/poly-kit/commit/197faa3033876c7f9d51c6df95e9b69c74f91d67))
* **codeowners:** point release-config ownership at .github/ paths ([25ea3bd](https://github.com/hop-top/poly-kit/commit/25ea3bd111aa92d59a727ba529e98d99bcad9592))
* **config:** normalise string list answers on the resolve-failed path ([4485d69](https://github.com/hop-top/poly-kit/commit/4485d69b7ca3cb6452f596451c5c00344d98ef43))
* **config:** preserve anchors and stop lossy int coercion ([38bd034](https://github.com/hop-top/poly-kit/commit/38bd0342d95675874baf2540bcaae786d9ad750a))
* **config:** preserve scalar types when reading config values ([2a7b137](https://github.com/hop-top/poly-kit/commit/2a7b137575f7cdc6d2f24f4af88f0a83471fa75f))
* **config:** typed scalar writes via SetValue and ParseScalar ([ea8fed9](https://github.com/hop-top/poly-kit/commit/ea8fed95ec53fc78dc201d0f1a6614fa35b954dc))
* **config:** whitelist resolvable scalar tags in Get ([3a61cbe](https://github.com/hop-top/poly-kit/commit/3a61cbe6b36b18268bb6b072f871ce81ea6854cd))
* **config:** write typed values from pkl config wizard ([ecdac5b](https://github.com/hop-top/poly-kit/commit/ecdac5b4c8733c4a2ab09aa4f566a40e5cdead9e))
* **conformance:** align grade client wire contract with svc ([630010f](https://github.com/hop-top/poly-kit/commit/630010f1d258d0d1cf322acf5ab4c9d784dbf674))
* **conformance:** AssertCLI walks signature validation ([7ecd548](https://github.com/hop-top/poly-kit/commit/7ecd548ffbafe252df043bb8705c0bd8a8046bdd))
* **conformance:** consistent symlink path prefix, no silent under-scan on race ([dc9be79](https://github.com/hop-top/poly-kit/commit/dc9be79670e2d1c1aab28eec36cfc050e176e11a))
* **conformance:** decode per-assertion traces in grade client ([f6eb609](https://github.com/hop-top/poly-kit/commit/f6eb609651428bea87f83ae7ef1f398ac37c9a30))
* **conformance:** expand directories passed to verify-no-leak --paths ([555e15f](https://github.com/hop-top/poly-kit/commit/555e15f684223ddfce08e013a3f85a9e42a8da36))
* **conformance:** follow symlinked directory roots in verify-no-leak --paths ([3567f8d](https://github.com/hop-top/poly-kit/commit/3567f8d5fdbb702e844a4a09e7805fde3192f86a))
* **conformance:** follow symlinked directory roots in verify-no-leak --paths ([f225456](https://github.com/hop-top/poly-kit/commit/f2254567c312fafe0fc2f9ef97424578b2e559ab))
* **conformance:** verify-no-leak recurses --paths directories ([dc6d056](https://github.com/hop-top/poly-kit/commit/dc6d056e3f92d2f9781a07cd508e7300c486d945))
* **hooks:** test nested modules from their own dir in pre-push ([189ec65](https://github.com/hop-top/poly-kit/commit/189ec653348bbd038e118c8bbd1acf1d91da87a3))
* **idemstore:** inject clock to deterministically test TTL expiry ([6582ccb](https://github.com/hop-top/poly-kit/commit/6582ccbc93efd69f5e765bd111b4e3b7fdc4c485))
* **init:** allow linked worktrees of bare repos ([02d1714](https://github.com/hop-top/poly-kit/commit/02d1714d5eed023cf4652a18a094fa3b116744ec))
* **init:** non-interactive git-hop under --yes; step-aware abort context ([4a9c906](https://github.com/hop-top/poly-kit/commit/4a9c906d3a38e56b86d56d652882f7de4749706a))
* **init:** point 12fcc next-steps at the real fetch path ([7a25f1a](https://github.com/hop-top/poly-kit/commit/7a25f1a01ae5437abd783f563acb8aba65f320b5))
* **init:** point 12fcc next-steps at the real fetch path ([42c8c52](https://github.com/hop-top/poly-kit/commit/42c8c52235c8e3a15a42b4b9f6af734d4a07d4fb))
* **init:** refuse bare repo ROOT explicitly ([ec0f3c4](https://github.com/hop-top/poly-kit/commit/ec0f3c4fd542f08417fa7123ade2f9d29024a8c5))
* **init:** TestDetect_BareWorktree asserted the wrong mode ([278b6e2](https://github.com/hop-top/poly-kit/commit/278b6e208038eec55207cfc03f7df105a5059537))
* **mcp:** build a fresh server per wire-fixture case ([886d044](https://github.com/hop-top/poly-kit/commit/886d044e7fd97479ddae7ef15ca14062d4c9f1ee))
* **mcpsdk:** pin tasks canary to exact wire behavior ([0ea0d06](https://github.com/hop-top/poly-kit/commit/0ea0d064f42ad9341356cb7ae47a45aa25f69309))
* **output:** honor --cols order in json/yaml ([0f3f6b9](https://github.com/hop-top/poly-kit/commit/0f3f6b965acea5c40a4b4e57ad051b7523d0d6e8))
* **output:** honor --cols order in table/csv/text ([135b351](https://github.com/hop-top/poly-kit/commit/135b351ea591dffa4cae2f14b6b5b45909d1d017))
* **output:** materialise option defaults and unwrap flat formats ([d9ea493](https://github.com/hop-top/poly-kit/commit/d9ea493fec10091e59cf53b83fc01b6d010590bb))
* **output:** preserve CR and LF verbatim in go csv fields ([30d71b1](https://github.com/hop-top/poly-kit/commit/30d71b1fcfa699acc472e1880224732893d9e04a))
* **output:** preserve provenance envelope and validate cols in Render ([190cdf5](https://github.com/hop-top/poly-kit/commit/190cdf531b4caf95ba0fac34961b5dcaf7f94b4b))
* **output:** preserve provenance envelope and validate cols in Render ([f6bbc1f](https://github.com/hop-top/poly-kit/commit/f6bbc1f11a151aad610744c1e3e79e3a7c807423))
* **output:** reject cols that Render/WithCols can't honor instead of leaking or panicking ([3fb6326](https://github.com/hop-top/poly-kit/commit/3fb632640af4430b6d256a8f94904b12cc29afbd))
* **parity:** load verbosity/streams blocks, drop decorative table block ([e94d031](https://github.com/hop-top/poly-kit/commit/e94d031b61a229bb74ac1c6c2d7006aa847f8c4d))
* **peer:** poll mesh discovery instead of fixed sleep ([ac270a8](https://github.com/hop-top/poly-kit/commit/ac270a8dc0cd8199f39f33897ec876204d6451ef))
* **policy:** implement AsCLIError on PolicyDeniedError ([67f0096](https://github.com/hop-top/poly-kit/commit/67f0096668b42119b76fe7d9c5a21f942091f8a0))
* **policy:** implement AsCLIError on PolicyDeniedError ([e5b20bc](https://github.com/hop-top/poly-kit/commit/e5b20bcc3c053eea04231848c932a08ca1b9c551))
* **release:** make promote-release gate-safe and manifest-aware ([bcb5a77](https://github.com/hop-top/poly-kit/commit/bcb5a776c718801732f0472417ef11d03f07d0be))
* **rs:** store kv keys as TEXT for cross-language SQLite access ([08a3d17](https://github.com/hop-top/poly-kit/commit/08a3d17c5d2e0c073d45ec6d39ac9adad7d4973f))
* **tasks:** fail closed when StartTask runs without Attach ([9d8f0ff](https://github.com/hop-top/poly-kit/commit/9d8f0ffd645fc1bd9a1b7af30c46e9a9fb526d53))
* **tasks:** route tasks methods through SDK dispatch ([ee27633](https://github.com/hop-top/poly-kit/commit/ee276336e5bb70a0968d361bf10b3a82c2d6bcf4))
* **tasks:** validate routing headers instead of routing on them ([26d4c86](https://github.com/hop-top/poly-kit/commit/26d4c8662657cf517eaca45c02ce1d144a578909))
* **telemetry:** goimports grouping in sink_https.go ([c38877a](https://github.com/hop-top/poly-kit/commit/c38877ac5defac6a65392491776f89a1dd6082f2))
* **templates:** ci-go self-triggers and races with cgo enabled ([7295ecf](https://github.com/hop-top/poly-kit/commit/7295ecf61f4f29cc1e983f8a4db87c2726754641))
* **templates:** default 12fcc gate paths to repo root, not cmd/ ([499e25e](https://github.com/hop-top/poly-kit/commit/499e25ecdea1e190a823cde2bbc423751066a4b2))
* **templates:** default 12fcc gate paths to repo root, not cmd/ ([5b5e932](https://github.com/hop-top/poly-kit/commit/5b5e932c37d5b961a4e058237c81bde15e6f7aa5))
* **templates:** resolve clap Args name collision in cli-rs hello ([241233a](https://github.com/hop-top/poly-kit/commit/241233a8b791cde0c26209b1e8c16331aa1a5373))
* **ts:** re-export registerOutputFlags + dispatch from output subpath ([89711a2](https://github.com/hop-top/poly-kit/commit/89711a26b71a012930f91d03e03b7a0b5b4948ad))
* **util:** clamp month/year overflow instead of normalising ([301ef67](https://github.com/hop-top/poly-kit/commit/301ef678446e6af50bda00a4c7033d8140e9ad13))
* **util:** clamp month/year overflow instead of normalising ([f3feac7](https://github.com/hop-top/poly-kit/commit/f3feac7cd97caf3bb7aa44753043abd018dd1d51))

## [0.5.0-alpha.1](https://github.com/hop-top/poly-kit/compare/kit/v0.5.0-alpha.0...kit/v0.5.0-alpha.1) (2026-06-19)


### Features

* **storage/httpcache:** caching RoundTripper over kv TTLStore ([7ce6619](https://github.com/hop-top/poly-kit/commit/7ce6619c9f5d027c73f4ed5a84d2d98a1201bae5))

## [0.5.0-alpha.0](https://github.com/hop-top/poly-kit/compare/kit/v0.4.0-alpha.9...kit/v0.5.0-alpha.0) (2026-06-07)


### Bug Fixes

* **ci:** install kit-py dev extras in publish test-command ([#145](https://github.com/hop-top/poly-kit/issues/145)) ([8609b64](https://github.com/hop-top/poly-kit/commit/8609b640d1254d2d0bc5e6e582354ca8684bdc6f))

## [0.4.0-alpha.9](https://github.com/hop-top/poly-kit/compare/kit/v0.4.0-alpha.8...kit/v0.4.0-alpha.9) (2026-06-06)


### Bug Fixes

* **ci:** pass RELEASE_BOT_* secrets to publish-on-tag ([#132](https://github.com/hop-top/poly-kit/issues/132)) ([f911766](https://github.com/hop-top/poly-kit/commit/f911766b1427e9eaae19b491ca6338a220fc7e34))

## [0.4.0-alpha.8](https://github.com/hop-top/poly-kit/compare/kit/v0.4.0-alpha.7...kit/v0.4.0-alpha.8) (2026-06-06)


### Features

* **scaffold:** multi-holder copyright in LICENSE files ([#100](https://github.com/hop-top/poly-kit/issues/100)) ([07bdae7](https://github.com/hop-top/poly-kit/commit/07bdae749040b5b612fd1b7b9a27b668a6e1cd93))


### Bug Fixes

* **ci:** unstick Templates + verify-no-leak-audit workflows ([#131](https://github.com/hop-top/poly-kit/issues/131)) ([62df81a](https://github.com/hop-top/poly-kit/commit/62df81abd42c1839eaaad39f541089ceed553a02))

## [0.4.0-alpha.7](https://github.com/hop-top/poly-kit/compare/kit/v0.4.0-alpha.6...kit/v0.4.0-alpha.7) (2026-06-03)


### Features

* **conformance:** 12FCC badge writer + scaffold seed ([#118](https://github.com/hop-top/poly-kit/issues/118)) ([1bd2254](https://github.com/hop-top/poly-kit/commit/1bd22540f1aaf3052b19b7db35cbdfda7075d6c7))
* **console/cli:** WithFlagValidator persistent-flag middleware ([#116](https://github.com/hop-top/poly-kit/issues/116)) ([cd2a1bd](https://github.com/hop-top/poly-kit/commit/cd2a1bd37be301d85bc1060b49b0f608c5340b33))
* **llm:** pool routing primitives ([#115](https://github.com/hop-top/poly-kit/issues/115)) ([d7c1219](https://github.com/hop-top/poly-kit/commit/d7c121960c0a4925ae0ec6e32f51172471739ba4))
* **scaffold:** add php CI template + composer dependabot ecosystem ([#106](https://github.com/hop-top/poly-kit/issues/106)) ([4728a3e](https://github.com/hop-top/poly-kit/commit/4728a3eb7f908fd24ea413b2555771a47b096b34))
* **scaffold:** add shared gitattributes snippets per lang + common ([#101](https://github.com/hop-top/poly-kit/issues/101)) ([f5d1af5](https://github.com/hop-top/poly-kit/commit/f5d1af5ab8c6c97f872896f17cc31aa8a0a2e0f9))
* **scaffold:** emit per-lang composed .gitattributes with managed-block markers ([#102](https://github.com/hop-top/poly-kit/issues/102)) ([a08eb57](https://github.com/hop-top/poly-kit/commit/a08eb577908d050bfe32315d24a513c086261583))


### Bug Fixes

* **scaffold:** include php in init.sh polyglot lang lists ([#125](https://github.com/hop-top/poly-kit/issues/125)) ([1ad895f](https://github.com/hop-top/poly-kit/commit/1ad895f8eceb99b7602093de9679c90c96872386))
* **scaffold:** move php gitignore to shared mechanism ([#95](https://github.com/hop-top/poly-kit/issues/95)) ([792553c](https://github.com/hop-top/poly-kit/commit/792553c687a04318ff8954852e78483f0848cca3))
* **scaffold:** reconcile per-lang tiers.yaml gitignore mapping ([#96](https://github.com/hop-top/poly-kit/issues/96)) ([376f28a](https://github.com/hop-top/poly-kit/commit/376f28a9c4a4f2ddd756853ffe64a43bbf6c4f4e))
* **scaffold:** remove vestigial .gitignore entry from cli-php tiers.yaml ([#104](https://github.com/hop-top/poly-kit/issues/104)) ([d9f0bc1](https://github.com/hop-top/poly-kit/commit/d9f0bc1c1f6692b6f86b74f2573ebbf314c28a5b))
* **scaffold:** resync templates/ ↔ internal/template/builtins/ mirror drift ([#105](https://github.com/hop-top/poly-kit/issues/105)) ([4e3491f](https://github.com/hop-top/poly-kit/commit/4e3491f6770cd56568a12a94bee76b5d35e9567f))
* **scaffold:** wrap composed .gitignore in kit-managed block ([#98](https://github.com/hop-top/poly-kit/issues/98)) ([dfafdc8](https://github.com/hop-top/poly-kit/commit/dfafdc8aa887b06594febb620b43e3bbf3d1d2c7))
* **templates:** bump golangci-lint pin to v2.12 for Go 1.26+ targets ([#117](https://github.com/hop-top/poly-kit/issues/117)) ([6fa65ad](https://github.com/hop-top/poly-kit/commit/6fa65ad9b2c962037e0576dcca5d80feedc3064a))
* **workspace:** disable pnpm 11 confirmModulesPurge prompt ([#123](https://github.com/hop-top/poly-kit/issues/123)) ([397d2af](https://github.com/hop-top/poly-kit/commit/397d2af77ec0f3e458f9ed0a8dcaa672f36c158a))

## [0.4.0-alpha.6](https://github.com/hop-top/poly-kit/compare/kit/v0.4.0-alpha.5...kit/v0.4.0-alpha.6) (2026-05-26)


### Bug Fixes

* **shape:** exclude reserved verbs from TooManyTopLevelVerbs count ([#93](https://github.com/hop-top/poly-kit/issues/93)) ([a6dfef1](https://github.com/hop-top/poly-kit/commit/a6dfef1acbed97a6a684190d1fdbbe1a4c183a6a))

## [0.4.0-alpha.5](https://github.com/hop-top/poly-kit/compare/kit/v0.4.0-alpha.4...kit/v0.4.0-alpha.5) (2026-05-26)


### Bug Fixes

* **output:** gate header Bold on TableStyle.Header non-nil ([#88](https://github.com/hop-top/poly-kit/issues/88)) ([a79465e](https://github.com/hop-top/poly-kit/commit/a79465ec7ffcb681e4aa7e1b8aa74ae593076225))

## [0.4.0-alpha.4](https://github.com/hop-top/poly-kit/compare/kit/v0.4.0-alpha.3...kit/v0.4.0-alpha.4) (2026-05-24)


### Features

* **contracts:** typeid-v1 cross-language parity fixtures ([ee7ecfb](https://github.com/hop-top/poly-kit/commit/ee7ecfbc7d382095c18090b956d947b145f919ee))
* **go:** kit/core/id - typeid primitive ([bac233d](https://github.com/hop-top/poly-kit/commit/bac233dcbdedc15f968258b17bc6c89564b4fe91))
* **init:** add php & rs templates ([35459b6](https://github.com/hop-top/poly-kit/commit/35459b6e6f586bed3310d5acd5a06f18dd8129e9))
* **init:** generate after-PR hook with liveness probe and tlc follow-up ([#77](https://github.com/hop-top/poly-kit/issues/77)) ([ee4a26c](https://github.com/hop-top/poly-kit/commit/ee4a26c1c5e9112723949d99a0af92a8a5d1306d))
* **init:** generate guarded PR kit bus event workflows ([#78](https://github.com/hop-top/poly-kit/issues/78)) ([46cd80e](https://github.com/hop-top/poly-kit/commit/46cd80ed991afd839128dc6149eb1856071c7531))
* **ts:** kit-sdk/id — typeid primitive ([aff7d71](https://github.com/hop-top/poly-kit/commit/aff7d7138f26949033ebbd596cf605ad950db9ae))


### Bug Fixes

* **console/cli/config:** defer --format to inherited root global ([#80](https://github.com/hop-top/poly-kit/issues/80)) ([07c36d5](https://github.com/hop-top/poly-kit/commit/07c36d5d77db1cb2dc2e6deba91b0a2657d2def6))

## [0.4.0-alpha.3](https://github.com/hop-top/poly-kit/compare/kit/v0.4.0-alpha.2...kit/v0.4.0-alpha.3) (2026-05-20)


### Features

* **cli:** expose KIT_INVOKED_AS via root.InvokedAs() for caller-context-aware config ([#56](https://github.com/hop-top/poly-kit/issues/56)) ([006acfc](https://github.com/hop-top/poly-kit/commit/006acfc9e34f21e21fe5faa705f3d68b3e98fb6b))
* **telemetry:** consenting telemetry stack across kit-go + 4 SDKs ([d7d85dc](https://github.com/hop-top/poly-kit/commit/d7d85dce02e64c4bd6bcc4a424810d2dcc9c8fd6))


### Bug Fixes

* **githooks,sdk/ts:** pre-push gates lint-ts on TS-file changes + declare pnpm 11 allowBuilds (T-0183 unblock) ([#48](https://github.com/hop-top/poly-kit/issues/48)) ([a601885](https://github.com/hop-top/poly-kit/commit/a6018857b78bae7b504f74bee011cfba6b92e483))
* **sdk/php:** rename SemVer pre-release identifier experimental.1 -&gt; alpha.1 (T-0183) ([#49](https://github.com/hop-top/poly-kit/issues/49)) ([0b76224](https://github.com/hop-top/poly-kit/commit/0b76224d2c45f98b08591edc805c106b0c38d4c1))

## [0.4.0-alpha.2](https://github.com/hop-top/poly-kit/compare/kit/v0.4.0-alpha.1...kit/v0.4.0-alpha.2) (2026-05-17)


### Bug Fixes

* **sdk/rs:** gate api_test on api feature + wire Rust into PR CI ([#41](https://github.com/hop-top/poly-kit/issues/41)) ([789b875](https://github.com/hop-top/poly-kit/commit/789b875f63e51349f43aab8224798627a6385e0b))

## [0.4.0-alpha.1](https://github.com/hop-top/poly-kit/compare/kit/v0.4.0-alpha.0...kit/v0.4.0-alpha.1) (2026-05-17)


### Features

* initial public release ([#1](https://github.com/hop-top/poly-kit/issues/1)) ([12569d0](https://github.com/hop-top/poly-kit/commit/12569d0e12bd0ee97fb1cf9ee835b35b5eab0732))


### Bug Fixes

* **ci:** unblock release-please PRs ([#9](https://github.com/hop-top/poly-kit/issues/9)) ([6003668](https://github.com/hop-top/poly-kit/commit/6003668ad33e211281113045b141dc1bfe47d079))

## [0.2.0-alpha.0](https://github.com/hop-top/poly-kit/compare/kit/v0.1.0-alpha.0...kit/v0.2.0-alpha.0) (2026-05-16)


### Features

* initial public release ([#1](https://github.com/hop-top/poly-kit/issues/1)) ([12569d0](https://github.com/hop-top/poly-kit/commit/12569d0e12bd0ee97fb1cf9ee835b35b5eab0732))


### Bug Fixes

* **ci:** unblock release-please PRs ([#9](https://github.com/hop-top/poly-kit/issues/9)) ([6003668](https://github.com/hop-top/poly-kit/commit/6003668ad33e211281113045b141dc1bfe47d079))
