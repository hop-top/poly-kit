# Changelog

## [0.5.0-alpha.6](https://github.com/hop-top/poly-kit/compare/kit-rs/v0.5.0-alpha.5...kit-rs/v0.5.0-alpha.6) (2026-10-30)


### Features

* **cmdreflect:** flag choices, variadic args ([464b8cc](https://github.com/hop-top/poly-kit/commit/464b8ccd8c98ad083134b5537a2ae48f34881ef9))

## [0.5.0-alpha.5](https://github.com/hop-top/poly-kit/compare/kit-rs/v0.5.0-alpha.4...kit-rs/v0.5.0-alpha.5) (2026-10-30)

The hop-top team is happy to announce Kit's Rust SDK 0.5.0-alpha.5. This release includes new features and bug fixes.


### Features

* **mcp:** tool_name_separator mount option in rs sdk ([8da95a4](https://github.com/hop-top/poly-kit/commit/8da95a4af8650bac80b0d949d47122b3e658bc1f))
* **rs/mcp:** audit mount hook for refusals ([90218f0](https://github.com/hop-top/poly-kit/commit/90218f0d60853539858b9280e3834d50cc273ce1))
* **rs/mcp:** stdio transport ([461aded](https://github.com/hop-top/poly-kit/commit/461aded88e3bfbdfeba574e8420b5171a361714c))


### Bug Fixes

* **mcp:** no reply to notifications; ping returns {} ([f24c097](https://github.com/hop-top/poly-kit/commit/f24c09796642894a2329a6170a160283a15c38af))

Full diff: [kit-rs/v0.5.0-alpha.4...kit-rs/v0.5.0-alpha.5](https://github.com/hop-top/poly-kit/compare/kit-rs/v0.5.0-alpha.4...kit-rs/v0.5.0-alpha.5)

## [0.5.0-alpha.4](https://github.com/hop-top/poly-kit/compare/kit-rs/v0.5.0-alpha.3...kit-rs/v0.5.0-alpha.4) (2026-10-29)

The hop-top team is happy to announce Kit's Rust SDK 0.5.0-alpha.4. This release includes new features and bug fixes.


### Features

* **rs:** alias proxies with options, validate_alias_name, detect_cycles ([94f0271](https://github.com/hop-top/poly-kit/commit/94f02713c5443231fd220204bddbbea9a7475ad7))


### Bug Fixes

* **rs:** offer a visible alias proxy once in completion ([085c019](https://github.com/hop-top/poly-kit/commit/085c019ba58779685275ef28f0b5cde529425e87))

Full diff: [kit-rs/v0.5.0-alpha.3...kit-rs/v0.5.0-alpha.4](https://github.com/hop-top/poly-kit/compare/kit-rs/v0.5.0-alpha.3...kit-rs/v0.5.0-alpha.4)

## [0.5.0-alpha.3](https://github.com/hop-top/poly-kit/compare/kit-rs/v0.5.0-alpha.2...kit-rs/v0.5.0-alpha.3) (2026-10-29)

The hop-top team is happy to announce Kit's Rust SDK 0.5.0-alpha.3. This release includes new features and bug fixes.


### ⚠ BREAKING CHANGES

* **ts:** TS routellm URI base_url / provider base URL now beats ROUTELLM_BASE_URL and extras routellm.base_url.
* **rs:** kit.ai.request.started no longer carries {"request": ...} and kit.ai.response.received no longer carries {"response": ...}; both are metadata payloads; Request gains extensions
* **rs:** Token gains tool_call and usage and no longer implements Eq; Request gains stream_events
* **rs:** ResolvedConfig gains a public field; a struct literal needs ..ResolvedConfig::default()
* **rs:** Request gains a public field; a Request struct literal needs ..Request::default()
* **rs:** anthropic sends every system message joined instead of only the last
* **rs:** a 403 is LlmError::PermissionDenied, no longer LlmError::Auth; match is_auth() for any auth failure
* **llm:** openai stream caps, truncation error, StreamWithTools
* **llm:** request and response bus events carry no content
* **llm:** anthropic joins every system message
* **llm:** split 403 into permission-denied error
* **deps:** `impl From<mti::MagicTypeIdError> for IdError` removed; mti no longer a dependency. Map mti errors yourself if converting them.

### Features

* **bus:** acknowledged network publish ([31b1931](https://github.com/hop-top/poly-kit/commit/31b193110217b44c715e4ed7a51da76e4cf5f166))
* **bus:** acknowledged network publish ([#564](https://github.com/hop-top/poly-kit/issues/564)) ([31b1931](https://github.com/hop-top/poly-kit/commit/31b193110217b44c715e4ed7a51da76e4cf5f166))
* **llm:** anthropic joins every system message ([42dc6d1](https://github.com/hop-top/poly-kit/commit/42dc6d187ac0d50b1d9fa91510257f7607b1b11e))
* **llm:** gemini StreamWithTools with tool-call and usage stream events ([d9dacad](https://github.com/hop-top/poly-kit/commit/d9dacad263b20945a27fedbf66605b6b6d10e319))
* **llm:** hermetic resolve option; document sync secret store ([25be0dc](https://github.com/hop-top/poly-kit/commit/25be0dca999152360d8c93c191ea7c66fa9dbe30))
* **llm:** openai stream caps, truncation error, StreamWithTools ([a72eaf2](https://github.com/hop-top/poly-kit/commit/a72eaf2493d4404b45e623401385894d4896f367))
* **llm:** request and response bus events carry no content ([0a5bef9](https://github.com/hop-top/poly-kit/commit/0a5bef95b7e27992aeafee63fa972b28c2217f42))
* **llm:** Request.ToolChoice across adapters ([ad40094](https://github.com/hop-top/poly-kit/commit/ad400941b866ae9055389ed7048b1aff453a887b))
* **llm:** split 403 into permission-denied error ([76bc153](https://github.com/hop-top/poly-kit/commit/76bc153836277e6f14f94bbdcbf0b557d3b92a50))
* **llm:** stream tool-call and usage events on opt-in ([5a0824e](https://github.com/hop-top/poly-kit/commit/5a0824e33c9763746ddd8eb683c738c92111b32e))
* **llm:** validate and reversibly sanitize tool names in adapters ([f8314ff](https://github.com/hop-top/poly-kit/commit/f8314ffd76627080f9e246fc2fb12e4116e3d21e))
* **llm:** validate and reversibly sanitize tool names in adapters ([9fd7b53](https://github.com/hop-top/poly-kit/commit/9fd7b53fb1155f3f7042dc5c973e1e47d4b56fb4))
* **llm:** validate and reversibly sanitize tool names in adapters ([#577](https://github.com/hop-top/poly-kit/issues/577)) ([f8314ff](https://github.com/hop-top/poly-kit/commit/f8314ffd76627080f9e246fc2fb12e4116e3d21e))
* **redact:** distinctive rules skip keyword gate and entropy in Apply ([f68ccb1](https://github.com/hop-top/poly-kit/commit/f68ccb1101e7e358232fdada47a135a14b2c8762))
* **redact:** single-pass spans and gitleaks rule semantics ([7f17a86](https://github.com/hop-top/poly-kit/commit/7f17a86105ab3bfaf5c282e42f5784c3f1c21eff))
* **rs:** acknowledged publish, dialer side ([f1fc50b](https://github.com/hop-top/poly-kit/commit/f1fc50b6a163dee99a3955275b34cf6e4356d008))
* **rs:** anthropic joins every system message ([5531be3](https://github.com/hop-top/poly-kit/commit/5531be3b4a4f9752eee38bebb561f0dc4cd2eca4))
* **rs:** async subscribe with bounded pool and draining close ([6a54a9a](https://github.com/hop-top/poly-kit/commit/6a54a9aeaffd4e081e848a2ed2d4a1a0f7aea2c6))
* **rs:** bus adapter seam, MemoryAdapter and composed DynBus ([61e14eb](https://github.com/hop-top/poly-kit/commit/61e14eb3fb200f7762a8f248a8a70705a52421ef))
* **rs:** bus context and draining close ([0261f4a](https://github.com/hop-top/poly-kit/commit/0261f4ac8ebcb8d6a40eee5d382012b893d74294))
* **rs:** bus enforcement mode from string, env and config ([894c4b2](https://github.com/hop-top/poly-kit/commit/894c4b2c0218f51da1d23ad77a93dd302f84768b))
* **rs:** bus network accept handler with coder/websocket checks and axum adapter ([60d00e3](https://github.com/hop-top/poly-kit/commit/60d00e3910883483140c012ac75d9445116358b9))
* **rs:** bus network adapter transport core ([6d5d88b](https://github.com/hop-top/poly-kit/commit/6d5d88b8f1e7b30e99403a4285e0e9a6511dbe38))
* **rs:** bus network link events, error callback and TLS client config ([9615b89](https://github.com/hop-top/poly-kit/commit/9615b890582184aa7a38a747fa073ef8ab92ea71))
* **rs:** bus sinks, TeeBus and the KIT_BUS_SINK env switch ([daf4142](https://github.com/hop-top/poly-kit/commit/daf4142e22fc66dd877585256fec0acba61d335a))
* **rs:** bus stream client core, sans I/O ([c96f7df](https://github.com/hop-top/poly-kit/commit/c96f7df75b2384b192f6828390fb2a319669207f))
* **rs:** circuit breaker ([519f9d9](https://github.com/hop-top/poly-kit/commit/519f9d943c505d1650f240f31341c8994933136a))
* **rs:** cloudevents 1.0 shaping for bus events ([251a278](https://github.com/hop-top/poly-kit/commit/251a278d4d30d92993a926f94560899264457fa4))
* **rs:** completion fig and completion usage spec exports ([8a4dba5](https://github.com/hop-top/poly-kit/commit/8a4dba574e99c57f799ba201a785a4fa6ab21c4a))
* **rs:** config bus getter ([2fe53d3](https://github.com/hop-top/poly-kit/commit/2fe53d3b4f2880afab72b8afa008ae0e9bdd644c))
* **rs:** config core ([8ba3a01](https://github.com/hop-top/poly-kit/commit/8ba3a019b686a9936ecb6a9c88891f886026639c))
* **rs:** config getter ([80a4d25](https://github.com/hop-top/poly-kit/commit/80a4d250aff88889fbc1ebb2c8c2ce436e2a971a))
* **rs:** config paths ([6091008](https://github.com/hop-top/poly-kit/commit/6091008a9e340dc2bd9eee95fc01a7ae9e2cd6cb))
* **rs:** config pkl ([9553578](https://github.com/hop-top/poly-kit/commit/9553578ceb7e06cd04d0eacee01d5496264ccb3d))
* **rs:** config reload ([5f9a070](https://github.com/hop-top/poly-kit/commit/5f9a0703e4e14877f8446235a74517402e974622))
* **rs:** config set and unset ([0dfde93](https://github.com/hop-top/poly-kit/commit/0dfde93ec491d7351f3ab69ee320ad29f27372e5))
* **rs:** domain state machine, port of the Go runtime/domain one ([dac4065](https://github.com/hop-top/poly-kit/commit/dac406537447321c875269b785026b3f52e0a299))
* **rs:** event envelope parity ([2b14f2e](https://github.com/hop-top/poly-kit/commit/2b14f2ea30e032c2a147d3ce4fad90b543450623))
* **rs:** extended thinking and prompt caching ([b10e15c](https://github.com/hop-top/poly-kit/commit/b10e15c85a4e4999710885a4b26551f481948705))
* **rs:** JSON document migrator ([df575be](https://github.com/hop-top/poly-kit/commit/df575be396e28b498a0917f921454e2f987b2e15))
* **rs:** llm async secret store ([8070c60](https://github.com/hop-top/poly-kit/commit/8070c6088fcf4d7aeb62c8eec8c49ca7b2f26599))
* **rs:** llm core and Anthropic adapter ([7930bf3](https://github.com/hop-top/poly-kit/commit/7930bf3d78cee593c41bfef5d4bee8568f188b67))
* **rs:** llm hermetic mode ([8f2d384](https://github.com/hop-top/poly-kit/commit/8f2d38426cf11e7ed7ad603c1ec08e94f37994b0))
* **rs:** llm permission-denied error for 403 ([dbeec12](https://github.com/hop-top/poly-kit/commit/dbeec1253c6de784f96a42f020def7c18e1b8c71))
* **rs:** llm redaction transforms for bus content opt-in ([6ce2646](https://github.com/hop-top/poly-kit/commit/6ce2646ac2fda069a3af466501281df26c35cc2e))
* **rs:** llm request and response bus events carry no content ([e59f157](https://github.com/hop-top/poly-kit/commit/e59f157a4e9c00ed37a7419328b09bba4ee0d011))
* **rs:** llm Request.tool_choice ([08538a5](https://github.com/hop-top/poly-kit/commit/08538a582c6a0404c31ce00909fc8e1cf430735f))
* **rs:** llm stream tool-call and usage events on opt-in ([45282dd](https://github.com/hop-top/poly-kit/commit/45282dd3b271aabd9d079730343b4e9eeda66b3a))
* **rs:** per-capability consent grants ([41872ea](https://github.com/hop-top/poly-kit/commit/41872ead16ff45a3367ca942f9cb6ccef83abdef))
* **rs:** redact with gitleaks rule semantics ([a48293a](https://github.com/hop-top/poly-kit/commit/a48293a1687c01e83ee585571c9ff63dd09359ba))
* **rs:** retry and backoff policy ([a5559ec](https://github.com/hop-top/poly-kit/commit/a5559ec68851664bf4c6d6f596c7ed0d77fa8760))
* **rs:** shell completion for the cli feature ([19ceeab](https://github.com/hop-top/poly-kit/commit/19ceeabeed2212ae4e1f8cc267897bac3c0fb66f))
* **rs:** stream event frames on the cloudevents core ([d2dbc67](https://github.com/hop-top/poly-kit/commit/d2dbc67e5791afbd09ea6deb591128b5689829ec))
* **rs:** topic builder and parse ([5ea741a](https://github.com/hop-top/poly-kit/commit/5ea741aaf580f69a2a3a9eb0c2a8b4acf937bd06))
* **rs:** upgrade parity with Go ([facae7d](https://github.com/hop-top/poly-kit/commit/facae7d8005926ec54fcb63f2d56226bffc820e3))
* **rs:** validate and reversibly sanitize llm tool names ([def5410](https://github.com/hop-top/poly-kit/commit/def54100b580369aa5824bb38a3a3a8dd5babd57))
* **rs:** validate and reversibly sanitize llm tool names ([16d8e1c](https://github.com/hop-top/poly-kit/commit/16d8e1c84293aef34a7be2a3ae745c7e0787eb85))
* **rs:** validate and reversibly sanitize llm tool names ([#581](https://github.com/hop-top/poly-kit/issues/581)) ([def5410](https://github.com/hop-top/poly-kit/commit/def54100b580369aa5824bb38a3a3a8dd5babd57))
* **rs:** versioned state migrations ([71c4417](https://github.com/hop-top/poly-kit/commit/71c44173856d85c341b2479e9a31662134fae52a))


### Bug Fixes

* **cli:** kit/args grammar shared with cmdreflect; completion-spec index links ([2874f3f](https://github.com/hop-top/poly-kit/commit/2874f3f9cfdc77e16ac623a031a9ed3a17ae8c6b))
* **deps:** bump kit-rs rustls 0.23.43 -&gt; 0.23.46 ([17d4c2a](https://github.com/hop-top/poly-kit/commit/17d4c2a27232e1ce426614a43d2fb643a39d764d))
* **deps:** kit-rs typeid on type-safe-id, drops block-buffer 0.10 ([c211f2f](https://github.com/hop-top/poly-kit/commit/c211f2fcb9090147b8905f0f44adf86224816247))
* **deps:** rust lockfile advisories ([eaf9d09](https://github.com/hop-top/poly-kit/commit/eaf9d095049092e2969f5a46468f2f3d58398769))
* **deps:** tokio 1.54.1 in kit-rs lock ([4ee13e6](https://github.com/hop-top/poly-kit/commit/4ee13e6c2186d080fdb687d113095ac26aeb9021))
* **domain:** strict statemachine-v1 runners, wasm smoke test in CI ([0c16609](https://github.com/hop-top/poly-kit/commit/0c16609c2cbc418d90447f2ad5b972fcc693ac0c))
* **llm:** guard every credential by endpoint origin ([b9076c1](https://github.com/hop-top/poly-kit/commit/b9076c1eccd124743ad48e285e4663fcc70d1519))
* **llm:** harden anthropic streams, bodies and redirects ([64c801d](https://github.com/hop-top/poly-kit/commit/64c801ddf97c99d109255d88fceaf4d2b2ee0f38))
* **llm:** parse endpoints with the client's URL parser ([27a6589](https://github.com/hop-top/poly-kit/commit/27a658902402bb29b4688db211042e1039ed61f7))
* **llm:** redact tool-call arguments on decoded JSON strings ([792ad67](https://github.com/hop-top/poly-kit/commit/792ad67dd0e4fb773e51706cbcf9fa2457d5948c))
* **llm:** send anthropic media urls as url sources; media_fetch vectors ([c1600dd](https://github.com/hop-top/poly-kit/commit/c1600dd6ad41a40e10fac7d08b644abbbc8af727))
* **llm:** send empty input schema for parameterless tools ([733c9a6](https://github.com/hop-top/poly-kit/commit/733c9a6e96024da5db4cf5a3c106bf755a0fdee6))
* **rs:** attached-only flag values need require_equals in completion specs ([2d97fa3](https://github.com/hop-top/poly-kit/commit/2d97fa3e954b71cbd86d618816dd1065750881c2))
* **rs:** bus network redial backoff, tracked accept sessions and review minors ([df3e181](https://github.com/hop-top/poly-kit/commit/df3e181bafdc7567265f20b81b992d4dd1bdd1d1))
* **rs:** bus network relay loops, echoes, orphaned links and stalled upgrades ([8a2f721](https://github.com/hop-top/poly-kit/commit/8a2f7218712cf6761b264829f91453c7755fe693))
* **rs:** bus publish ignores context like go; go close semantics ([4c29a4a](https://github.com/hop-top/poly-kit/commit/4c29a4aa8a79f0964bcf609e2d1085f6dc54db72))
* **rs:** bus, handlers and reporters are Send + Sync ([4a34a76](https://github.com/hop-top/poly-kit/commit/4a34a76eccdb84ce903af12988cccc5eb7b68dc4))
* **rs:** completion descriptions keep control characters raw ([d50e148](https://github.com/hop-top/poly-kit/commit/d50e148d60172e22c65c7013187b1137a59e89e3))
* **rs:** config dir resolution matches Go on macOS ([063ed73](https://github.com/hop-top/poly-kit/commit/063ed734e18cdb1c71bab7ae8202b4666adc30dc))
* **rs:** config edits refuse instead of dropping comments ([0560201](https://github.com/hop-top/poly-kit/commit/056020154e978f80e23f126e95b472ff2c5414b8))
* **rs:** config signal readiness, reload off the executor, path cleaning ([5758755](https://github.com/hop-top/poly-kit/commit/57587554c84a276428f75946d3710ab3adc04ac9))
* **rs:** config yaml decode bounded and read like yaml.v3 ([08e7233](https://github.com/hop-top/poly-kit/commit/08e723383227e272912c08e08c16689fb6c4b714))
* **rs:** deep alias shim and built-in completion match Go ([c948dc0](https://github.com/hop-top/poly-kit/commit/c948dc0154a6834fe651de33cd978c03e7c00e26))
* **rs:** deny-wins in legacy telemetry consent ([096bbee](https://github.com/hop-top/poly-kit/commit/096bbee5ff63127ea57bdf67cadabe3dd1cc5e4a))
* **rs:** env sink reporter test, byte-faithful env values ([5b99e64](https://github.com/hop-top/poly-kit/commit/5b99e6477b6217207c3b99d6da32894410e91720))
* **rs:** event clock seam; no system clock read on wasm32 ([045c80f](https://github.com/hop-top/poly-kit/commit/045c80f719cb4ccd921d70d1d25bcd08e327b36a))
* **rs:** fail-closed key context and member order in llm argument redaction ([5cc4d8e](https://github.com/hop-top/poly-kit/commit/5cc4d8e0fa98d5a8fa0b8326b48c5b244d2689a0))
* **rs:** gate macOS-only xdg_paths test helpers off other targets ([b7c7334](https://github.com/hop-top/poly-kit/commit/b7c7334f990a26ef413a16a91c22bd231f89fe9d))
* **rs:** gate the telemetry netpolicy test on its feature ([63ede72](https://github.com/hop-top/poly-kit/commit/63ede72f074d4d1c2397291abdf407dcdefa3e56))
* **rs:** go-compatible quoting and mode parse shape ([81e75db](https://github.com/hop-top/poly-kit/commit/81e75db27c710ffacb09400725964b3a29dcaa48))
* **rs:** keep comments in legacy telemetry consent writes ([5bebd08](https://github.com/hop-top/poly-kit/commit/5bebd086cb75d041e2071f6d9716376764b2c7cf))
* **rs:** keep provider keys off untrusted endpoints, bound retry-after ([42f99ff](https://github.com/hop-top/poly-kit/commit/42f99fffc825a597fdd06d53cdf01dcee04c8db5))
* **rs:** kit-frame offsets as go reads them, kitoffset reserved on events ([9aa900e](https://github.com/hop-top/poly-kit/commit/9aa900e70d19e9f45d43ae360818b2a4ab526513))
* **rs:** linear long-line repair, quote context, merge-key duplicates ([82d4d15](https://github.com/hop-top/poly-kit/commit/82d4d159c733d7fe7d1850fdaea694ce92cb5ab3))
* **rs:** linear yaml repair, tab continuations, bom-aware edits ([3c018b2](https://github.com/hop-top/poly-kit/commit/3c018b28ad894844f244d2361254c21586bbf14a))
* **rs:** match Go on bracketed hosts in netpolicy ([8e77aae](https://github.com/hop-top/poly-kit/commit/8e77aae66a2b190a43f366e1e2bde4adfb09afdc))
* **rs:** mcp preserve_order premise test only under output ([b06a52c](https://github.com/hop-top/poly-kit/commit/b06a52c2351a13f9b85844b633d8fba4a3b41962))
* **rs:** never lose a redial to a reconnect loop on its way out ([2172543](https://github.com/hop-top/poly-kit/commit/2172543b84936295d33c7079fe3ef1d02b4287f6))
* **rs:** old-location records migrate once and only deny ([26005e5](https://github.com/hop-top/poly-kit/commit/26005e51b02900a6adaa422a8a91e177abd37b56))
* **rs:** one redial state machine for every reconnect entry point ([a503934](https://github.com/hop-top/poly-kit/commit/a503934f7090ddbc41c1a7bce2daca760b30d0c0))
* **rs:** private pkl amendment file ([2e7d482](https://github.com/hop-top/poly-kit/commit/2e7d4825faafe3f6f5f7c2be2e02b5449e694246))
* **rs:** private registry file and redacted source token ([804cb62](https://github.com/hop-top/poly-kit/commit/804cb62cb3855a1273ca738ce7ba15c7e0bc7b50))
* **rs:** process-wide handshake ids, disconnect stops the link ([5ce0eed](https://github.com/hop-top/poly-kit/commit/5ce0eed731a1bdfef61c5c6f1b7ca0b94586c119))
* **rs:** publish unknown audit events under their own topic ([56817ba](https://github.com/hop-top/poly-kit/commit/56817ba53aeeef8247473ecca164ed120c168747))
* **rs:** reject unencodable timestamp offsets with the shared error text ([95a289d](https://github.com/hop-top/poly-kit/commit/95a289d8f4c61db540c30963542cef5ff60f38f0))
* **rs:** retry error text and runner strictness ([f462f1b](https://github.com/hop-top/poly-kit/commit/f462f1ba6bdabade6bba58eb65b983acc883bafb))
* **rs:** SharedBus drops removed handlers after releasing its lock ([5febee1](https://github.com/hop-top/poly-kit/commit/5febee1809f8f3d6626a9916da151a486c9bb039))
* **rs:** stream client disconnect, dial ids, write-failure close ([a5106c8](https://github.com/hop-top/poly-kit/commit/a5106c8c0fa5ace3d4070b661a552d1d8a8daee0))
* **rs:** unsubscribe always drains, context edge parity ([9f66b6d](https://github.com/hop-top/poly-kit/commit/9f66b6d81372f16ca37ba790d4536fc434868ee2))
* **rs:** usage bin cannot close its tera raw block ([c8ba437](https://github.com/hop-top/poly-kit/commit/c8ba4374993d1d2cabc720997b5979d0ea8fe4d6))
* **rs:** wake a reconnect loop's backoff wait for user requests ([9fe490a](https://github.com/hop-top/poly-kit/commit/9fe490affd594d928994220a3780c5c478e907e3))
* **ts:** routellm base URL precedence matches Go ([16e9cb6](https://github.com/hop-top/poly-kit/commit/16e9cb68283d98090722d3c9109d7f43c64fc7c6))

Full diff: [kit-rs/v0.5.0-alpha.2...kit-rs/v0.5.0-alpha.3](https://github.com/hop-top/poly-kit/compare/kit-rs/v0.5.0-alpha.2...kit-rs/v0.5.0-alpha.3)

## [0.5.0-alpha.2](https://github.com/hop-top/poly-kit/compare/kit-rs/v0.5.0-alpha.1...kit-rs/v0.5.0-alpha.2) (2026-09-05)

The hop-top team is happy to announce Kit's Rust SDK 0.5.0-alpha.2. This release includes new features.


### Features

* **rs:** EXIT_GENERIC and CliError::generic for the exit-1 class
* **rs:** Go-style duration parser for serve budgets
* **rs:** mount serve as a clap command
* **rs:** mount serve as a clap command
* **rs:** serve command with --list, --enable/--disable and timeout flags
* **rs:** serve run path from ArgMatches to exit code
* **rs:** serve supervisor and service lifecycle
* **rs:** serve supervisor and service lifecycle
* **sdk:** EXIT_GENERIC and generic error constructor in all SDKs

Full diff: [kit-rs/v0.5.0-alpha.1...kit-rs/v0.5.0-alpha.2](https://github.com/hop-top/poly-kit/compare/kit-rs/v0.5.0-alpha.1...kit-rs/v0.5.0-alpha.2)

## [0.5.0-alpha.1](https://github.com/hop-top/poly-kit/compare/kit-rs/v0.5.0-alpha.0...kit-rs/v0.5.0-alpha.1) (2026-09-04)

The hop-top team is happy to announce Kit's Rust SDK 0.5.0-alpha.1. This release includes new features and bug fixes.


### ⚠ BREAKING CHANGES

* **ai/llm:** `Request.Temperature` type changes from `float64` to `*float64`. Callers setting a literal must pass a pointer; zero-value construction (unset) keeps behaving as before via nil.
* **rs:** `telemetry::ClientOptions` gains a `policy` field, so struct-literal construction must add it (`..Default::default()` is unaffected). `api::ApiClient::request` returns `netpolicy::RequestBuilder` instead of `reqwest::RequestBuilder`.

### Features

* **httpcache:** port response cache to rust behind feature gate
* merge offline-transport
* merge offline-transport
* **output:** add rs csv + text formatters
* **rs/sqldb:** configurable WAL retry budget on Options
* **rs:** add kv store with sqlite backend
* **rs:** add sqldb primitive
* **rs:** add sqlstore typed kv store over sqldb
* **rs:** enforce `--offline` at the reqwest construction path
* **rs:** port blob local backend behind `blob` feature
* **rs:** port bus core behind `bus` feature
* **rs:** port dual-spec MCP surface behind an `mcp` feature
* **rs:** port relative-date parsing behind timeutil feature
* **rs:** port relative-date parsing behind timeutil feature
* **sdk/rs:** add structured error envelope with transience class


### Bug Fixes

* **ai/llm:** send explicit zero temperature on the wire
* **ci:** mirror-sync false-positives on the documented .go/.tmpl rename
* **output:** decide table emptiness by row count, not column count
* **output:** enable serde_json preserve_order in rs
* **output:** enforce header == key on ColumnSpec construction
* **output:** give rs --template an ordered-column affordance
* **output:** preserve CR and LF verbatim in rs csv fields
* **output:** thread ColumnSpec order through to formatters
* **rs/blob:** narrow stored blob mode to 0640
* **rs:** clippy io_other_error in tests/error.rs, fmt lib.rs
* **rs:** correct restore_from_blob doc comment, no rename fix needed
* **rs:** exempt telemetry from `--offline`
* **rs:** match Go exactly on bare week phrases
* **rs:** match Go exactly on bare week phrases
* **rs:** reflect lazily-attached leaf flags on long-lived mounts
* **rs:** reject empty and leading-slash keys in blob local resolve
* **rs:** reject every key spelling that resolves to the blob store root
* **rs:** retry rename-over-existing on restore for Windows parity
* **rs:** store kv keys as TEXT for cross-language SQLite access
* **sqldb:** tolerate concurrent first-open WAL conversion

Full diff: [kit-rs/v0.5.0-alpha.0...kit-rs/v0.5.0-alpha.1](https://github.com/hop-top/poly-kit/compare/kit-rs/v0.5.0-alpha.0...kit-rs/v0.5.0-alpha.1)

## [Unreleased]

### Changed

- **`serde_json`'s `preserve_order` feature is now enabled.** This is the
  headline change and it reaches further than the output layer: it swaps the
  backing store of *every* `serde_json::Map` in the build graph from
  `BTreeMap` to `indexmap`, so JSON object keys serialize in insertion order
  instead of alphabetically. **Any consumer that parses, re-serializes or
  diffs JSON through this crate sees different bytes**, not only callers of
  the output formatters. The feature is additive within a Cargo build graph,
  so it also takes effect for any other crate sharing the same `serde_json`
  compilation. It is set on the dev-dependency as well as the optional one,
  so the test build and the shipped build agree about `Map`'s backing store.
  `indexmap` becomes a hard transitive dependency; it was already present via
  `serde_yaml`, and `Cargo.lock` gains one line. Audited before flipping:
  nothing in the crate hashes, signs or golden-compares serialized JSON
  (`sha2` runs only over raw random bytes), and the cross-language cassettes
  canonicalize with sorted keys, so they are unaffected.
- **Output column ordering is now driven by the `ColumnSpec` list.**
  Previously `DispatchOptions::columns` was consumed only to validate
  `--cols` and then dropped, so every formatter fell back to payload key
  order — which, under the old `BTreeMap`, meant **alphabetical**. A caller
  asking for `ID DUE TYPE PRIORITY CATEGORY TITLE` got
  `CATEGORY DUE ID PRIORITY TITLE TYPE` and no `ColumnSpec` list could say
  otherwise. The list now supplies the default column order, the header
  labels, and the column selection. **User-visible:** output that previously
  came out alphabetically now follows the caller's `ColumnSpec` order, and
  payload keys absent from the schema stop being emitted. Anyone parsing
  column positions or diffing `--format json` output sees different bytes.
  Precedence is `--cols` (user order always wins; it reorders as well as
  selects), else `ColumnSpec` order, else payload key order. `table`, `json`
  and `yaml` follow the same rule.
- `Formatter::render()` is **unchanged**. `resolve_effective_cols`
  (`dispatch.rs:161`) collapses `--cols` and the `ColumnSpec` list into one
  ordered list once, at the dispatch layer, and passes it through the
  existing `cols` parameter — so third-party `Formatter` implementations keep
  compiling and pick up correct ordering with no code change. The collapse is
  sound only because `header == key`; a split would have forced `ColumnSpec`
  values through to every formatter.
- **Zero rows emits nothing** from the `table` formatter — not even a bare
  header row. Emptiness is now decided by row count rather than column count.
  This fixes a live bug: `--cols` on an empty payload previously printed a
  bare header row.
- **`ColumnSpec::new` now panics when `header != key`.** The two name the
  same column, so validation and row lookup are one operation on one name. Go
  cannot express a header/key split through its `table:""` struct tags, so no
  SDK may. `priority` is still accepted and stored but remains ignored
  outside Go.

### Known limitations

- `csv` and `text` formatters are not implemented in this SDK. Only `table`,
  `json` and `yaml` are portable across all five kit runtimes, so callers
  cannot assume `--format csv` exists.
- The `--template` path has no ordered-column affordance: the minimal `{key}`
  substituter receives the row but not the schema. Since `--template` and
  `--cols` are mutually exclusive, the `ColumnSpec` list is the only ordering
  signal on that path, and Rust exposes none of it. Go (`.Cols`), Python and
  TS (`cols`) expose an iterable column list; PHP has `{*}`. The spelling for
  Rust is an open decision.

### Fixed

- **CSV fields containing CR or LF are now preserved verbatim.** With
  `crlf` set, the encoder previously DROPPED a lone carriage return and
  rewrote an embedded LF to CRLF inside the quoted field — both silent,
  both irrecoverable, and the CR drop applied on the `quote-all` path too.
  A field holding CR and/or LF is quoted and its bytes pass through
  untouched in both line-ending modes and both quoting paths; `crlf` now
  changes the record terminator and nothing else. RFC 4180 lists `CR` and
  `LF` as separate alternatives inside the `escaped` production, so a bare
  CR between quotes is legal, and W3C CSV on the Web states that line
  endings within escaped cells are not normalised.
  **User-visible:** `--format csv --format-opt crlf` output for values
  containing `\r` changes, and such values now survive a decode round-trip.
- **Leading-whitespace quoting matches the documented rule.** The check was
  `starts_with(' ')`, which left a leading TAB, vertical tab or NBSP
  unquoted; it is now a unicode whitespace test on the first character, as
  the module doc claimed. A field equal to `\.` is also quoted, since that
  sequence alone on a line terminates a PostgreSQL `COPY` stream.

## [0.5.0-alpha.0](https://github.com/hop-top/poly-kit/compare/kit-rs/v0.4.0-experimental.3...kit-rs/v0.5.0-alpha.0) (2026-06-07)


### Refactored

* migrate uri+hdl to cite across Go/TS/Py/Rs/PHP ([#129](https://github.com/hop-top/poly-kit/issues/129)) ([105fbe3](https://github.com/hop-top/poly-kit/commit/105fbe3eefb0e92a4c313479791a3af0477c3cd5))

## [0.4.0-experimental.3](https://github.com/hop-top/poly-kit/compare/kit-rs/v0.4.0-experimental.2...kit-rs/v0.4.0-experimental.3) (2026-05-21)

The hop-top team is happy to announce Kit's Rust SDK 0.4.0-experimental.3. This release includes new features.


### Features

* **contracts:** typeid-v1 cross-language parity fixtures
* **init:** add php & rs templates
* **rs:** kit-sdk/id — typeid primitive
* **telemetry:** consenting telemetry stack across kit-go + 4 SDKs

Full diff: [kit-rs/v0.4.0-experimental.2...kit-rs/v0.4.0-experimental.3](https://github.com/hop-top/poly-kit/compare/kit-rs/v0.4.0-experimental.2...kit-rs/v0.4.0-experimental.3)

## [0.4.0-experimental.2](https://github.com/hop-top/poly-kit/compare/kit-rs/v0.4.0-experimental.1...kit-rs/v0.4.0-experimental.2) (2026-05-17)

The hop-top team is happy to announce Kit's Rust SDK 0.4.0-experimental.2. This release includes maintenance release with bug fixes.


### Bug Fixes

* **sdk/rs:** gate api_test on api feature + wire Rust into PR CI

Full diff: [kit-rs/v0.4.0-experimental.1...kit-rs/v0.4.0-experimental.2](https://github.com/hop-top/poly-kit/compare/kit-rs/v0.4.0-experimental.1...kit-rs/v0.4.0-experimental.2)

## [0.4.0-experimental.1](https://github.com/hop-top/poly-kit/compare/kit-rs/v0.4.0-experimental.0...kit-rs/v0.4.0-experimental.1) (2026-05-17)

The hop-top team is happy to announce Kit's Rust SDK 0.4.0-experimental.1. This release includes new features.


### Features

* initial public release

Full diff: [kit-rs/v0.4.0-experimental.0...kit-rs/v0.4.0-experimental.1](https://github.com/hop-top/poly-kit/compare/kit-rs/v0.4.0-experimental.0...kit-rs/v0.4.0-experimental.1)

## [0.2.0-experimental.0](https://github.com/hop-top/poly-kit/compare/sdk/rs/v0.1.0-experimental.0...sdk/rs/v0.2.0-experimental.0) (2026-05-16)

The hop-top team is happy to announce kit 0.2.0-experimental.0. This release includes new features.


### Features

* initial public release

Full diff: [sdk/rs/v0.1.0-experimental.0...sdk/rs/v0.2.0-experimental.0](https://github.com/hop-top/poly-kit/compare/sdk/rs/v0.1.0-experimental.0...sdk/rs/v0.2.0-experimental.0)
