# Changelog

## [0.5.0-alpha.4](https://github.com/hop-top/poly-kit/compare/kit/v0.5.0-alpha.3...kit/v0.5.0-alpha.4) (2026-09-06)


### ⚠ BREAKING CHANGES

* **console:** kit conformance harness record exit codes change; scripts branching on 3, 4 or 5 need updating.
* **conformance:** `hop.top/kit/go/console/cli/conformance/harness` and `.../verifynoleak/...` move to `hop.top/kit/go/conformance/harness` and `hop.top/kit/go/conformance/verifynoleak/...`. Adopters importing the harness toolkit update the import path; no API change. No forwarding aliases — kit ships breaking changes directly on the alpha channel.
* **cli:** the api service refuses to start on a non-loopback address when no delegation policy is configured. Name a `--policy`, listen on loopback, or set `services.api.insecure_no_policy: true` to restore the previous behaviour.
* **kv:** `kv.Open` requires importing the driver package for the chosen backend; sqlite and badger were previously linked in unconditionally.

### Features

* **bridge:** match payloads to manifest accept rules ([5fcabd3](https://github.com/hop-top/poly-kit/commit/5fcabd384b65c623a8f7e892ba7f2eeffe4560da))
* **bridge:** match payloads to manifest accept rules ([af57a67](https://github.com/hop-top/poly-kit/commit/af57a67736c501ed701c5395631e95bee804fb62))
* **engine:** optional If-Match precondition on document PUT ([#280](https://github.com/hop-top/poly-kit/issues/280)) ([4433446](https://github.com/hop-top/poly-kit/commit/443344686d0cbf548c414fad7c9bc45bd72085b6))
* **kv:** add context-carrying opener alongside Opener ([6998bac](https://github.com/hop-top/poly-kit/commit/6998bac9414809b9d9a0dcdcdb8a3d99c024387f))
* **sdk:** port the consenting-telemetry factor to the TS and Python compliance checkers ([#281](https://github.com/hop-top/poly-kit/issues/281)) ([6e205b8](https://github.com/hop-top/poly-kit/commit/6e205b889a365e2e3a648dff60f975d1d9dba0cd))


### Bug Fixes

* **cli:** deny remote serving without a delegation policy ([217f60e](https://github.com/hop-top/poly-kit/commit/217f60ef412c11e73e841749159835db648250d3))
* **console:** resolve four doc-versus-source drifts ([#274](https://github.com/hop-top/poly-kit/issues/274)) ([a6582ff](https://github.com/hop-top/poly-kit/commit/a6582ffe1ac6467d69ce3a4c4c0a25ef892caf53))
* **contracts:** correct buf es out path, drop orphan ts stubs ([#275](https://github.com/hop-top/poly-kit/issues/275)) ([6a8c185](https://github.com/hop-top/poly-kit/commit/6a8c18545a1ad3c2732eb4eecae485e3b30288ec))
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
