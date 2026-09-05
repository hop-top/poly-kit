# datasette

Recipe for browsing a kit instance's SQLite state with
[Datasette](https://datasette.io/).

## Contents

- `kit-metadata.json` — Datasette metadata pre-configured for kit's
  table conventions (`documents`, `versions`, `version_parents`,
  `snapshots`). Includes faceting, canned queries, and column
  descriptions.
- `inspect.sh` — convenience script that locates the kit data
  directory and runs `datasette serve --immutable` against it.

## Quick start

```bash
# Install Datasette once
uv tool install datasette        # or: pipx install datasette

# Run against the default kit data directory
./inspect.sh
```

Open <http://localhost:8001>.

## See also

- [Inspect a kit instance with Datasette](../../docs/adopters/guides/inspect-with-datasette.md):
  per-environment metadata overlays, redaction and the `datasette-mask`
  plugin, the trust boundary, copying a live DB, performance notes
