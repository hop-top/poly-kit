# ci

Reusable GitHub Actions workflow fragments. Drop into a repo's
`.github/workflows/` directory; the `.tmpl` extension gets rendered by
the kit toolchain (or stripped manually for one-off use).

## Available templates

| File | Language / Platform | Runner | What it builds |
|---|---|---|---|
| `ci-go.yml.tmpl` | Go | ubuntu-latest | `go test -race`, `golangci-lint`, build |
| `ci-ts.yml` | TypeScript / Node | ubuntu-latest | `pnpm test`, lint, build |
| `ci-py.yml` | Python | ubuntu-latest | `pytest`, ruff, type-check |
| `ci-swift.yml.tmpl` | Swift / iOS | macos-14 (Xcode 15.4) | `xcodebuild test` (iPhone + iPad simulators), SwiftLint, no-signing build |
| `ci-kotlin.yml.tmpl` | Kotlin / Android | ubuntu-latest (JDK 17) | `./gradlew test`, `lint`, `ktlintCheck`, `assembleDebug` |

## When to use

- **`ci-swift.yml.tmpl`** — Swift/iOS apps in any repo. Detects either
  `mobile/ios/Ctxt.xcodeproj` (ctxt's monorepo layout) or a top-level
  `*.xcodeproj`. Tests against both iPhone and iPad simulators by
  default; repos can override the matrix.
- **`ci-kotlin.yml.tmpl`** — Kotlin/Android apps in any repo. Detects
  either `mobile/android/gradlew` (ctxt's monorepo layout) or a
  top-level `gradlew`. Caches the Gradle home for fast incremental
  rebuilds.

## Secrets

Both mobile templates run **without code signing** (debug builds and
test runs only). Apple Developer / Play Console signing happens in
separate release workflows that are gated on tagged commits and have
the relevant secrets scoped to them. Keep release and CI workflows
isolated so PR builds never touch production credentials.

## Customising

Each template is a starting point. Common overrides:

- **Different runner image**: change `runs-on: macos-14` to a newer
  macOS runner when adopting newer iOS SDKs.
- **Skip a job**: delete the `lint` or `build` job blocks if your repo
  doesn't need them yet.
- **Different Java version** (Kotlin): edit `java-version: "17"` to
  match your AGP / Gradle requirements.
- **Different test matrix** (Swift): extend the `destination` list in
  the `test` job to cover more simulator devices.

The templates default to ctxt's `mobile/{ios,android}/` monorepo
layout but fall back to top-level project files via shell `if` guards,
so they work for split-repo deployments too.
