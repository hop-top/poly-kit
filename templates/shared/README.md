# shared

Common infrastructure blueprints: the version SOT, the managed-block
writer, the per-artifact emitters and the services catalog.
`templates/scaffold.sh` sources these; `kit init` embeds a byte-identical
mirror under `cmd/kit/init/managed_assets/`.

## Contents

| Path | What it is | Start here when |
|------|------------|-----------------|
| [`tool-versions.toml`](tool-versions.toml) | single source of truth for every tool version emitted into mise, CI and the devcontainer | you bump a runtime or a workflow tool |
| [`managed-block.sh`](managed-block.sh) | idempotent writer for marker-delimited blocks in TOML, YAML, `.env`, shell and JSON-C | an emitter must refresh a section without clobbering user content |
| [`emit-mise.sh`](emit-mise.sh) | writes the `mise.toml` managed block from the SOT | you change what lands in a project's mise config |
| [`emit-devcontainer-json.sh`](emit-devcontainer-json.sh) | writes `.devcontainer/devcontainer.json` (no source directory), per-language extensions | you change the devcontainer surface |
| [`emit-docker-compose.sh`](emit-docker-compose.sh) | writes `docker-compose.yml` service blocks | you change the compose stack |
| [`emit-env-example.sh`](emit-env-example.sh) | writes `.env.example` from the selected services | you change which env keys a scaffold documents |
| [`apply-services.sh`](apply-services.sh) | applies the opt-in `--services` selection across the emitted artifacts | you wire a new service end to end |
| [`services/`](services/) | curated `--services` catalog: postgres, redis, minio, mailpit, redpanda | you add or change a catalog entry |
| [`gitignore/`](gitignore/) | `common.gitignore` plus per-language snippets, composed at scaffold time | you change what a scaffolded project ignores |
| [`gitattributes/`](gitattributes/) | `common.gitattributes` plus per-language snippets, composed at scaffold time | you change a scaffolded project's attributes |
| [`ci/`](ci/README.md) | drop-in CI workflow blueprints | you change a scaffolded project's CI |
| [`docs/`](docs/README.md) | documentation blueprints copied into a scaffold | you change the docs a scaffold ships with |
| [`scripts/`](scripts/README.md) | helper scripts copied into a scaffold | you change a scaffolded project's scripts |
| [`tiers.yaml`](tiers.yaml) | tier filtering for the composed gitignore and gitattributes | you change which tier a shared file lands in |

## Conventions

Every emitted artifact is wrapped in a `kit-managed` marker block so a
re-scaffold or `kit init --update` never touches user-owned content
outside the markers. Per-language `tiers.yaml` files must not list
`.gitignore` or `.gitattributes`: composition for both is governed by
`shared/tiers.yaml` alone.

## See also

- [Shared template blueprints](../../docs/contributors/shared-template-blueprints.md):
  SOT format and update policy, composition order, the managed-block API
  and idempotency guarantees, every emitter's API and output, the
  services catalog layout and `KIT_QUEUE_DRIVER` precedence
