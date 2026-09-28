# security

## What it answers

"Can I trust this artifact and this execution?" — claims made by things
the tool did not produce or cannot fully control. Today that is one
family: a tamper-evident audit log. Keys and tokens are
`go/core/identity`; secret material is `go/storage/secret`; scrubbing
data is `go/core/redact`.

## Use it when

- a record must be provably unedited after the fact → `OpenAuditLog`,
  `AuditLog.Append`
- checking a log for edits, deletions, reorders or insertions →
  `VerifyAuditLog`, which reports the first break
- served commands should land in one → `cmdsurface.ChainSink`, or
  `services.<svc>.audit.sinks: [chain]` on a kit-shipped service

## Quick start

```go
log, _ := security.OpenAuditLog(path, security.AuditLogOptions{})
_, _ = log.Append([]byte(`{"path":"widget add","caller":"alice"}`))
_, _ = log.Append([]byte(`{"path":"widget purge","caller":"bob"}`))
_ = log.Close()

rep, _ := security.VerifyAuditLog(path)
fmt.Println("holds:", rep.OK(), "records:", rep.Records)
```

Prints `holds: true records: 2`; edit either record and `rep.Break`
names its line (`example_test.go`).

## Contract

- One record per line: `{"v":1,"seq":N,"at":…,"prev":<hex>,"rec":<payload>,"hash":<hex>}`.
  `hash` is SHA-256 over the line up to `,"hash":` plus a closing `}`;
  `prev` is the previous record's hash, empty on seq 1.
- One writer: a second `OpenAuditLog` on the same path, from any
  process, fails with `ErrAuditLogBusy`. Appends within one log are
  serialized.
- `SyncNever` (default) survives a process crash, `SyncAlways` a power
  loss, `SyncPeriodic` bounds the loss to one interval.
- `MaxBytes` rotates to `<path>.<first seq, 20 digits>`; the chain
  continues across files. `MaxSegments` deletes the oldest rotated
  files; verification then anchors at the oldest kept record.
- Not detectable from the file alone: records cut from the tail. Keep
  `AuditLog.Head` somewhere the writer cannot reach.
- A failed verification is `TAMPER_DETECTED`, exit 71
  (`CodeTamperDetected`, `ExitTamperDetected`), recorded in
  `go/console/output/envelope`'s extension band.

## Neighbours

- `go/transport/cmdsurface`: `ChainSink` puts invocation audits here,
  after redaction.
- `go/console/cli`: `cli.WithAuditCommand` mounts `<tool> audit verify`.
- `go/runtime/provenance`: field-level provenance, which this log does
  not feed yet.

## See also

- [Keep a tamper-evident trail](../../docs/adopters/guides/secure-remote-serving.md#keep-a-tamper-evident-trail)
