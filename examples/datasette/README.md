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

## Customizing per environment

Copy `kit-metadata.json` to your environment-specific overlay and
adjust the masking rules to match your redact policy:

```bash
cp kit-metadata.json ~/.config/kit/datasette-prod.json
# edit masking patterns
datasette serve /var/lib/kit/data.db \
  --immutable \
  --metadata ~/.config/kit/datasette-prod.json
```

## Trust boundary

Datasette has no notion of kit's redact policies by default. The
shipped metadata applies coarse pattern-based masking through the
`datasette-mask` plugin, but **never expose Datasette publicly**
without an auth layer in front of it.
