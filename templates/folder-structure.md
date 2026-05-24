# The Hardened Base: Universal Repository Architecture

This document defines a language-agnostic, "self-aligning" repository architecture designed for maximum automation, deterministic quality, and seamless collaboration between humans and AI agents.

---

## 1. Expanded Repository Structure

```text
.
├── .config/                    # Tool-specific configurations (Opinionated Linters)
│   ├── black.toml              # Python: psf/black config
│   ├── pint.json               # PHP: laravel/pint config
│   ├── rufo.ruby               # Ruby: rufo config
│   └── gofumpt.toml            # Go: gofumpt config
├── .devcontainer/              # Environment-as-Code (Deterministic Env)
│   ├── devcontainer.json       # VS Code / Codespaces orchestration
│   └── Dockerfile              # Base image definition with mise/tools pre-installed
├── .github/                    # CI/CD Orchestration & Platform Governance
│   ├── ISSUE_TEMPLATE/         # Structured inputs
│   │   ├── bug_report.md
│   │   └── feature_request.md
│   ├── workflows/              # Automation pipelines
│   │   ├── ci.yml              # Test, Lint, Type-check on every PR
│   │   ├── security.yml        # SAST, Secret scanning, Dependency audit
│   │   └── release.yml         # Automated versioning & changelog (Semantic Release)
│   ├── CODEOWNERS              # Automatic reviewer assignment by path
│   ├── dependabot.yml          # Automated dependency update schedule
│   ├── FUNDING.yml             # Sponsorship/Donation configuration
│   └── PULL_REQUEST_TEMPLATE.md
├── .githooks/                  # Enforced local automation (git config core.hooksPath)
│   ├── pre-commit              # Runs local lint/format before allow commit
│   ├── commit-msg              # Enforces Conventional Commits
│   └── post-merge              # Auto-runs 'mise install' on branch updates
├── bin/                        # Universal CLI wrappers (The "Dashboard")
│   ├── setup                   # One-touch project bootstrap
│   ├── build                   # Language-agnostic build trigger
│   └── ship                    # Release preparation script
├── docs/                       # The Knowledge Graph (Source of Truth)
│   ├── decisions/              # ADR-0001-slug.md (Architectural Decision Records)
│   ├── manuals/                # Operational guidance
│   │   ├── user.md             # End-user documentation
│   │   ├── dev.md              # Contributor/Internal documentation
│   │   └── agent.md            # LLM/AI Agent specific context & tool usage
│   ├── personas/               # PERS-001-slug.md (User/Target Audience profiles)
│   └── stories/                # US-0001-slug.md (User Stories / Requirements)
├── scripts/                    # The "Hands" (Utilities and Glue)
│   ├── db/                     # Migrations, seeding, and backups
│   └── deploy/                 # Environment-specific deployment logic
├── src/ or packages/           # Domain Logic (Polyglot units or single src)
├── tests/                      # The "Proof" (Validation layers)
│   ├── unit/                   # Fast, isolated logic tests
│   ├── integration/            # Boundary/Service tests
│   └── e2e/                    # User-flow verification (Playwright/Cypress)
├── .cspell.json                # Spell-check rules (Prevents typo-bugs/agent hallucinations)
├── .editorconfig               # Fundamental white-space & charset alignment
├── .env.example                # Template for required secrets/env vars
├── .gitattributes              # Git hygiene (LF vs CRLF, binary handling)
├── .gitignore                  # Exclusion map
├── .mailmap                    # Git history identity mapping (merges contributor aliases)
├── .markdownlint.yaml          # Documentation style enforcement
├── .prettierrc                 # Deterministic UI/Web formatting
├── .yamllint                   # YAML structure & syntax validation
├── AGENTS.md                   # Agentic Context: Tech stack, commands, and rules
├── AUTHORS                     # List of project contributors
├── CODE_OF_CONDUCT.md          # Social contract & community standards
├── CONTRIBUTING.md             # Rules of Engagement (How to help)
├── docker-compose.yml          # Orchestration for local deps (DBs, Caches)
├── LICENSE                     # Intellectual Property / Usage Rights
├── Makefile                    # The Universal Interface (make test, make lint)
├── mise.toml                   # Polyglot tool management (Versions & Tasks)
├── README.md                   # The Map (Onboarding & High-level overview)
├── SECURITY.md                 # Vulnerability reporting process
└── VERSION                     # Root source of truth for current release version
```

---

## 2. Core Pillars of the Architecture

### I. Environmental Determinism (`mise` & `.devcontainer`)
*   **`mise.toml`**: The foundation. It manages language versions (Node, Python, Go, etc.) and environment-specific variables. It ensures that every developer and agent is using the exact same toolset.
*   **`.devcontainer`**: The ultimate "no-excuses" setup. It encapsulates the OS dependencies, tools, and extensions so the project works instantly in a containerized environment.

### II. Agentic Alignment (`AGENTS.md`)
*   **Renamed from `CLAUDE.md`**: This is a generalized manual for any AI agent.
*   **Content**: It must define the **Tech Stack**, **Testing Patterns**, **Naming Conventions**, and **Safe Zones** (where the agent can edit) vs. **Critical Zones** (where the agent must ask for permission).

### III. Intellectual Continuity (`docs/decisions/`)
*   **ADRs (Architectural Decision Records)**: Every major change is recorded as an ADR. This prevents "regressive engineering" where a future developer removes a critical piece of logic because they didn't understand the original constraint.

### IV. The Quality Loop (`.githooks` & `.github/workflows`)
*   **Local Enforcement**: `.githooks` prevent broken code from leaving the machine.
*   **Remote Verification**: GitHub Workflows verify that code passes linting, tests, and security scans before it can be merged.
*   **Commit Linting**: Conventional Commits (via Husky/Githooks) enable automated versioning via the root `VERSION` file and `CHANGELOG.md`.

### V. Operational Consistency (`Makefile` & `bin/`)
*   **The Interface**: No matter how many languages are in the repo, the commands are always the same:
    *   `make setup`: Prepare the environment.
    *   `make lint`: Run all formatters and linters.
    *   `make test`: Run all test suites.
    *   `make ship`: Release a new version.

---

## 3. Best Practices for "Self-Alignment"

1.  **Format on Save / Commit**: Use `Prettier`, `Black`, `Pint`, and `Rufo` with zero-tolerance. Style is not a choice; it is a repository attribute.
2.  **Context-Rich Pull Requests**: Use Issue and PR templates to force the provider (human or agent) to explain the "Why" and link to the relevant User Story.
3.  **Automated Dependency Updates**: Use `dependabot` or `renovate` to keep the foundation fresh and secure without manual intervention.
4.  **Spell-Checking**: Use `cspell` to ensure that variable names and documentation are professional and consistent. This catches AI-generated "nonsense" variables before they enter the codebase.
5.  **Linear History**: Enforce squash-merges or rebase-merges to keep a clean, readable git history for future archaeology.

---

## 4. What `scaffold.sh` / `kit init` Emits

Spec:
`.tlc/tracks/scaffold-emits-mise-toml-devcontainer-compose/spec.md`.

### `mise.toml` (project root) — SOT for tool versions

```
<project>/
└── mise.toml          # kit-managed [tools], [env], [tasks.install]
```

Pinned by the central manifest
`templates/shared/tool-versions.toml`. Every developer, CI workflow,
and devcontainer reads the same version table. `mise run install` is
the single contributor entry point — it fans out to `go mod
download`, `pnpm install`, `uv sync`, `cargo fetch` per the selected
languages.

Refresh with `kit init --update`. User-added tools above the
`# >>> kit-managed >>>` marker are preserved verbatim.

### `.devcontainer/` — compose-mode, no Dockerfile

```
<project>/.devcontainer/
├── devcontainer.json   # compose-mode; mise feature installs the toolchain
├── docker-compose.yml  # devcontainer + kit-managed telemetry + opted-in
└── otel-config.yaml    # entirely kit-managed
```

No `Dockerfile` by default. The dev image is
`mcr.microsoft.com/devcontainers/base:debian` with the
`ghcr.io/jdx/mise/features/mise:1` feature; the toolchain comes from
`mise.toml`. Add a Dockerfile only if you need OS packages.

`docker-compose.yml` ships two labeled managed blocks:

- `# kit-managed: telemetry` — `otel-collector` + `jaeger`
  (forwarded ports `4318`, `16686`).
- `# kit-managed: opted-in services` — populated by
  `kit init --add-service <name>` from the `--services` catalog
  (`postgres`, `redis`, `minio`, `mailpit`, `redpanda`).

Service data persists in `.data/<service>/` bind mounts at the
project root (gitignored).

Jaeger UI: <http://localhost:16686>.

### `.env.example` — five kit-managed blocks

```
<project>/.env.example
├── # >>> kit-managed: telemetry >>>
├── # >>> kit-managed: storage   >>>
├── # >>> kit-managed: queue     >>>
├── # >>> kit-managed: log       >>>
└── # >>> kit-managed: config    >>>
```

One block per kit adapter domain. Defaults: `KIT_QUEUE_DRIVER=sqlite`,
local-XDG storage, JSON logs at `info`. Opt-in services flip the
relevant variables — e.g. `--add-service redis` sets
`KIT_QUEUE_DRIVER=redis` and uncomments `KIT_QUEUE_REDIS_URL`.

### `templates/shared/` — emitter library + service catalog

```
templates/shared/
├── tool-versions.toml          # SOT consumed by emit-mise.sh
├── managed-block.sh            # idempotent marker-delimited block writer
├── emit-mise.sh                # mise.toml emitter
├── emit-devcontainer-json.sh   # devcontainer.json emitter (compose-mode)
├── emit-docker-compose.sh      # docker-compose.yml + otel-config.yaml
├── emit-env-example.sh         # .env.example with five managed blocks
├── apply-services.sh           # --services catalog applier
├── services/
│   ├── postgres.yml            # compose snippet, bind-mounted to .data/ at project root
│   ├── redis.yml
│   ├── minio.yml
│   ├── mailpit.yml
│   ├── redpanda.yml
│   └── env/                    # matching .env snippets per service
└── README.md                   # API reference for every script above
```

`scaffold.sh` sources these emitters; `kit init` embeds the same set
(see below) so generated projects can refresh themselves without
poly-kit checked out.

### `cmd/kit/init/managed_assets/` — embedded bash mirror

The kit Go binary embeds (via `go:embed`) a mirror of
`templates/shared/` under `cmd/kit/init/managed_assets/`:

```
cmd/kit/init/managed_assets/
├── managed-block.sh
├── emit-mise.sh
├── emit-devcontainer-json.sh
├── emit-docker-compose.sh
├── emit-env-example.sh
├── apply-services.sh
├── tool-versions.toml
└── services/                   # full catalog mirror
```

A pre-commit hook keeps the two directories byte-identical. Running
`kit init` against an existing project unpacks these assets to a
tmpdir and invokes them — so users can refresh managed blocks with
just the `kit` binary on `$PATH`, no clone required.

### CI workflows — mise-action

`.github/workflows/*.yml` no longer pins toolchain versions in YAML.
Every workflow uses:

```yaml
- uses: actions/checkout@v4
- uses: jdx/mise-action@v2
  with: { install: true, cache: true }
- run: mise run install
```

No `actions/setup-go`, `actions/setup-node`, `actions/setup-python`,
or `dtolnay/rust-toolchain`. Version bumps land in
`tool-versions.toml`, propagate via `kit init --update`.