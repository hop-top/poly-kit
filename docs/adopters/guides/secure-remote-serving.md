# Secure remote serving

Serve your command tree beyond the machine it runs on without
serving it to everyone: loopback by default, authentication before
exposure, one permission gate on every transport, and one audit
trail that records who asked for what and what happened.

## Who this is for

Developers running a kit CLI as a service — the `api` service over
HTTP, the `socket` service over a Unix socket, or both — who need to
answer "who can reach this, what may they run, and how do I know what
they ran." It targets the
[go-toolmaker](../../personas/go-toolmaker.md) wiring the tool and the
[security-operator](../../personas/security-operator.md) who has to
sign off on the deployment.

If you have not exposed your commands yet, start with
[expose-cli-over-rest.md](expose-cli-over-rest.md) or
[serve-cli-over-unix-socket.md](serve-cli-over-unix-socket.md); this
guide picks up where those leave off.

## Before you begin

You need:

- A kit project with a cobra root (see
  [create-cli-project.md](create-cli-project.md))
- `hop.top/kit/go/console/cli`, `hop.top/kit/go/transport/api`, and
  `hop.top/kit/go/transport/cmdsurface` importable
- Commands annotated with `kit/side-effect`; commands that need an
  entitlement additionally annotated with `kit/permissions`

Every Go example below is a complete program. `widgetCmd()` stands
for your own command tree; substitute it.

## What you get

- **Loopback by default.** `WithAPI` listens on `127.0.0.1:8080`
  unless you say otherwise. Saying otherwise without authentication
  is refused before anything binds, and so is saying it without a
  delegation policy: exposure needs an answer to who may call and to
  what any caller may run.
- **One identity model on every transport.** The principal, tenant,
  request id, trace id, and idempotency key travel with each call
  into the same `Meta`, whether it arrived over HTTP or the socket.
- **One permission gate.** A decision you write once runs inside the
  bridge, after the destructive ceiling and before the command, on
  every transport. A caller cannot route around it by picking a
  different one.
- **One audit trail.** Every refusal — not authenticated, not
  permitted, not confirmed — and every command that ran over a remote
  surface reaches the sinks you register, with the same fields.

The socket service needs none of the address rules: a Unix socket has
no port and is not routable. The file is created `0600`, so the
filesystem permission is the access control, and the service is
loopback-only by construction.

## Steps

### 1. Serve on loopback (the default)

Registering the api service with no address serves the machine you
are on and nothing else:

```go
package main

import (
    "context"
    "log"

    "hop.top/kit/go/console/cli"
)

func main() {
    root := cli.New(cli.Config{Name: "mytool", Version: "1.4.2"},
        cli.WithAPI(cli.APIConfig{}), // listens on 127.0.0.1:8080
    )
    root.Cmd.AddCommand(widgetCmd())

    if err := root.Execute(context.Background()); err != nil {
        log.Fatal(err)
    }
}
```

```console
$ mytool serve
INFO service started service=api
INFO service ready service=api address=127.0.0.1:8080
```

```bash
curl -s http://127.0.0.1:8080/v1/commands/widget/list
```

```json
{"exit_code":0,"stdout":"widget-1\nwidget-2\n"}
```

`127.0.0.1`, `::1`, and the literal `localhost` are loopback. A bare
port (`:8080`), `0.0.0.0`, `::`, or any other host is not.

### 2. What the refusal looks like when you forget

Change the address and nothing else:

```go
import "hop.top/kit/go/console/cli"

cli.WithAPI(cli.APIConfig{Addr: "0.0.0.0:8080"})
```

```console
$ mytool serve
USAGE: service "api": addr: "0.0.0.0:8080" is not a loopback address and the api service has no authentication; set APIConfig.Auth, listen on 127.0.0.1, or set services.api.insecure_remote: true (or --insecure-remote) to serve unauthenticated beyond loopback
$ echo $?
2
```

Nothing bound. The three ways forward are the three the message
names: configure `Auth` (step 3), go back to loopback, or accept the
exposure by name (step 4). `--addr` on the command line is refused
the same way, and so is `--no-auth` on a non-loopback address:

```console
$ mytool serve --no-auth
USAGE: service "api": addr: "0.0.0.0:8080" is not a loopback address and --no-auth disables authentication; drop --no-auth, listen on 127.0.0.1, or set services.api.insecure_remote: true (or --insecure-remote) to serve unauthenticated beyond loopback
```

`--no-auth` still works on loopback, as it always did.

Authentication is only the first gate. Configure `Auth` (step 3) and
the same address is refused again, this time for what it does not
bound:

```console
$ mytool serve
USAGE: service "api": addr: "0.0.0.0:8080" is not a loopback address and no delegation policy is configured; set --policy, listen on 127.0.0.1, or set services.api.insecure_no_policy: true (or --insecure-no-policy) to serve every command beyond loopback
$ echo $?
2
```

Without a `--policy` the permission gate permits every command for
every caller — the same verdict it would give if there were no gate
at all — so an authenticated caller may still run your destructive
commands. The three ways forward are again the three the message
names: name a policy (step 5), go back to loopback, or accept it by
name (step 4). Both gates apply independently: a non-loopback address
must satisfy each.

### 3. Expose beyond loopback with Auth

`APIConfig.Auth` is what permits a non-loopback address. It runs
before every route, projected and your own, and the claims it returns
are how each call is attributed. Return an [`api.Claims`](../../../go/transport/api/mw_auth.go),
any value implementing `api.Identity`, or a string-keyed map with
`sub` and `tenant`; the transport reads the principal and tenant out
of any of them without knowing your type.

```go
package main

import (
    "context"
    "errors"
    "log"
    "net/http"
    "strings"

    "hop.top/kit/go/console/cli"
    "hop.top/kit/go/transport/api"
)

// tokens stands in for your identity provider. A real AuthFunc
// verifies a JWT or asks a token endpoint; the shape it returns is
// the same.
var tokens = map[string]api.Claims{
    "t0k3n-alice": {Subject: "alice", Tenant: "acme", Scopes: []string{"widgets:read", "widgets:admin"}},
    "t0k3n-bob":   {Subject: "bob", Tenant: "acme", Scopes: []string{"widgets:read"}},
}

func authenticate(r *http.Request) (any, error) {
    token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
    if !ok {
        return nil, errors.New("missing bearer token")
    }
    claims, ok := tokens[token]
    if !ok {
        return nil, errors.New("unknown token")
    }
    return claims, nil
}

func main() {
    root := cli.New(cli.Config{Name: "mytool", Version: "1.4.2"},
        cli.WithAPI(cli.APIConfig{
            Addr: "0.0.0.0:8080",
            Auth: authenticate,
        }),
    )
    root.Cmd.AddCommand(widgetCmd())

    if err := root.Execute(context.Background()); err != nil {
        log.Fatal(err)
    }
}
```

An unauthenticated call is answered before any command runs:

```bash
curl -s -i http://10.0.0.5:8080/v1/commands/widget/list
```

```http
HTTP/1.1 401 Unauthorized
Content-Type: application/json
X-Request-ID: 6d4a0f0e8c2b4b1e9f3a7c5d2e1b0a94

{"status":401,"code":"unauthorized","message":"missing bearer token"}
```

With a token, the call runs and is attributed to `alice` of `acme`:

```bash
curl -s http://10.0.0.5:8080/v1/commands/widget/list \
  -H 'Authorization: Bearer t0k3n-alice'
```

```json
{"exit_code":0,"stdout":"widget-1\nwidget-2\n"}
```

`Scopes` are not interpreted by kit. They reach the permission gate
in step 5 as `Meta.Extra["scopes"]`, comma-joined, which is where a
`kit/permissions` annotation gets enforced.

### 4. The opt-ins, and what they mean

There are two, one per gate, and neither implies the other.

To serve **without** authentication on a non-loopback address, say
so by name, in code, config, or on the command line:

```go
import "hop.top/kit/go/console/cli"

cli.WithAPI(cli.APIConfig{Addr: "0.0.0.0:8080", InsecureRemote: true})
```

```yaml
# ~/.config/mytool/config.yaml
services:
  api:
    addr: 0.0.0.0:8080
    insecure_remote: true
```

```console
$ mytool serve --insecure-remote
```

The flag wins over the config key, and the config key wins over the
code. With it set, every host that can reach the address may run
every command the policy permits, as whoever it claims to be: the
audit trail records what it claimed, and `Meta.Caller` is empty
because nothing established it. That is the whole effect of the
flag, and the name is chosen so it reads that way in a config review.

To serve on a non-loopback address with **no delegation policy**, say
that separately:

```go
import "hop.top/kit/go/console/cli"

cli.WithAPI(cli.APIConfig{Addr: "0.0.0.0:8080", InsecureNoPolicy: true})
```

```yaml
# ~/.config/mytool/config.yaml
services:
  api:
    addr: 0.0.0.0:8080
    insecure_no_policy: true
```

```console
$ mytool serve --insecure-no-policy
```

Same precedence: flag, then config key, then code. With it set, any
caller the surface admits may run the whole command tree, destructive
commands included, because nothing bounds what the permission gate
allows.

The two are deliberately separate keys, because they waive different
things. `insecure_remote` says you accept unidentified callers;
`insecure_no_policy` says you accept unbounded ones. A tool with
`Auth` configured has answered the first question and not the second,
and a tool with a `--policy` and no `Auth` has answered the second and
not the first. Setting one never sets the other, so a config review
can see exactly which of the two you accepted:

```console
$ mytool serve --addr 0.0.0.0:8080 --insecure-remote
USAGE: service "api": addr: "0.0.0.0:8080" is not a loopback address and no delegation policy is configured; ...
```

Loopback needs neither. Serving on `127.0.0.1` keeps
allow-by-default, with no policy and no opt-in, because that is the
development path and the caller is already on your machine. The
socket service needs neither for the same reason: it is loopback by
construction.

### 5. Wire a permission policy

Annotate a command with the entitlement it needs:

```go
package main

import (
    "context"
    "log"

    "github.com/spf13/cobra"

    "hop.top/kit/go/console/cli"
)

func widgetPurgeCmd() *cobra.Command {
    return &cobra.Command{
        Use:   "purge",
        Short: "Delete every widget",
        Annotations: map[string]string{
            "kit/side-effect": "write-shared",
            "kit/permissions": "widgets:admin",
        },
        RunE: func(cmd *cobra.Command, _ []string) error {
            cmd.Println("purged")
            return nil
        },
    }
}

func main() {
    root := cli.New(cli.Config{Name: "mytool", Version: "1.4.2"},
        cli.WithAPI(cli.APIConfig{}),
    )
    widget := &cobra.Command{Use: "widget", Short: "Manage widgets"}
    widget.AddCommand(widgetPurgeCmd())
    root.Cmd.AddCommand(widget)

    if err := root.Execute(context.Background()); err != nil {
        log.Fatal(err)
    }
}
```

Then install the decision. `cli.WithPermission` runs it inside the
bridge for the api service and the socket service alike, after the
destructive ceiling and before the command:

```go
package main

import (
    "context"
    "log"
    "strings"

    "hop.top/kit/go/console/cli"
    "hop.top/kit/go/transport/cmdsurface"
)

// requireScopes refuses a caller whose credential lacks a scope the
// command declares under kit/permissions.
func requireScopes(_ context.Context, meta cmdsurface.Meta, leaf *cmdsurface.Leaf) cmdsurface.PermissionDecision {
    have := map[string]bool{}
    for _, s := range strings.Split(meta.Extra["scopes"], ",") {
        have[s] = true
    }
    for _, need := range leaf.Class.Permissions {
        if !have[need] {
            return cmdsurface.PermissionDecision{Reason: "missing scope " + need}
        }
    }
    return cmdsurface.PermissionDecision{Allowed: true}
}

func main() {
    root := cli.New(cli.Config{Name: "mytool", Version: "1.4.2"},
        cli.WithAPI(cli.APIConfig{Addr: "0.0.0.0:8080", Auth: authenticate}),
        cli.WithSocket(cli.SocketConfig{}),
        cli.WithPermission(requireScopes),
    )
    root.Cmd.AddCommand(widgetCmd())

    if err := root.Execute(context.Background()); err != nil {
        log.Fatal(err)
    }
}
```

What a denied caller sees over REST — `403`, not `401`, because bob
is authenticated and the refusal is about what bob may do:

```bash
curl -s -X POST http://10.0.0.5:8080/v1/commands/widget/purge \
  -H 'Authorization: Bearer t0k3n-bob' \
  -H 'Content-Type: application/json' -d '{}'
```

```json
{"status":403,"code":"permission_denied","message":"api: permission denied: cmdsurface: permission denied: widget purge on rest: missing scope widgets:admin"}
```

The same caller over the socket gets the same verdict under the
socket's own code:

```console
$ echo '{"path":["widget","purge"],"caller":"bob"}' | socat - UNIX-CONNECT:/tmp/mytool.sock
{"ok":false,"error":{"code":"DENIED","message":"cmdsurface: permission denied: widget purge on rpc: missing scope widgets:admin"}}
```

Over the socket there is no `Authorization` header, so `scopes` is
absent and `caller` is what the request claimed: without an
authenticator on the socket (`SocketConfig.Auth`), a decision that
trusts `Meta.Caller` there is trusting the caller's word. Decide on
what the transport verified, or give the socket an authenticator.

Discovery does not change for a caller-specific refusal. `GET
/v1/commands` cannot know who will call, so `widget purge` stays
listed as invocable and the gate answers per call. A command your
decision refuses **for everyone** is different: return
`CallerIndependent: true` and discovery withholds it at mount with
the reason `permission-denied`, exactly as it withholds an
interactive command:

```json
{"name": "widget purge", "invocable": false, "reason": "permission-denied"}
```

The tool's policy engine is wired into the same gate, and naming one
is what satisfies the second exposure gate from step 2. A `--policy`
that refuses a side-effect class refuses it on every surface for
every caller, before your decision is asked, and discovery reflects
it the same way:

```console
$ mytool serve api --policy=readonly
$ curl -s http://127.0.0.1:8080/v1/commands | jq '.commands[] | select(.name=="widget purge")'
```

```json
{"name": "widget purge", "side_effect": "write", "invocable": false, "reason": "permission-denied"}
```

### 6. Read the audit trail

Register a sink. `cli.WithAuditSinks` applies it to the api service
and the socket service; a JSON-Lines file on stderr is the smallest
useful one:

```go
package main

import (
    "context"
    "log"
    "os"

    "hop.top/kit/go/console/cli"
    "hop.top/kit/go/transport/cmdsurface"
)

func main() {
    root := cli.New(cli.Config{Name: "mytool", Version: "1.4.2"},
        cli.WithAPI(cli.APIConfig{Addr: "0.0.0.0:8080", Auth: authenticate}),
        cli.WithSocket(cli.SocketConfig{}),
        cli.WithPermission(requireScopes),
        cli.WithAuditSinks(cmdsurface.SinkSpec{
            Sink:    &cmdsurface.FileSink{W: os.Stderr},
            OnOK:    true, // executions
            OnError: true, // refusals and failed executions
        }),
    )
    root.Cmd.AddCommand(widgetCmd())

    if err := root.Execute(context.Background()); err != nil {
        log.Fatal(err)
    }
}
```

The three calls from steps 3 and 5 produce three records — not
authenticated, ran, not permitted — with the same fields on each:

```json
{"at":"2026-09-04T16:03:11Z","path":"widget list","surface":"rest","exit_code":0,"error":"cmdsurface: authentication refused: missing bearer token","request_id":"6d4a0f0e8c2b4b1e9f3a7c5d2e1b0a94"}
{"at":"2026-09-04T16:03:12Z","path":"widget list","surface":"rest","exit_code":0,"request_id":"9c1e7b3a4d2f4e8b8a6c0d5e1f2a3b4c","caller":"alice","tenant":"acme"}
{"at":"2026-09-04T16:03:13Z","path":"widget purge","surface":"rest","exit_code":0,"error":"cmdsurface: permission denied: widget purge on rest: missing scope widgets:admin","request_id":"0f2e4d6c8b1a4c3e9d7f5a2b1c0e8d94","caller":"bob","tenant":"acme"}
```

Read the verdict from two fields: `error` is set when the call was
refused before the command ran, and names the gate that refused it;
`exit_code` is the command's own outcome when it did run. A
destructive command refused by its confirmation gate is the second
kind — no `error`, `exit_code` 5 — because confirmation is the
command's flag, not the transport's.

`FileSink` is one of four ready-made sinks. `LogSink` writes the same
fields as `slog` attributes, `WebhookSink` posts the envelope to a
URL, and `BusSink` publishes it; see
[the cmdsurface reference](../reference/cmdsurface.md#sinks).
Sinks are best-effort and cannot change a verdict.

### 7. Propagate request and trace ids

Send the standard headers and they travel into `Meta` and the audit
record; the request id is echoed on the response so a caller can
find its own record:

```bash
curl -s -i http://10.0.0.5:8080/v1/commands/widget/list \
  -H 'Authorization: Bearer t0k3n-alice' \
  -H 'X-Request-ID: req-42' \
  -H 'traceparent: 00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01' \
  -H 'Idempotency-Key: 8f1c2a'
```

```http
HTTP/1.1 200 OK
Content-Type: application/json
X-Request-ID: req-42

{"exit_code":0,"stdout":"widget-1\nwidget-2\n"}
```

```json
{"at":"2026-09-04T16:05:40Z","path":"widget list","surface":"rest","exit_code":0,"trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","request_id":"req-42","caller":"alice","tenant":"acme"}
```

| Header | Lands in | Notes |
|---|---|---|
| `X-Request-ID` | `Meta.RequestID` | issued by the server when absent; echoed on the response |
| `traceparent` | `Meta.TraceID` | the W3C trace-id field; `X-Trace-ID` is the fallback |
| `Idempotency-Key` | `Meta.IdempotencyKey` | forwarded to the command's `--idempotency-key` flag when it has one |

Over the socket the same values are request fields:

```console
$ echo '{"path":["widget","list"],"request_id":"req-42","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","idempotency_key":"8f1c2a"}' \
    | socat - UNIX-CONNECT:/tmp/mytool.sock
```

A caller that disconnects mid-command cancels the command's context
on both transports; a command that honors its context stops.

## Option reference

| Option | Default | Effect |
|---|---|---|
| `APIConfig.Addr` | `127.0.0.1:8080` | Listen address. Non-loopback needs `Auth` or `InsecureRemote`. |
| `APIConfig.Auth` | none | Authenticates every route and permits any address. Claims attribute the call. |
| `APIConfig.InsecureRemote` | `false` | Serve unauthenticated beyond loopback. `services.api.insecure_remote` / `--insecure-remote` set the same. |
| `APIConfig.InsecureNoPolicy` | `false` | Serve beyond loopback with no delegation policy. `services.api.insecure_no_policy` / `--insecure-no-policy` set the same. |
| `SocketConfig.Auth` | none | Verifies each socket request; the verified identity replaces the claimed one. |
| `cli.WithPermission(fn)` | permit all | Permission gate on every kit-shipped transport service. |
| `cli.WithAuditSinks(specs...)` | none | Audit sinks on every kit-shipped transport service. |
| `--policy=<name>` | none | The tool's policy engine, applied to remote calls for every caller. Naming one permits a non-loopback address. |

Precedence for either opt-in is flag, then config key, then code. The
socket path, exposure patterns, and destructive policy are documented
in their own guides and are unchanged.

## What it does not implement

Absence here is deliberate; each of these belongs somewhere else:

- **An identity provider.** `Auth` is yours. Kit verifies nothing
  about a token and issues none; it carries what your function
  returns.
- **A tenant registry.** `Meta.Tenant` is a label your claims
  supply. Nothing scopes state by it.
- **Socket peer credentials out of the box.** `SocketConfig.Auth`
  receives the connection so an authenticator can ask the kernel
  (`SO_PEERCRED`, `LOCAL_PEERCRED`) who is on the other end; kit
  ships no such authenticator.
- **A dedupe store.** The idempotency key reaches the command's flag
  and the audit record; replay is the command's own middleware.
- **Forced remote execution.** Interactive commands, destructive
  commands the policy withholds, and commands the permission gate
  refuses stay refused. There is no override.
- **A default policy.** Kit ships none and infers none. A tool with
  no `--policy` is unbounded, which is why serving beyond loopback
  makes you either name one or accept the absence by name.
- **A per-caller discovery listing.** Discovery is one document for
  every caller; caller-specific verdicts are given per call.

## Related pages

- [expose-cli-over-rest.md](expose-cli-over-rest.md) — the api
  service: routes, discovery, destructive commands, confirmation
- [serve-cli-over-unix-socket.md](serve-cli-over-unix-socket.md) —
  the socket service: wire format, permissions, restrictions
- [serve-lifecycle contract](../../contracts/serve-lifecycle.md#security)
  — the normative rules this guide applies
- [api README](../reference/transport-api.md#auth) — claims
  shapes, request provenance, refusal codes
- [socket README](../../../go/transport/socket/README.md) — request
  fields, error codes, the authenticator hook
- [cmdsurface README](../../../go/transport/cmdsurface/README.md) —
  the bridge, its gates, and the sinks
