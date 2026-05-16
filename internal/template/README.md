# template

Template engine for kit init. Parses `kit-template.yaml` manifests,
renders directory templates over an `fs.FS`, runs lifecycle hooks
(externally), resolves built-in (embed.FS), `@org/name` (registry
index), git URL, and filesystem template specs.

Reader: `cmd/kit/init` (Track B).
Writers: this package + manifest authors.
