# ps

Process state monitoring and management for hop.top CLI tools.

The package gives every adopter a consistent `<tool> ps` subcommand
plus the supervisory primitives needed to spawn, watch, and stop the
child processes that subcommand reports on.

## Read side — observe processes

Use these when a process exists and you want to surface it in `ps`
output, the ID-table renderer, or a status check.

| API                               | Purpose                                |
|-----------------------------------|----------------------------------------|
| `EntryFromPIDFile(path)`          | One PID file → one `Entry`             |
| `LoadFromPIDDir(dir)`             | Glob `*.pid` in dir → `[]Entry`        |
| `IsAlive(pid)`                    | Signal-0 liveness probe                |
| `Render(w, entries, format, ...)` | Table / JSON / quiet output            |
| `Command(name, provider, viper)`  | Wires a Cobra `ps` subcommand          |
| `Provider`                        | Interface a tool implements: `List`    |

## Write side — supervise processes

Counterparts to the read side. Use these when your tool spawns a
long-running child (a daemon, a backend service, a worker pool)
that other invocations of the same tool will later observe via
`Entry`.

| API                            | Purpose                                       |
|--------------------------------|-----------------------------------------------|
| `WritePIDFile(path, entry)`    | Atomic write-then-rename; mode 0600           |
| `SpawnDetached(ctx, cmd, opts)`| Detached child + stdio + PID file in one call |
| `Stop(entry, grace)`           | SIGTERM → poll → SIGKILL escalator, idempotent|
| `IsAlive(pid)`                 | Same probe used by the read side              |

### Typical flow

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

### `SpawnDetached` options

`SpawnDetached` accepts a fully built `*exec.Cmd` rather than wrapping
`exec.Command` itself. This keeps `os/exec`'s entire surface (env,
working dir, ExtraFiles, custom `SysProcAttr` bits) available to
adopters; the package only forces `Setpgid=true` on POSIX and the
requested stdio routing on top.

| `StdioMode`     | Effect                                              |
|-----------------|-----------------------------------------------------|
| `StdioInherit`  | Default. Leaves `cmd.Stdout/Stderr` as the caller   |
|                 | configured them.                                    |
| `StdioDiscard`  | Pipe the stream to `io.Discard`.                    |
| `StdioFile`     | Truncate-open `StdoutPath`/`StderrPath` (mode 0600).|
| `StdioBuffer`   | In-memory `bytes.Buffer`. **Tests only**.           |

### `Stop` semantics

`Stop` is idempotent: empty / unparseable entries are no-ops, dead pids
return `nil`, and re-calling on an already-stopped target does nothing.
It refuses to act on `os.Getpid()` so a misconfigured caller cannot
kill the host process. `Stop` does **not** remove a PID file — that
policy belongs to the caller.

On Windows, the graceful signal phase is best-effort (the Go runtime
does not deliver SIGTERM to non-self processes). The SIGKILL phase
maps to `TerminateProcess`, which the OS does support.

## Convention

`<tool> ps` is the standard subcommand for every hop.top tool that
manages asynchronous or long-running work. Standard columns: ID,
Status (colored), Worker, Scope (truncated 40ch), Duration (since
started), Progress (`done/total (pct%)`). Optional Worktree and Track
columns appear only when at least one entry populates them.

Standard flags: `--json`, `--all`/`-a`, `--quiet`/`-q`,
`--watch`/`-w`, `--interval`/`-i` (default 5s).
