# templates

scaffolding and conformance tools for kit projects.

## Tools

- [scaffold.sh](scaffold.sh): create new project from templates.
- [conform.sh](conform.sh): bring existing repo to kit standards.

## Shared

- [lib.sh](lib.sh): shared utilities (sourced by scaffold + conform).
- [shared/](shared/README.md): common infrastructure (CI, docs, scripts).

## Blueprints

- [cli-go/](cli-go/README.md): Go CLI template.
- [cli-py/](cli-py/README.md): Python CLI template.
- [cli-ts/](cli-ts/README.md): TypeScript CLI template.

## Tests

- [test-lib.sh](test-lib.sh): unit tests for lib.sh.
- [test-conform-e2e.sh](test-conform-e2e.sh): e2e tests for conform.sh.
- [test-scaffold-e2e.sh](test-scaffold-e2e.sh): e2e tests for scaffold.sh.
