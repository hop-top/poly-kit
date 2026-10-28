# Development Environment Setup

This guide covers setting up the development environment for `hop.top/kit`.

## Recommended: VS Code Dev Containers

The easiest way to get started is to use VS Code Dev Containers. The repository includes a pre-configured environment with all necessary tools (Go, golangci-lint, Node.js, pnpm, Python, ruff, eslint, go-task, git, gh).

1. Install the **Dev Containers** extension in VS Code.
2. Open the project folder in VS Code.
3. Click "Reopen in Container" when prompted.

The toolchains come from the [Nix](https://nixos.org/) flake in this
directory (`flake.nix`, `flake.lock`). The Dockerfile builds the flake's
`packages.default` profile in a `nixos/nix` stage and copies it, with its
whole store closure, into a Debian image at `/usr/local/tools`. To build
the image outside VS Code (the context is this directory):

```bash
docker build -t kit-dev .devcontainer
```

The same flake backs `nix develop ./.devcontainer` and the
[Devbox](https://www.jetpack.io/devbox/) shell (`devbox.json`).

### Post-Creation Automation
The `post-create.sh` script runs automatically to:
- Install TypeScript dependencies in `sdk/ts`.
- Create a Python virtual environment and install dependencies in `sdk/py`.
- Install necessary AI tools.

## Manual Setup

If you prefer to set up the environment manually, install
[mise](https://mise.jdx.dev) and run `mise trust && mise install` at the
repo root. `mise.toml` pins Go, Node.js, pnpm, Python, uv, Rust and the
linters at the versions CI uses; see
[CONTRIBUTING.md](../CONTRIBUTING.md#toolchain-versions). PHP (for the
experimental PHP SDK) is not in mise: install the version
`sdk/experimental/php/composer.json` requires.

### Initializing Dependencies

Run the following command from the root to initialize all sub-projects:

```bash
make setup
```

This will:
1. Initialize Go modules.
2. Install TypeScript dependencies (`sdk/ts`).
3. Sync Python environments (`sdk/py` and `engine/sdk/py-kit-engine`).

## Verification

Run the full suite of linters and tests to ensure everything is working correctly:

```bash
make check
```

For language-specific tests:
- `make test-go`
- `make test-ts`
- `make test-py`
