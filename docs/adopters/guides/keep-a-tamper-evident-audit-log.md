# Keep a tamper-evident audit log

> Audience: Go adopters who need records that can be proven unedited
> after the fact (admin actions, served-command trails, release
> decisions). Package contract:
> [`go/security` README](../../../go/security/README.md). For the
> served-command trail wired through configuration, start at
> [Keep a tamper-evident trail](secure-remote-serving.md#keep-a-tamper-evident-trail);
> this page covers the library underneath it.

`security.AuditLog` is an append-only JSON Lines file in which each
record carries the SHA-256 of the one before it. An edit, deletion,
reorder or insertion breaks the chain at that record, and
`security.VerifyAuditLog` reports the first break.

## Prerequisites

- Go module that imports `hop.top/kit/go/security`.
- A directory the tool can write (created with mode 0700 if absent).
- A place to keep the log's head hash that the log's writer cannot
  reach (see "What verification cannot see").

## 1. Open and append

```go
log, err := security.OpenAuditLog(path, security.AuditLogOptions{
    Sync:        security.SyncAlways,
    MaxBytes:    4 << 20,
    MaxSegments: 8,
})
if err != nil {
    return err
}
defer log.Close()

entry, err := log.Append([]byte(`{"path":"widget purge","caller":"bob"}`))
// entry.Seq is the record's position (from 1); entry.Hash its SHA-256.
```

Rules for `Append`:

- The payload must be valid JSON, stored verbatim under `rec`.
  Embedded newlines are compacted away.
- Redact before you append. The log keeps what you give it for good;
  see [`go/core/redact`](../../../go/core/redact/README.md).
- Safe for concurrent use; appends are serialized.
- After `Close`, `Append` returns `security.ErrAuditLogClosed`.

A record on disk is one line:

```json
{"v":1,"seq":2,"at":"2026-09-28T10:00:00Z","prev":"<hex>","rec":{...},"hash":"<hex>"}
```

## 2. Pick a durability mode

| `Sync` | A record survives | Cost |
|--------|-------------------|------|
| `SyncNever` (zero value) | Process crash | None beyond hashing |
| `SyncPeriodic` | Power loss, up to one `SyncInterval` (default 1s) | Background goroutine |
| `SyncAlways` | Power loss | One fsync per record |

The log always syncs on rotation and on `Close`.

## 3. Rotate and retain

- `MaxBytes > 0` moves the active file aside as
  `<path>.<first seq, 20 digits>` before an append would pass the
  limit. The chain continues across files.
- `MaxSegments > 0` keeps that many rotated files and deletes the
  older ones after each rotation. Verification then starts at the
  oldest kept record; `AuditReport.FirstSeq` is above 1.

## 4. One writer at a time

A second `OpenAuditLog` on the same path, from any process, fails
with `security.ErrAuditLogBusy` (advisory lock on `<path>.lock`).
Two writers would fork the chain, so the second is refused. A path
holding a non-chain file fails with `security.ErrNotAuditLog`.

If the writer died mid-line, the next open moves the unfinished line
to `<path>.torn-<unix-nanos>` and resumes from the last complete
record.

## 5. Verify

```go
rep, err := security.VerifyAuditLog(path)
if err != nil {
    return err // log unreadable; a missing log wraps os.ErrNotExist
}
if !rep.OK() {
    // rep.Break.File, .Line, .Seq, .Reason name the first bad record
    return fmt.Errorf("%s (code %s, exit %d)", rep.Break,
        security.CodeTamperDetected, security.ExitTamperDetected)
}
```

A log that was read and does not hold is a nil error with `rep.Break`
set; the error return is only for a log that could not be read.
`AuditReport` also carries `Files`, `Records`, `FirstSeq`, `Head` and
`TornTail` (an unfinished last line: reported, not a break). Editing a
record yields `…audit.chain:2: hash does not match the record's bytes:
the record was edited`.

`TAMPER_DETECTED` with exit 71 is the code and exit status kit uses
for a failed verification.

## What verification cannot see

- Records cut from the tail still leave a chain that holds. Record
  `log.Head()` (`Seq` and `Hash`) somewhere the writer cannot reach,
  and compare it with `rep.Head` when verifying.
- Whole rotated files removed from the head also hold; that is what
  `MaxSegments` retention does by design.
- Someone who can rewrite the whole file can recompute every hash.
  Tamper-evidence, not tamper-proofing: anchor the head elsewhere.

## Served commands

You do not need to call `Append` yourself for served traffic.
`cmdsurface.ChainSink{Log: log}` appends one redacted record per
invocation (path, surface, exit code, caller, tenant, request and
trace ids, args and flags; never output), and
`service.WithAuditCommand()` mounts `<tool> audit verify`. The
configuration route (`services.<svc>.audit.sinks: [chain]`) is in
[secure-remote-serving](secure-remote-serving.md#keep-a-tamper-evident-trail).
The caller owns the log's lifecycle when constructing a `ChainSink`
directly.

## See also

- [`go/security` README](../../../go/security/README.md)
- [secure-remote-serving](secure-remote-serving.md)
