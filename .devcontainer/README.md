# Development Environment Setup

This guide covers setting up the development environment for `hop.top/kit`.

## Recommended: VS Code Dev Containers

The easiest way to get started is to use VS Code Dev Containers. The repository includes a pre-configured environment with all necessary tools (Go, Nix, Devbox, pnpm, uv).

1. Install the **Dev Containers** extension in VS Code.
2. Open the project folder in VS Code.
3. Click "Reopen in Container" when prompted.

The setup process uses [Devbox](https://www.jetpack.io/devbox/) and [Nix](https://nixos.org/) to provide a reproducible environment.

### Post-Creation Automation
The `post-create.sh` script runs automatically to:
- Install TypeScript dependencies in `sdk/ts`.
- Create a Python virtual environment and install dependencies in `sdk/py`.
- Install necessary AI tools.

## Manual Setup

If you prefer to set up the environment manually, you will need:

### Core Tools
- **Go**: 1.26+
- **Node.js & pnpm**: For TypeScript SDK.
- **Python 3.9+ & uv**: For Python SDK and engine.
- **Nix & Devbox** (optional, but recommended for parity).

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
