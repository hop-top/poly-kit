# ext

Extension runtime primitives for kit-based tools.

## Packages

- `ext.go`: capability model + shared extension lifecycle interface.
- `manager.go`: capability routing and init/close orchestration.
- `registry/`: in-process registration for `CapRegistry` extensions.
- `hook/`: lifecycle hook bus for `CapHook` extensions.
- `discover/`: PATH scanning and `--ext-info` interrogation for external plugins.
- `dispatch/`: Cobra bridge that mounts discovered plugins as subcommands.
- `config/`: config-backed enable/disable and settings storage.
- `runtime.go`: dependency-aware layer selection, interop graph queries, and lockfile read/write.

## Runtime Layer Model

`runtime.go` adds a deterministic layer runtime used by FIN and future kit adopters:

- `LayerDescriptor`: id/category/availability + required/optional dependencies.
- `LayerRegistry`: defaults, validation, lock resolution, and interop graph.
- `LayerLock`: replayable lockfile artifact with schema version.
- `FeatureFlagProvider`: adapter facade (map/config today, pluggable later).
