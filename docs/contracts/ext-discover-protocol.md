# ext-discover Protocol

> PATH-based external plugin contract for kit-based tools.
> Authority: [`go/ai/ext/discover/discover.go`](../../go/ai/ext/discover/discover.go).

A sidecar binary becomes a discoverable kit extension by:

1. Sitting on `$PATH` with a name that starts with the host tool's prefix
   (e.g. `kit-foo`, `tlc-bar`).
2. Having its executable bit set.
3. Implementing the `--ext-info` interrogation flag.
4. Running its main work when invoked with no arguments.

The host tool's discovery layer scans configured directories (or
`$PATH` when unset), filters by prefix, deduplicates by basename
(first occurrence wins), and skips non-executables. Each match is
exposed as a `Found` extension with capability `CapDiscover`.

## Phases

A sidecar's lifecycle has three observable phases. Only the
**Interrogate** phase is rigorously specified; **Init** and
**Shutdown** are intentionally permissive so adopters can fold the
protocol over existing CLIs without re-architecting.

### 1. Interrogate

**Trigger:** the host calls `discover.Interrogate(path)` (typically
during enrichment after `Scan()`).

**Invocation:**

```
<binary> --ext-info
```

- argv\[0\]: the binary path
- argv\[1\]: literal `--ext-info`
- env: inherited from host (no extras injected)
- stdin: closed
- stdout: **must** contain a single JSON object — see schema below
- stderr: free-form; ignored by the host
- exit code: `0` on success; non-zero signals "no metadata available"
  and the host falls back to synthesized metadata (name from filename,
  empty version)

**Response schema:**

```json
{
  "name":         "string  (required)",
  "version":      "string  (required, semver recommended)",
  "description":  "string  (optional, one-line)",
  "capabilities": ["string", "..."]
}
```

`capabilities` is currently informational; the host always treats a
discovered binary as `CapDiscover`. Adopters may surface other
capability names ("registry", "hook", "config") as a hint to
operators.

**Example response:**

```json
{
  "name": "scrape",
  "version": "0.4.1",
  "description": "Fetch one URL and emit JSON",
  "capabilities": ["discover"]
}
```

**Timeout:** the host enforces a 5-second deadline (see
`interrogateTimeout` in discover.go). A binary that exceeds this
budget is treated as failed; the host logs the error and falls back
to synthesized metadata.

### 2. Init (run)

**Trigger:** the host calls `Found.Init(ctx)` to actually execute
the sidecar's main work.

**Invocation:**

```
<binary>
```

- argv: just the binary path (no extra args injected by the host)
- env: inherited from host
- stdin: inherited
- stdout: inherited
- stderr: inherited
- exit code: propagated to the host as the return value of
  `Init`. `0` is success; anything else surfaces as an `*exec.ExitError`.

The context passed to `Init` controls cancellation; cancelling it
sends SIGKILL via `exec.CommandContext`. Sidecars that need a graceful
shutdown should trap SIGTERM themselves (the host doesn't downgrade
SIGKILL).

The host does not pass arguments through; sidecars that need
parameters should read them from environment variables or from a
config file path the host advertises out of band.

### 3. Shutdown

There is no explicit shutdown phase. `Found.Close()` is a no-op for
external plugin binaries — once `Init` returns, the sidecar's
process is gone. Side effects (open files, network connections,
spawned children) are the sidecar's responsibility to clean up
before exit.

## Exit-code conventions

| Code | Meaning                                            |
|------|----------------------------------------------------|
| `0`  | Success                                            |
| `1`  | Generic failure (host treats as `*exec.ExitError`) |
| `2`  | Convention: usage error                            |

The host does not branch on specific non-zero codes; all non-zero
exits are surfaced as `*exec.ExitError`.

## Timeout / cancellation semantics

| Phase       | Timeout      | Cancellation  |
|-------------|--------------|---------------|
| Interrogate | 5s (fixed)   | host context  |
| Init        | none         | host context (SIGKILL) |
| Shutdown    | n/a          | n/a           |

Sidecar authors that need a graceful shutdown for long-running work
should catch SIGTERM and exit promptly; otherwise the host will
SIGKILL on context cancellation.

## Worked example: Python sidecar

```python
#!/usr/bin/env python3
"""Minimal kit-prefixed sidecar in Python."""

import json
import sys


def main() -> int:
    if len(sys.argv) >= 2 and sys.argv[1] == "--ext-info":
        json.dump(
            {
                "name": "scrape",
                "version": "0.1.0",
                "description": "Fetch a URL and print body",
                "capabilities": ["discover"],
            },
            sys.stdout,
        )
        return 0

    # Real work here.
    print("scraping...")
    return 0


if __name__ == "__main__":
    sys.exit(main())
```

Save as `kit-scrape`, `chmod +x`, drop on `$PATH`. The host
discovers it as extension `scrape`.

## Worked example: TypeScript sidecar

```ts
#!/usr/bin/env node
// Minimal kit-prefixed sidecar in TypeScript (compiled to JS).

const argv = process.argv.slice(2);

if (argv[0] === "--ext-info") {
  process.stdout.write(
    JSON.stringify({
      name: "scrape",
      version: "0.1.0",
      description: "Fetch a URL and print body",
      capabilities: ["discover"],
    }),
  );
  process.exit(0);
}

// Real work here.
console.log("scraping...");
process.exit(0);
```

Compile, save the resulting JS as `kit-scrape` with a Node shebang,
`chmod +x`, drop on `$PATH`.

## See also

- [`go/ai/ext/discover/discover.go`](../../go/ai/ext/discover/discover.go)
  — Go reference implementation of the host side.
- [`go/ai/ext/ext.go`](../../go/ai/ext/ext.go) — `Extension` /
  `Capability` / `Metadata` types.
- [extending-kit.md](../workflows/extending-kit.md) — broader extension model
  (registry / hook / discover / config).
