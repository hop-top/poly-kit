# Changelog

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
