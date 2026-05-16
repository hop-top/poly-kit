# kit-template.yaml reference

> Template manifest schema. One file per template root, parsed by the
> Go engine and the standalone `init.sh` to produce identical output.

## Who this is for

Template authors creating a new template under
`internal/template/builtins/` or shipping a standalone template repo
that consumers clone + run via `init.sh`.

## Recommended path

Start by copying an existing manifest (`internal/template/builtins/cli-go/kit-template.yaml`)
and changing `name`, `description`, `variables`, and the
`license.sources` map to match your template's license file naming.

## Top-level fields

| Field | Type | Required | Purpose |
|---|---|---|---|
| `name` | string | yes | Template name; matches the directory name. |
| `description` | string | no | One-line summary shown in `kit template list`. |
| `kit_version` | semver constraint | no | E.g. `">=0.4.0"`. Validated at parse time. |
| `variables` | list | no | User-facing inputs (see Variables). |
| `files` | object | no | Per-file routing rules (see Files). |
| `render_rules` | object | no | Post-render rules (see Render Rules). |
| `hooks` | object | no | Lifecycle scripts (see Hooks). |

## Variables

```yaml
variables:
  - name: Name
    prompt: "Project name (lowercase, hyphens)"
    required: true
    validate: "^[a-z][a-z0-9-]{0,63}$"
  - name: License
    type: choice
    choices: [MIT, Apache-2.0]
    default: MIT
```

| Field | Purpose |
|---|---|
| `name` | Variable name. Templates reference as `{{.Name}}`. |
| `prompt` | Human prompt for interactive mode. |
| `required` | Reject empty values when true. |
| `default` | Initial value (also a Go template string). |
| `validate` | Regex; rejected values re-prompt. |
| `type` | Either `string` (default) or `choice`. |
| `choices` | Required when `type: choice`. |

## Files

```yaml
files:
  exclude:
    - "kit-template.yaml"
    - "tiers.yaml"
  binary:
    - "**/*.png"
```

| Field | Effect |
|---|---|
| `exclude` | Globs matching source paths to drop entirely. |
| `binary` | Globs matching paths to copy without templating. |

## Render Rules

Post-render rules applied identically by both pipelines (Go engine
and bash `init.sh`). Single source of truth — neither implementation
ships with hardcoded fallbacks.

```yaml
render_rules:
  strip_suffixes: [".tmpl"]
  remove_after_render:
    - "kit-template.yaml"
    - "tiers.yaml"
  license:
    var: License
    target: LICENSE
    sources:
      MIT: LICENSE-MIT
      Apache-2.0: LICENSE-Apache-2.0
```

### `strip_suffixes`

Filename suffixes to strip after render. Each entry must start with `.`.
A file ending in any listed suffix is renamed in place
(e.g. `main.go.tmpl` → `main.go`). If the stripped target already
exists, render fails loudly — that signals a template authoring bug.

Empty list = no stripping; every file renders to its source name.

### `remove_after_render`

Project-relative paths to delete after render completes. Use for
manifest leftovers (`kit-template.yaml`, `tiers.yaml`) that should
not ship in the rendered project.

Constraints:
- Relative paths only (no `/etc/...`).
- No `..` segments.
- Missing files are not an error.

### `license`

Picks one source file based on a variable's value, copies it to the
target path, then removes all source files.

| Field | Required | Purpose |
|---|---|---|
| `var` | yes | Variable name to resolve (typically `License`). |
| `target` | yes | Destination path (typically `LICENSE`). |
| `sources` | yes | Map of variable values to source paths. |

If the resolved variable matches no source key, no copy happens and
sources are left in place. If a source file is absent in the rendered
tree (template composition gap; e.g., LICENSE files only ship via
`shared/`), the rule degrades silently.

## Hooks

```yaml
hooks:
  pre_render: ["hooks/pre.sh"]
  post_render: ["hooks/post.sh"]
  post_init: ["hooks/post-init.sh"]
  post_push: ["hooks/post-push.sh"]
```

Lists of script paths (relative to template root) that the
orchestrator runs at the named lifecycle phases. Distinct from
`render_rules.post_render_*` keys — hooks are imperative scripts,
render rules are declarative.

## Validation

`internal/template.Manifest.Validate()` returns
`ErrManifestInvalid` for:

- Empty `name`.
- Malformed `kit_version` semver constraint.
- Variable with `type: choice` and no `choices`.
- Variable `validate` regex that fails to compile.
- `render_rules.strip_suffixes` entry without leading `.`.
- `render_rules.remove_after_render` entry that is absolute or
  contains `..`.
- `render_rules.license` missing `var`, `target`, or `sources`.
- Empty / absolute hook paths.

## Verify the result

After authoring or editing, run:

```sh
kit template lint <path>
```

Or pipe through the Go validator directly:

```go
m, err := template.Parse("kit-template.yaml")
if err == nil {
    err = m.Validate()
}
```

Bash side: `init.sh` exits 1 with a clear message if `render_rules` is
missing or `yq` isn't on PATH.

## Related

- [author-a-template.md](author-a-template.md) — when to
  start from `examples/spaced/` vs. `kit init`.
- [create-cli-project.md](create-cli-project.md) — task: scaffold a
  new CLI from a kit template.
- `internal/template/manifest.go` — Go schema source of truth.
- `internal/template/builtins/shared/init.sh` — bash consumer of the
  same schema via `yq`.
