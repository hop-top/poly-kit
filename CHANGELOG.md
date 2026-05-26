# Changelog

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
