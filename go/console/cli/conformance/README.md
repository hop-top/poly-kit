# conformance

## What it answers

Which cobra command tree sits behind `kit conformance`, and which exit codes
its leaves return. It is the command surface, not a test helper: adopters
asserting their own root against the Layer-A contract want
`hop.top/kit/go/conformance` and its `AssertCLI`, which this package does not
export.

## Use it when

- mounting the whole tree on a kit root → `root.AddCommand(conformance.Cmd())`
- classifying a leaf error in tests → `conformance.ExitCode(err)`
- returning a typed failure from a custom leaf → `LeakDetectedError`,
  `UsageError`, `IOError`, `ConfigError`

| Path | What it is | Start here when |
|------|------------|-----------------|
| `grade/` | `kit conformance grade` leaf: uploads a cassette, prints the verdict | wiring CI grading or PR comments |
| `svc/` | `kit conformance svc {serve,token}` operator tree | running the grading service |
| `harness/` | group directory, no Go source; holds `record` | never directly |
| `harness/record/` | `kit conformance harness record` leaf: scenario plus binary to cassette | producing input for `grade` |
| `badge/` | `kit conformance badge` leaf: shields.io endpoint JSON | seeding or regenerating `.12fc.json` |

## Quick start

```go
leak := conformance.LeakDetectedError("scenario-shaped block in README.md")
code, known := conformance.ExitCode(leak)
fmt.Println(code, known)

cfg := conformance.ConfigError("bad allowlist", ".verifynoleak.allow:3", "remove the bare ignore")
code, known = conformance.ExitCode(cfg)
fmt.Println(code, known)

code, known = conformance.ExitCode(fmt.Errorf("unrelated"))
fmt.Println(code, known)
// 66 true
// 67 true
// 0 false
```

## Contract

Subcommands: `verify-no-leak`, `install-hooks`, `verify-stories`, `grade`,
`badge`, `harness record`, `svc`. `static` and `generate-stories` are
reserved placeholders that exit 2. Alias: `kit con`.

Exit codes for the leaves in this package:

| Code | Name | Meaning |
|------|------|---------|
| 0 | clean | no findings |
| 2 | usage | bad flags, or reserved name invoked |
| 6 | io | git, gh or fs failed; transient |
| 66 | LEAK_DETECTED | scenario-shaped block found |
| 67 | CONFIG | bad `.verifynoleak.allow` or bare ignore |

66 and 67 extend the band that `hop.top/kit/go/console/output` opens with 64
(RATE_LIMITED) and 65 (PROVENANCE_MISSING); 68 and 69 belong to
`hop.top/kit/go/conformance/client`. Kit releases before this layout used
2 leak / 3 usage / 4 io / 5 config; pin a release if you consume those.
Sentinels are `*output.Error` values; exit codes survive kit's RunE middleware.

## Neighbours

- `hop.top/kit/go/conformance`: Layer-A `AssertCLI` helper adopters import in
  tests; also parents `client`, `recorder`, `scenario`, `story`, `badge`
- `hop.top/kit/go/console/cli`: root, annotations (`SetSideEffect`,
  `SetHierarchical`, `SetExemptValidation`) every leaf here uses
- `hop.top/kit/go/console/output`: `Error` envelope and the 64/65 band

## See also

- [Go primitives reference](../../../../docs/adopters/reference/go-primitives.md)
- [kit init guide](../../../../docs/adopters/guides/kit-init.md)
- [Architecture](../../../../docs/contributors/architecture/architecture.md)
- [Layer-A helper README](../../../conformance/README.md)
