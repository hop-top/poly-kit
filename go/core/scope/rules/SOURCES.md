# Vendored Sources

| File                   | Source                                              | License    |
|------------------------|-----------------------------------------------------|------------|
| gitleaks-paths.toml    | github.com/gitleaks/gitleaks @ v8.30.1 | Apache-2.0 |
| gitleaks-content.toml  | github.com/gitleaks/gitleaks @ v8.30.1 | Apache-2.0 |
| LICENSE                | github.com/gitleaks/gitleaks @ v8.30.1 | Apache-2.0 |

## Pinned Commit

- Tag:    v8.30.1
- SHA:    83d9cd684c87d95d656c1458ef04895a7f1cbd8e
- URL:    https://github.com/gitleaks/gitleaks/tree/83d9cd684c87d95d656c1458ef04895a7f1cbd8e/config/gitleaks.toml

## Refresh

```
make refresh-secret-rules
```

The Makefile target re-runs `tools/vendor-gitleaks` against the latest
tagged release. Same tag → byte-identical output.
