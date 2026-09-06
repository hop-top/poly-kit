# ps

## What it answers

Which child processes a hop.top tool has spawned, whether they are still
alive, and how to stop them: one `<tool> ps` subcommand plus the
spawn/observe/stop primitives behind it. Wrong package when the work is
in-process (the bus, `hop.top/kit/go/runtime/bus`) or when you need the
served lifecycle rather than raw children (`hop.top/kit/go/transport`).

## Use it when

- your tool needs the standard `<tool> ps` subcommand → implement `Provider` (`List`) and mount `ps.Command(name, provider, viper)`
- you surface an existing daemon → `ps.EntryFromPIDFile(path)` or `ps.LoadFromPIDDir(dir)`, then `ps.Render(w, entries, format, ...)`
- you start a detached child → `ps.SpawnDetached(ctx, cmd, ps.SpawnOptions{PIDFile: ..., Stdout: ps.StdioFile, StdoutPath: ...})`
- you shut one down → `ps.Stop(entry, grace)`; remove the PID file yourself
- you only need liveness → `ps.IsAlive(pid)`

## Quick start

```go
// Spawn — writes voice.pid, child detached from CLI's process group.
cmd := exec.Command(binPath, args...)
cmd.Stderr = logFile
s, err := ps.SpawnDetached(ctx, cmd, ps.SpawnOptions{
    PIDFile: "/run/myapp/voice.pid",
    Stdout:  ps.StdioFile,
    StdoutPath: "/var/log/myapp/voice.log",
})

// Observe — same pid file the spawn wrote.
entry, _ := ps.EntryFromPIDFile("/run/myapp/voice.pid")
if entry.Status == ps.StatusRunning { /* surface in `myapp ps` */ }

// Supervise — graceful first, hard kill after 2s.
_ = ps.Stop(entry, 2*time.Second)
os.Remove("/run/myapp/voice.pid") // caller policy
```

## Contract

- `WritePIDFile` is atomic (write-then-rename), mode 0600; `StdioFile`
  truncate-opens its paths at mode 0600.
- `SpawnDetached` takes a fully built `*exec.Cmd` and only forces
  `Setpgid=true` on POSIX plus the requested stdio routing.
- `Stop` is SIGTERM, poll, SIGKILL; idempotent; refuses `os.Getpid()`;
  never removes the PID file. On Windows the graceful phase is
  best-effort and SIGKILL maps to `TerminateProcess`.
- `StdioBuffer` is for tests only.
- `<tool> ps` standard columns (ID, Status, Worker, Scope, Duration,
  Progress, optional Worktree and Track) and flags (`--json`, `--all`,
  `--quiet`, `--watch`, `--interval`):
  [Convention](../../../docs/adopters/reference/ps.md#convention).

## Neighbours

- `hop.top/kit/go/console/cli`: the root that `ps.Command` mounts on and
  the viper it reads `--json`/`--quiet` from
- `hop.top/kit/go/console/output`: the table/JSON renderers `Render`
  follows

## See also

- [Process State API reference](../../../docs/adopters/reference/ps.md):
  read-side and write-side API tables, `SpawnDetached` options, `Stop`
  semantics, `<tool> ps` convention
- `example_test.go` in this directory: `Render` in quiet mode and
  `ProgressString`
