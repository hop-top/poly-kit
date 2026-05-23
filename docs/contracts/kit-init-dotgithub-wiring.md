# kit init: .github wiring and PR hooks contract

> Pinned contracts shared by the `kit-init-dotgithub-wiring` track.
> Implementation tasks: T-0772 (workflow callers), T-0773 (before-PR hook),
> T-0774 (after-PR hook), T-0775 (e2e), T-0776 (bus event workflows).
> Refs: `tlc/T-0771`, track `kit-init-dotgithub-wiring`.

`kit init` generates the consumer-side wiring a hop-top project needs to
plug into the shared `hop-top/.github` reusable workflows and into the local
git hooks that bracket PR creation. This document pins the contracts every
downstream impl task must implement to the letter so they compose without
follow-up renegotiation.

## 1. Ownership boundary

`kit init` and `hop-top/.github` are complementary, not overlapping. The
boundary is:

| Owner               | Owns                                                                                              | Never touches                                                  |
|---------------------|---------------------------------------------------------------------------------------------------|----------------------------------------------------------------|
| `kit init`          | consumer-side scaffolding rendered into the adopter repo                                          | reusable workflow implementation, org-default community files  |
| `hop-top/.github`   | reusable workflow implementation, org-default community files (CoC, security, funding, templates) | files inside an adopter repo's `.github/workflows/*-caller.yml`|

### What `kit init` generates

- `.github/workflows/*.yml` **caller** stubs that `uses:` reusable workflows
  hosted in `hop-top/.github` (e.g. `hop-top/.github/.github/workflows/release-go.yml@<ref>`).
- `.githooks/pre-pr` (before-PR hook) and `.githooks/post-pr-open` (after-PR
  hook) plus any helper scripts they invoke.
- `.github/workflows/kit-bus-*.yml` PR-scoped bus event workflows, generated
  in **disabled-by-default** form (see Section 3).
- `.kit/generated.json` — the manifest tracking everything `kit init` has
  generated (Section 6).

### What `kit init` never touches

- The reusable workflow **bodies** under `hop-top/.github/.github/workflows/*`
  (those are pulled by `uses:` reference, never inlined).
- Org-default community files served from `hop-top/.github/.github/`:
  `CODE_OF_CONDUCT.md`, `SECURITY.md`, `FUNDING.yml`, `ISSUE_TEMPLATE/`,
  `PULL_REQUEST_TEMPLATE.md`, `dependabot.yml`. These are GitHub
  organisation defaults; the adopter repo gets them by inheritance.
- Anything outside `.github/`, `.githooks/`, `.kit/` and the explicit opt-in
  paths declared by individual generators.

### Compatibility note

If `hop-top/.github` later changes a reusable workflow's required inputs or
secrets, the consumer-side caller stub becomes stale. `kit init` does **not**
auto-rewrite the caller — instead it follows the augment-mode conflict
policy (Section 6) and surfaces a `<path>.kit-suggested` sibling. Cross-repo
contract changes flow through that path, not through silent overwrites.

## 2. Canonical event names

PR lifecycle events emitted by generated workflows use the four-segment
form `[Source].[Category].[Object].[Action]`, with source `github`.
Canonical names (the complete enumeration for this track):

| Topic                          | When                                       | Listener intent                                                |
|--------------------------------|--------------------------------------------|----------------------------------------------------------------|
| `github.pr.run.completed`      | CI run finishes (success or failure)       | annotate the originating track/task; flag failures             |
| `github.pr.comment.created`    | A review comment is created                | nudge the author; schedule a follow-up                         |
| `github.pr.merged`             | PR is merged                               | close the originating track/task                               |
| `github.pr.closed`             | PR is closed without merge                 | reopen or annotate the originating task                        |

These names match `bus.ValidateTopic` (see `docs/contracts/event-topics.md`):
four lowercase segments, past-tense action.

### Common payload envelope

Every PR event carries the following fields (excerpt-style; not full logs):

```json
{
  "topic": "github.pr.run.completed",
  "repo": "hop-top/example",
  "pr": {
    "number": 123,
    "url": "https://github.com/hop-top/example/pull/123",
    "branch": "feat/something",
    "head_sha": "deadbeef…",
    "base_sha": "cafebabe…"
  },
  "actor": "octocat",
  "occurred_at": "2026-05-23T14:02:11Z"
}
```

Per-topic additions (only the bounded fields each listener needs to act;
full logs and full bodies are deliberately omitted in favour of URLs):

- `github.pr.run.completed`:

  ```json
  {
    "run": {
      "id": 9876543210,
      "name": "ci",
      "conclusion": "failure",
      "url": "https://github.com/hop-top/example/actions/runs/9876543210",
      "logs_url": "https://github.com/hop-top/example/actions/runs/9876543210/logs",
      "failure_summary": "test ./go/runtime/bus: FAIL TestPublishTimeout (3.42s)"
    }
  }
  ```

  `failure_summary` is bounded to one log excerpt — at most ~512 bytes,
  truncated with an ellipsis. Listeners that need the full log fetch
  `logs_url`.

- `github.pr.comment.created`:

  ```json
  {
    "comment": {
      "id": 1234567890,
      "kind": "review",
      "author": "octocat",
      "url": "https://github.com/hop-top/example/pull/123#discussion_r1234567890",
      "excerpt": "Consider returning early here."
    }
  }
  ```

  `excerpt` is also bounded (~512 bytes).

- `github.pr.merged`:

  ```json
  {
    "merge": {
      "merge_commit_sha": "abc1234…",
      "merged_at": "2026-05-23T14:03:00Z"
    }
  }
  ```

- `github.pr.closed`:

  ```json
  {
    "closed_at": "2026-05-23T14:03:00Z",
    "reason": "not_planned"
  }
  ```

Listeners derive everything else from the URLs in the payload. **Workflow
implementations must not embed full log bodies, full PR descriptions, or
full comment bodies**; payloads are intentionally bounded.

## 3. Bus gating

Generated workflows are **disabled by default**. A job emits to the bus only
when **all** of the following are true:

- `vars.KIT_BUS_ENABLED == "true"`
- `vars.KIT_BUS_INGRESS_URL` is non-empty

Delivery authenticates with one of:

- `secrets.KIT_BUS_TOKEN` (bearer token in `Authorization: Bearer …`), or
- `secrets.KIT_BUS_SIGNING_KEY` (HMAC-SHA256 signature in `X-Kit-Bus-Signature`).

If both are configured, prefer `KIT_BUS_SIGNING_KEY`.

### Failure modes

- **Fail open** by default. If delivery returns a non-2xx response, the
  workflow job logs the failure and exits 0. CI does not break because the
  bus is unreachable.
- **Fail closed** when `vars.KIT_BUS_STRICT == "true"`. Delivery failure
  fails the notification job. This is opt-in per repo.

Generated job stub (illustrative):

```yaml
emit-bus:
  if: ${{ vars.KIT_BUS_ENABLED == 'true' && vars.KIT_BUS_INGRESS_URL != '' }}
  runs-on: ubuntu-latest
  steps:
    - name: post to kit bus
      env:
        KIT_BUS_INGRESS_URL: ${{ vars.KIT_BUS_INGRESS_URL }}
        KIT_BUS_TOKEN: ${{ secrets.KIT_BUS_TOKEN }}
        KIT_BUS_SIGNING_KEY: ${{ secrets.KIT_BUS_SIGNING_KEY }}
        KIT_BUS_STRICT: ${{ vars.KIT_BUS_STRICT }}
      run: |
        # one of: fail-open (default) or fail-closed under KIT_BUS_STRICT=true
        ...
```

## 4. Scratchpad location (cross-platform)

The before-PR hook moves ephemeral planning artefacts (stray TODOs, scratch
notes, agent thought-dumps) out of the working tree and into a per-user OS
temp scratchpad. The exact location, pinned for every implementation:

| OS      | Location                                                                       |
|---------|--------------------------------------------------------------------------------|
| Linux   | `$XDG_RUNTIME_DIR/<project-id>.scratchpad` if `$XDG_RUNTIME_DIR` is set, else `$TMPDIR/<project-id>.scratchpad`, else `/tmp/<project-id>.scratchpad` |
| macOS   | `$TMPDIR/<project-id>.scratchpad`                                              |
| Windows | `%LOCALAPPDATA%\Temp\<project-id>.scratchpad`                                  |

In Go, the implementation reads `os.TempDir()` as the default and prefers
`$XDG_RUNTIME_DIR` on Linux when that variable is set and non-empty.

### `<project-id>` slug algorithm

The slug must be deterministic, filesystem-safe, and stable across machines
that share the same `origin` remote. Algorithm:

1. Read `git config --get remote.origin.url`.
2. If `origin` is set, derive the slug from it:
   1. Strip leading `git@host:` or `scheme://[user@]host[:port]/` (handle
      `https://`, `http://`, `ssh://`, and the `git@host:path` shorthand).
   2. Strip a trailing `.git` if present.
   3. Lowercase.
   4. Replace any character not in `[a-z0-9-]` with `-`.
   5. Collapse runs of `-` to a single `-`.
   6. Trim leading/trailing `-`.
3. If `origin` is not set (or the remote URL is empty), derive the slug from
   the absolute path to the repository root (`git rev-parse --show-toplevel`)
   using the same lowercase / non-`[a-z0-9-]` substitution / collapse / trim
   rules.
4. If the resulting slug is empty, use the literal string `kit-init`.

### Worked examples

| Input                                       | Slug                          |
|---------------------------------------------|-------------------------------|
| `git@github.com:hop-top/poly-kit.git`       | `github-com-hop-top-poly-kit` |
| `https://github.com/hop-top/poly-kit.git`   | `github-com-hop-top-poly-kit` |
| `https://gitea.example.org/team/Repo.Name`  | `gitea-example-org-team-repo-name` |
| (no `origin`) `/Users/jad/work/My Project`  | `users-jad-work-my-project`   |

So on macOS, in a clone of `hop-top/poly-kit`, the scratchpad lives at:

```
$TMPDIR/github-com-hop-top-poly-kit.scratchpad
```

## 5. Push/pull follow-up model (T-0774)

The after-PR-open hook chooses between two delivery models, in this order:

### Push path

If `vars.KIT_BUS_ENABLED == "true"` AND the configured ingress acks delivery
of the relevant event with **HTTP 2xx within 5 seconds**, the hook does
**not** create a local follow-up task. The bus listener (running on the
host the ingress fronts) owns the follow-up. The 5-second timeout is the
single canonical value; longer-running listeners must ack first and process
asynchronously.

### Pull path

The hook creates a scheduled local `tlc` task — due 10 minutes after PR
open — when **any** of the following is true:

- `vars.KIT_BUS_ENABLED != "true"` (bus disabled)
- `vars.KIT_BUS_INGRESS_URL` is empty
- the ingress is unreachable (connection refused, DNS failure, TLS error,
  or the 5-second timeout elapses)
- the ingress responds with a non-2xx status

The same model applies to `github.pr.merged` and `github.pr.closed`: if the
bus acks, no local task is created; otherwise a scheduled `tlc` task is
created with the appropriate event family in its body.

### Follow-up task content

When the pull path triggers, the generated task includes:

- a link back to the originating `tlc` track or task (when discoverable
  from the branch name or PR body)
- the PR URL and number
- the head SHA
- the originating branch
- the event family that triggered scheduling

### Duplicate-prevention key

The hook keys deduplication on the triple `(repo, PR number, event family)`,
where `event family` is one of `run`, `comment`, `merged`, `closed`. This
gives the right semantics:

- two CI runs on the same PR collapse to one follow-up
- a closed-then-reopened PR creates a fresh follow-up (the close and the
  next open are distinct events)
- a merge and a close on different PRs are independent

### Failure modes when local tooling is missing

If `tlc` or `gh` is not installed or not on `$PATH`, the hook logs **one**
actionable single-line message to stderr (e.g. `kit: tlc not found on PATH;
skipping scheduled follow-up for PR #123`) and exits 0. PR creation is
never blocked by missing local tooling — that would defeat the whole point
of fail-open.

## 6. Augment-mode conflict policy and manifest

This is the single canonical phrasing all four impl tasks must use:

> **`kit init` never overwrites existing files.** When `kit init` (in augment
> mode) would change a file that already exists, it writes a sibling at
> `<path>.kit-suggested` with the would-be contents, leaves the original
> untouched, and records both the would-be path and the conflict in the
> dry-run / JSON output. The `.kit/generated.json` manifest distinguishes
> "user edited" (file hash differs from manifest) from "kit can refresh in
> place" (file hash matches manifest).

### `.kit/generated.json` manifest format

```json
{
  "version": 1,
  "generated_by": "kit-init",
  "files": [
    {
      "path": ".github/workflows/release-go.yml",
      "sha256": "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
      "generatedAt": "2026-05-23T14:00:00Z"
    },
    {
      "path": ".githooks/pre-pr",
      "sha256": "2c26b46b68ffc68ff99b453c1d30413413422d706483bfa0f98a5e886266e7ae",
      "generatedAt": "2026-05-23T14:00:00Z"
    }
  ]
}
```

Schema rules:

- `version` is an integer. Current version: `1`. Bump when the manifest
  shape changes incompatibly.
- `generated_by` identifies the producing tool. Current value:
  `"kit-init"`.
- Each entry in `files` has:
  - `path` — repo-relative POSIX path (forward slashes, no leading `./`).
  - `sha256` — hex-encoded SHA-256 of the file contents at generation time.
  - `generatedAt` — RFC 3339 UTC timestamp.

### Refresh logic (consumed by future augment runs)

1. For each manifest entry, recompute the on-disk SHA-256.
2. If the on-disk hash **matches** the manifest hash, kit may refresh the
   file in place (overwrite with new generated contents and update the
   manifest entry).
3. If the on-disk hash **differs** from the manifest hash, the file has been
   user-edited. Kit writes the new contents to `<path>.kit-suggested` and
   leaves the original untouched.
4. If a file appears on disk but is not in the manifest and a generator
   wants to write it, treat as user-edited: write `<path>.kit-suggested`.

### Dry-run / JSON output

`kit init --dry-run` (or `--format json`) reports, for each file the
generator would touch:

```json
{
  "path": ".github/workflows/release-go.yml",
  "action": "write" | "skip-unchanged" | "suggest-sibling",
  "suggested_path": ".github/workflows/release-go.yml.kit-suggested",
  "reason": "user-edited" | "new" | "refresh"
}
```

`suggested_path` is present only when `action == "suggest-sibling"`.

## 7. Before-PR hook failure semantics (T-0773)

The before-PR hook (rendered at `.githooks/pre-pr` or installed via the
repo's existing `.githooks/` convention) runs the following gates:

1. project lint (`make lint` or equivalent declared by the adopter)
2. project tests (`make test` or equivalent)
3. scratchpad cleanup detection: scan tracked source/docs for ephemeral
   planning artefacts and relocate them to the path pinned in Section 4.
   If any artefact would have to be moved at PR time, the gate fails — the
   intent is that the hook is run pre-PR and the author commits a clean
   working tree.

### Exit semantics

- **Exit 0** when all gates pass.
- **Exit non-zero** when any gate fails. The hook writes a single-line
  actionable message to stderr naming the failing gate (e.g.
  `kit: pre-pr lint failed (golangci-lint exit 1); see <log>`), then a
  brief detail block.

A non-zero exit **blocks PR creation**.

### Bypass

There is **one** escape: the standard git hook bypass, `--no-verify` (e.g.
`git push --no-verify` or, for workflows that invoke the hook via a wrapper,
the wrapper's documented `--no-verify` passthrough). There is **no built-in
"warn-only" mode**. Adopters who want gate output without enforcement run
the gates directly (`make lint`, `make test`) outside the hook.

## 8. Opt-in / opt-out flags and augment-tier behavior

`kit init` exposes per-generator flags so adopters can include or exclude
each piece of wiring. Defaults are tuned so a hop-top project that runs
`kit init` with no flags ends up wired into the standard hop-top contract.

| Flag                              | Default | Effect                                                                     |
|-----------------------------------|---------|----------------------------------------------------------------------------|
| `--with-github-workflows`         | `true`  | Render `.github/workflows/*-caller.yml` stubs (T-0772).                    |
| `--with-githook-pre-pr`           | `true`  | Render `.githooks/pre-pr` and helpers (T-0773).                            |
| `--with-githook-post-pr-open`     | `true`  | Render `.githooks/post-pr-open` and helpers (T-0774).                      |
| `--with-bus-workflows`            | `true`  | Render `.github/workflows/kit-bus-*.yml` (T-0776). Disabled at runtime by default per Section 3. |
| `--dry-run`                       | `false` | Compute the file list without writing; emit JSON report.                   |
| `--format json`                   | (off)   | Emit machine-readable plan output (see Section 6).                         |

Each `--with-*` flag has a `--without-*` complement that flips the default
to `false`. Example: `kit init --without-bus-workflows` skips bus event
workflow generation entirely.

### Augment-tier behavior

When `kit init` is invoked in **augment mode** (the project already has a
`.kit/generated.json` manifest or any of the target files exist):

- Selected generators (per the flags above) are evaluated against the
  manifest using the refresh logic in Section 6.
- Files matching the manifest hash refresh in place.
- Files diverging from the manifest hash produce `<path>.kit-suggested`
  siblings.
- New files (target path absent on disk) are written.
- `--dry-run` is supported in augment mode and reports the same
  `action`/`reason` shape as in bootstrap mode.

### Bootstrap-tier behavior

When `.kit/generated.json` does not exist and no target files are present,
`kit init` writes each selected file and emits a new manifest. If **any**
target file is already present, `kit init` treats the run as augment for
that file (writes `<path>.kit-suggested`) but still emits the manifest for
the files it did write.

## 9. Cross-references

- Topic shape rules: [`docs/contracts/event-topics.md`](./event-topics.md)
- Bus topic validator: [`go/runtime/bus/topics.go`](../../go/runtime/bus/topics.go)
- Existing `kit init` implementation: [`cmd/kit/init/`](../../cmd/kit/init/)
- Track plan: [`.tlc/tracks/kit-init-dotgithub-wiring/plan.md`](../../.tlc/tracks/kit-init-dotgithub-wiring/plan.md)
