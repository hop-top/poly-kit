# Changelog

## [0.4.0-alpha.10](https://github.com/hop-top/poly-kit/compare/kit/v0.4.0-alpha.9...kit/v0.4.0-alpha.10) (2026-06-07)


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
