# contributors

Documentation for changing kit itself: adding packages, changing behaviour, cutting releases. Building an app on kit instead: [`../adopters/`](../adopters/README.md).

## Contents

| Path | What it is | Start here when |
|------|------------|-----------------|
| [`contributing.md`](contributing.md) | dev setup, PR flow, conventions, testing, signed commits, branch policy | your first change to the repo |
| [`readme-guide.md`](readme-guide.md) | the two folder README shapes, which directories need one, caps, `make lint-readmes` allowlist | you add or rewrite a directory README |
| [`releasing.md`](releasing.md) | release-please flow, version-bump policy, BREAKING-change protocol, per-release notes | you prepare or cut a release |
| [`shared-template-blueprints.md`](shared-template-blueprints.md) | `templates/shared/`: version SOT, gitignore/gitattributes composition, managed-block library, emitters, services catalog | you change what a scaffold writes into a project |
| [`architecture/`](architecture/README.md) | layering, dependency rules, package map, internal coupling; start at `architecture.md` | you need to know where new code belongs |
| [`../contracts/`](../contracts/README.md) | wire-level contracts: event topics, ext-discover protocol, kit init PR wiring, serve lifecycle | your change touches a cross-process boundary |
