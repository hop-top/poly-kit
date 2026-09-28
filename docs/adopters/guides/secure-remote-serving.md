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
- **One permission gate.** A command's `kit/permissions` scopes are
  enforced against the caller's verified credential, and a decision
  you write once runs after them, inside the bridge, before the
  command, on every transport. A caller cannot route around it by
  picking a different one.
- **One audit trail.** Every refusal — not authenticated, not
  permitted, not confirmed, body too large — and every command that
  ran over a remote surface reaches the sinks you register, with the
  same fields and with secrets redacted.
- **Bounded request bodies.** Every route the api service serves —
  the projection, your `Handlers` and `Resources` — refuses a body
  over 1 MiB with `413` and code `body_too_large`, whether the
  `Content-Length` declares it or a chunked body crosses the cap
  mid-read. Raise or lower it with `services.api.body_limit.max_bytes`
  (or `services.all.body_limit.max_bytes` for every service).
- **TLS where you want it.** Terminate it at your proxy, or on the
  listener itself with a certificate file or ACME; with client
  certificates (`auth.mode: mtls`) the certificate is the credential.
- **A rate limit beyond loopback.** A service listening beyond
  loopback bounds how fast each caller may invoke commands, per
  side-effect tier, and answers the excess `429 rate_limited` with
  `Retry-After`. On loopback it is off until you turn it on. A quota
  caps what each caller uses per hour or day, and survives restarts. See
  [Bound how fast a caller may call](#11-bound-how-fast-a-caller-may-call).
- **No reach from a browser tab.** The api service refuses requests
  whose `Host` it does not answer for (DNS rebinding) and writes from
  pages on other origins (cross-site request forgery), and sets
  hardening headers on every response. All on by default, loopback
  and beyond.

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
the same address serves, and with no `--policy` named it serves under
`kit-default`, the policy kit ships for exactly this case:

| Caller | May run |
|---|---|
| not established (no verified credential) | read commands |
| an established principal | read and write commands |
| an established principal, destructive command | only one declaring `kit/permissions`, whose scopes it holds; the command's own confirmation still applies |

A command that declares no `kit/side-effect` counts as a write. Every
refusal names the policy and the way past it:

```json
{"status":403,"code":"permission_denied","message":"api: permission denied: cmdsurface: permission denied: widget add on rest: policy: write not allowed for widget add (policy kit-default: kit's default beyond loopback: writes need an authenticated caller, destructive commands a declared kit/permissions scope; name a --policy to choose otherwise)"}
```

Without it, a tool that names no `--policy` would have a permission
gate that permits every command for every caller, destructive included.
Name your own policy (step 5) to choose other rules, or accept an
unbounded surface by name (step 4). Loopback is unaffected: there, no
`--policy` still means no policy.

### 3. Expose beyond loopback with Auth

`APIConfig.Auth` is what permits a non-loopback address. It runs
before every route, projected and your own, and the claims it returns
are how each call is attributed. "Every route" includes
`/openapi.json`, the `/docs` page and `/schemas` that `APIConfig.OpenAPI`
serves, and paths that match nothing (a `401`, not a `404`); only the
`/healthz` and `/readyz` probes answer without credentials. Return an [`api.Claims`](../../../go/transport/api/mw_auth.go),
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
Www-Authenticate: Bearer
X-Request-ID: 6d4a0f0e8c2b4b1e9f3a7c5d2e1b0a94

{"status":401,"code":"unauthenticated","message":"missing bearer token"}
```

The challenge is `Bearer` unless `api.AuthChallenge` names another
scheme. The refusal is counted by code in the HTTP refusal metrics and
recorded in the audit trail.

With a token, the call runs and is attributed to `alice` of `acme`:

```bash
curl -s http://10.0.0.5:8080/v1/commands/widget/list \
  -H 'Authorization: Bearer t0k3n-alice'
```

```json
{"exit_code":0,"stdout":"widget-1\nwidget-2\n"}
```

`Scopes` are what a `kit/permissions` annotation is checked against:
kit refuses a command to a caller whose scopes do not cover it (step
5). They also reach your own permission decision as
`Meta.Extra["scopes"]`, comma-joined.

To publish the OpenAPI document to callers without a token, admit
that path in your `AuthFunc`; returning no error with nil claims lets
the request through unattributed:

```go
func authenticate(r *http.Request) (any, error) {
    if r.Method == http.MethodGet && r.URL.Path == "/openapi.json" {
        return nil, nil // the spec is public; every command still needs a token
    }
    // ... verify the bearer token as above
}
```

A command annotated `kit/auth-required` runs only for a request your
`AuthFunc` accepted, on every address, loopback included: a loopback
listener is reachable by every local user, and an `Authorization`
header nobody verified is not authentication. Without `Auth` such a
command stays listed and answers every call with `401` and
`WWW-Authenticate`:

```http
HTTP/1.1 401 Unauthorized
Www-Authenticate: Bearer

{"status":401,"code":"unauthenticated","message":"api: authentication required: ..."}
```

Returning no error is what counts as verified, so an `AuthFunc` that
admits a path anonymously (as above) admits it for auth-required
commands too; keep such exceptions to paths that run no command. Over
the Unix socket the owner-only file is the authentication, and such a
command runs for any caller who can open it.

**Let kit verify the token.** Instead of writing `Auth`, name a
verifier in configuration. It satisfies the refusal in step 2 the
same way, on the api, rpc and mcp services:

```yaml
services:
  all:
    auth:
      mode: oidc                         # or jwks, or jwt
      oidc:
        issuer: https://login.example.com/   # discovery finds the key set
        audience: https://api.example.com    # required: tokens minted for this API only
```

When `audience` is the service's own URL and the block names an
issuer, each of the api, rpc and mcp services also publishes its OAuth
protected resource metadata (RFC 9728) at
`/.well-known/oauth-protected-resource<path>`, and a refused request's
`WWW-Authenticate` names it, so an OAuth client finds the provider by
itself.

`jwks` names the key set by URL (`auth.jwks.url`) instead of
discovering it; `jwt` trusts the tool's own identity keypair
(`cli.WithIdentity`) and any public keys in
`auth.jwt.public_key_files`.

With `jwt` the tool issues its own tokens. `WithAPI` plus
`WithIdentity` mounts `token create`, which signs with the keypair,
and `token verify`, which checks a token the way a service would
(`--service rpc` for another one; exit `5` when refused):

```console
$ export TOKEN=$(mytool token create --sub ci-bot --tenant acme --scopes widgets:read --expires 1h)
$ curl -s https://api.example.com/v1/commands/widget/list -H "Authorization: Bearer $TOKEN"
$ mytool token verify "$TOKEN"
{
  "valid": true,
  "service": "api",
  "mode": "jwt",
  "sub": "ci-bot",
  ...
}
```

The scopes are written as `scope` (space-delimited, the claim OAuth
resource servers read) and as `scopes` (a list).

For machine callers that should not carry a JWT, `auth.mode: apikey`
checks kit-issued API keys. Add `cli.WithAPIKeys(cli.APIKeysConfig{})`
and import the store's driver (`_ "hop.top/kit/go/storage/kv/sqlite"`);
then `mytool token key create --sub ci-bot --scopes widgets:read`
prints a key once, callers send it as `X-API-Key`, `token key list`
shows every key's state, and `token key revoke <id>` stops one at
once. `token claims` still
prints an unsigned template for an external signer, and `token`
itself is never served: remote callers cannot mint tokens. The token's `sub` is the caller, its
`tenant` claim (`tenant_claim` to rename) the tenant, and its
`scope`, `scopes` or `scp` the scopes. A configured mode replaces
`APIConfig.Auth` for that service; with no mode, `Auth` applies. The
keys, defaults and refusals are in the contract's
[Bearer tokens](../../contracts/serve-lifecycle.md#bearer-tokens).

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

Same precedence: flag, then config key, then code. With it set and no
`--policy` named, `kit-default` does not apply: any caller the surface
admits may run the whole command tree, destructive commands included,
because nothing bounds what the permission gate allows.

The two are deliberately separate keys, because they waive different
things. `insecure_remote` says you accept unidentified callers;
`insecure_no_policy` says you accept unbounded ones. Setting one never
sets the other, so a config review can see exactly which of the two
you accepted. Under `insecure_remote` alone nobody is established, so
`kit-default` lets every caller read and refuses every write:

```console
$ mytool serve --addr 0.0.0.0:8080 --insecure-remote
$ curl -s -X POST http://10.0.0.5:8080/v1/commands/widget/add -d '{}' | jq -r .message
api: permission denied: cmdsurface: permission denied: widget add on rest: policy: write not allowed for widget add (policy kit-default: ...)
```

Loopback needs neither. Serving on `127.0.0.1` keeps
allow-by-default, with no policy and no opt-in, because that is the
development path and the caller is already on your machine. The
socket service needs neither for the same reason: it is loopback by
construction.

### 5. Require scopes, then wire a permission policy

Annotate a command with the scopes it needs. kit enforces the
annotation itself: on every served surface, a command declaring
`kit/permissions` runs only for a caller whose verified credential
holds every scope it names.

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
        cli.WithAPI(cli.APIConfig{Addr: "0.0.0.0:8080", Auth: authenticate}),
        cli.WithSocket(cli.SocketConfig{}),
    )
    widget := &cobra.Command{Use: "widget", Short: "Manage widgets"}
    widget.AddCommand(widgetPurgeCmd())
    root.Cmd.AddCommand(widget)

    if err := root.Execute(context.Background()); err != nil {
        log.Fatal(err)
    }
}
```

The scopes compared are the ones your `AuthFunc` returned in
`api.Claims.Scopes` (or a `scopes` entry of a claims map). Matching is
exact: `widgets:admin` does not imply `widgets:read`. List several,
comma-separated, and the caller needs all of them.

What a caller lacking the scope sees over REST — `403`, not `401`,
because bob is authenticated and the refusal is about what bob may do.
The `WWW-Authenticate` challenge ([RFC 6750](https://www.rfc-editor.org/rfc/rfc6750#section-3.1))
names the scope a token needs, so a client can ask its authorization
server for one that has it:

```bash
curl -si -X POST http://10.0.0.5:8080/v1/commands/widget/purge \
  -H 'Authorization: Bearer t0k3n-bob' \
  -H 'Content-Type: application/json' -d '{}'
```

```http
HTTP/1.1 403 Forbidden
Www-Authenticate: Bearer error="insufficient_scope", scope="widgets:admin"

{"status":403,"code":"insufficient_scope","message":"api: insufficient scope: cmdsurface: insufficient scope: widget purge on rest: missing scope widgets:admin"}
```

The other surfaces answer the same class in their own protocol:
Connect `permission_denied` over RPC, an MCP tool result with
`isError` and `_meta["hop.top/refusal"].code` of
`insufficient_scope` (over HTTP, a caller that sent a bearer token gets
this `403` and challenge instead, as the MCP authorization spec asks),
and `DENIED` over the socket. In Go,
`errors.Is(err, cmdsurface.ErrInsufficientScope)` holds, and so does
`errors.Is(err, cmdsurface.ErrPermissionDenied)`.

Whose scopes count depends on how the transport established the
caller:

| Caller | Scopes compared | A `kit/permissions` command |
|---|---|---|
| Verified by `Auth` (api, mcp, rpc), a socket `Auth`, a webhook or bus verifier | the credential's | runs when they cover the annotation |
| Established by the transport: the `0600` socket file, MCP over stdio, a cron schedule | none — the caller holds the owner's authority | runs |
| Unverified, including one naming itself in the request | none | refused `insufficient_scope` |
| The CLI itself | none | runs |

The socket and stdio rows are the owner: whoever can open an
owner-only socket or spawn the process could run `mytool widget purge`
directly with the same credentials. When you share a socket
deliberately, give it an authenticator and return the caller's scopes
from it:

```go
package main

import (
    "context"
    "log"
    "net"

    "hop.top/kit/go/console/cli"
    "hop.top/kit/go/transport/socket"
)

// identifyPeer names the local caller and the scopes it holds. Stand-in
// for your own lookup, e.g. from the peer's credentials on conn.
func identifyPeer(_ context.Context, _ net.Conn, _ socket.Request) (socket.Identity, error) {
    return socket.Identity{Principal: "ci", Scopes: []string{"widgets:read"}}, nil
}

func main() {
    root := cli.New(cli.Config{Name: "mytool", Version: "1.4.2"},
        cli.WithSocket(cli.SocketConfig{Path: "/run/mytool/shared.sock", Auth: identifyPeer}),
    )
    root.Cmd.AddCommand(widgetCmd())

    if err := root.Execute(context.Background()); err != nil {
        log.Fatal(err)
    }
}
```

A command that declares no `kit/permissions` is not checked for
scopes at all.

For anything a scope list cannot say — a suspended account, a tenant
boundary — install a decision of your own. `cli.WithPermission` runs
it inside the bridge on every kit-shipped service, after the scope
check, and it can only narrow: it is asked only about calls the scope
check admitted.

```go
package main

import (
    "context"
    "log"

    "hop.top/kit/go/console/cli"
    "hop.top/kit/go/transport/cmdsurface"
)

// notSuspended refuses callers your account system has suspended.
func notSuspended(_ context.Context, meta cmdsurface.Meta, _ *cmdsurface.Leaf) cmdsurface.PermissionDecision {
    if suspended(meta.Tenant, meta.Caller) {
        return cmdsurface.PermissionDecision{Reason: "account suspended"}
    }
    return cmdsurface.PermissionDecision{Allowed: true}
}

func main() {
    root := cli.New(cli.Config{Name: "mytool", Version: "1.4.2"},
        cli.WithAPI(cli.APIConfig{Addr: "0.0.0.0:8080", Auth: authenticate}),
        cli.WithSocket(cli.SocketConfig{}),
        cli.WithPermission(notSuspended),
    )
    root.Cmd.AddCommand(widgetCmd())

    if err := root.Execute(context.Background()); err != nil {
        log.Fatal(err)
    }
}
```

Its refusal is `403 permission_denied` with your reason:

```json
{"status":403,"code":"permission_denied","message":"api: permission denied: cmdsurface: permission denied: widget purge on rest: account suspended"}
```

Over the socket, without an authenticator, `caller` is what the
request claimed: a decision that trusts `Meta.Caller` there is
trusting the caller's word. Decide on what the transport verified, or
give the socket an authenticator:
`services.socket.auth.mode: peer` makes `Meta.Caller` the uid the
kernel reports (`uid:501`, or the user name with
`auth.peer.resolve_names`), and `SocketConfig.Auth` takes one you
write.

Discovery answers for the caller who asks. `GET /v1/commands` from a
request your `AuthFunc` verified lists what that caller may run: bob,
lacking `widgets:admin`, sees

```json
{"name": "widget purge", "invocable": false, "reason": "insufficient-scope"}
```

and a command your decision refuses him is listed with the reason
`permission-denied`. The MCP tool list leaves such tools off for him.
The listing asks the same gates a call would, charges nothing, and is
advisory: every call still meets every gate.

A request nobody verified — a loopback listener without `Auth` — gets
the shared listing, which cannot know who will call: a caller-specific
refusal leaves the command listed as invocable. A command your
decision refuses **for everyone** is different: return
`CallerIndependent: true` and the shared listing withholds it at mount
with the reason `permission-denied`, exactly as it withholds an
interactive command.

The tool's policy engine is wired into the same gate, between the
scope check and your decision, and naming one replaces the
`kit-default` rules from step 2. A `--policy` that refuses a
side-effect class refuses it on every surface, before your decision is
asked, and discovery reflects it the same way:

```console
$ mytool serve api --policy=readonly
$ curl -s http://127.0.0.1:8080/v1/commands | jq '.commands[] | select(.name=="widget purge")'
```

```json
{"name": "widget purge", "side_effect": "write", "invocable": false, "reason": "permission-denied"}
```

A class is refused by its legacy name or its exact tier: `write: []`
refuses `write`, `write-local` and `write-shared` commands alike.

#### Different rules for different callers

A policy applies the same rules to everyone until you give it a
`callers` section. Each rule names who it is for — a principal, a
tenant, a scope the caller's credential holds, any of them, as globs —
and answers for the side-effect classes it declares. The first rule
that matches a caller answers; the policy's own `allow` answers the
rest, and answers every caller nobody verified.

```yaml
# ~/.config/mytool/policies/team.yaml
name: team
allow:
  write: []                 # nobody writes by default
  destructive: []
callers:
  - principal: "svc-*"      # service accounts write, 100 calls an hour
    allow:
      write: ["*"]
    max_ops: 100
    window: 1h
  - scope: widgets:admin    # holders of the admin scope may purge
    allow:
      write: ["*"]
      destructive: ["widget purge"]
```

```console
$ mytool serve api --policy=team
```

`max_ops` gives each matching principal, per tenant, a budget of write
and destructive calls per window. Only a call every permission decider
admits — the policy, its `permissions:` rules and your
`cli.WithPermission` gate — spends from it. It is counted in
`$XDG_STATE_HOME/mytool/usage.db`, so restarting the server does not
reset it; pass `cli.WithUsageStore(store)` to count in a store several
instances share. A caller who has spent it is refused until the window
resets:

```json
{"status":403,"code":"permission_denied","message":"api: permission denied: cmdsurface: permission denied: widget add on rest: policy: max_ops budget of 100 per 1h0m0s spent; resets at 2026-09-28T15:00:00Z"}
```

The rule decides inside the command too: the served run knows who it
runs for, so the `--policy` check a command makes asks for the same
caller. On the CLI, where there is no established caller, the policy's
own rules apply, and `max_ops` stays a per-invocation cap.

#### Write permission rules in the policy file

Rules that depend on the call — who, from where, with which arguments —
can live in the same policy file instead of in Go. Wire the evaluator
once:

```go
package main

import (
    "context"
    "log"

    "hop.top/kit/go/console/cli"
    "hop.top/kit/go/console/cli/celpermission"
)

func main() {
    root := cli.New(cli.Config{Name: "mytool", Version: "1.4.2"},
        cli.WithPolicy(cli.DefaultPolicyLoader("mytool")),
        cli.WithAPI(cli.APIConfig{Addr: "0.0.0.0:8080", Auth: authenticate}),
        celpermission.With(),
    )
    root.Cmd.AddCommand(widgetCmd())

    if err := root.Execute(context.Background()); err != nil {
        log.Fatal(err)
    }
}
```

Then add a `permissions:` block beside the policy's other keys. Each
rule is a [CEL](https://cel.dev) expression, the same rule language
kit's bus guards use:

```yaml
# ~/.config/mytool/policies/ops.yaml
allow:
  destructive: []
permissions:
  - name: tenant-boundary
    when: principal.tenant == "acme"
    effect: allow
    otherwise: deny
    message: acme tenants only
  - name: bulk-needs-admin
    when: has(payload.flags.all) && !("widgets:admin" in principal.scopes)
    effect: deny
    otherwise: allow
    message: --all needs widgets:admin
  - name: writes-from-the-office
    when: resource.tier != "read" && !context.client_addr.startsWith("10.")
    effect: deny
    otherwise: allow
    message: writes only from the office network
```

```console
$ mytool serve api --policy=ops
```

What a rule can read:

| Binding | Value |
|---|---|
| `principal.id`, `principal.tenant` | the caller and tenant the transport recorded |
| `principal.scopes` | the verified credential's scopes; empty for anyone else |
| `principal.established`, `principal.source` | whether the transport established the caller; `verified`, `transport` or `none` |
| `resource.id`, `resource.path` | the command, `"widget purge"` and `["widget", "purge"]` |
| `resource.tier` | its side-effect tier: `read`, `write-local`, `write-shared`, `destructive-local`, `destructive-shared` |
| `context.surface` | `rest`, `mcp`, `rpc`, `socket`, ... |
| `context.client_addr` | the caller's IP, no port; empty over the socket and stdio |
| `payload.args`, `payload.flags` | the positional arguments, and the flags the caller set |

Any rule that denies refuses the call. A rule that cannot be evaluated
denies too, so guard a flag the caller may not have set with `has()`.
Rules run after the scope check and the policy's `allow` lists, and
before your `cli.WithPermission` decision: they can only narrow. The
refusal names the rule, over the wire and in the audit record:

```json
{"status":403,"code":"permission_denied","message":"api: permission denied: cmdsurface: permission denied: widget purge on rest: permission rule \"bulk-needs-admin\": --all needs widgets:admin"}
```

Every rule compiles when the service starts. One that does not refuses
the start with exit 2, naming it:

```console
$ mytool serve api --policy=ops
USAGE: service "api": USAGE: policy "ops": permission rule "tenant-boundary" does not compile: ERROR: <input>:1:... Syntax error: ...
$ echo $?
2
```

A policy that declares rules in a tool that does not wire
`celpermission.With()` is refused the same way, rather than served
without them. A decision costs a few microseconds per call; see the
[package contract](../../../go/console/cli/celpermission/README.md#contract)
for the budget.

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
{"at":"2026-09-04T16:03:13Z","path":"widget purge","surface":"rest","exit_code":0,"error":"cmdsurface: insufficient scope: widget purge on rest: missing scope widgets:admin","request_id":"0f2e4d6c8b1a4c3e9d7f5a2b1c0e8d94","caller":"bob","tenant":"acme"}
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

#### Secrets never reach a sink

Every record is redacted before any sink sees it; there is no switch
to turn that off. A flag's value is masked as `***REDACTED***` when
the flag is marked secret or its name reads as secret (`token`,
`password`, `secret`, `api-key`, `auth`, `cookie`, …), and every echo
of that value in stdout, stderr, or the error message is masked too.
Everything else is scanned with the
[redact](../reference/redact.md) default rules, which catch
credential-shaped values whatever the flag is called:

```json
{"invocation":{"path":["db","connect"],"flags":{"dsn":"***REDACTED***","region":"eu-west-1","token":"***REDACTED***"}},"result":{"exit_code":0,"stdout":"connected via ***REDACTED***\n"}}
```

Mark a flag whose name does not give it away:

```go
cmd.Flags().String("dsn", "", "database DSN, password included")
_ = cmdsurface.MarkFlagSecret(cmd.Flags(), "dsn")
```

An operator adds flags and content patterns per service, or for every
service under `services.all`; the service's own key wins, and a list
replaces rather than merges. The block can only add: it has no
`enabled` key, and an unknown key is refused at exit 2.

```yaml
# ~/.config/mytool/config.yaml
services:
  all:
    audit:
      redact:
        secret_flags: [conn]
        patterns: ['acme_[a-z0-9]{32}']
```

A field longer than 4 KiB is withheld whole rather than shipped
unscanned; `max_field_bytes` in the same block moves that limit. See
[the cmdsurface reference](../reference/cmdsurface.md#redaction) for
exactly what is scanned and what it costs.

#### Keep a tamper-evident trail

A JSON-Lines file proves nothing once someone with write access has
edited it. Name a chain instead, and every record carries the SHA-256
of the one before it, so an edit, a deletion, a reorder or an
insertion anywhere in the file is detectable afterwards. Mount the
command that checks it when you build the root:

```go
root := cli.New(cli.Config{Name: "mytool", Version: "1.4.2"},
    cli.WithAPI(cli.APIConfig{Addr: "0.0.0.0:8080", Auth: authenticate}),
    cli.WithSocket(cli.SocketConfig{}),
    cli.WithAuditCommand(), // <tool> audit verify
)
```

The operator turns the chain on in configuration, for every service or
per service (the service's list replaces the `services.all` one):

```yaml
services:
  all:
    audit:
      sinks: [chain]        # $XDG_STATE_HOME/mytool/audit.chain
```

Each record holds who ran what — surface, command, args, flags,
caller, tenant, request and trace ids — and the verdict, redacted like
every audit record and never the command's output:

```json
{"v":1,"seq":2,"at":"2026-09-28T05:04:25.638373Z","prev":"add2c04c…","rec":{"path":"item add","surface":"socket","exit_code":0,"request_id":"5af7d8ee…","args":["spring"],"flags":{"token":"***REDACTED***"}},"hash":"fc21a39a…"}
```

`audit verify` checks every configured chain, or one `--file`:

```console
$ mytool audit verify
FILE                                    STATUS    RECORDS  FIRST  HEAD  HEAD HASH  BREAK
/home/me/.local/state/mytool/audit.chain  tampered  1        1      1     add2c04c…  …/audit.chain:2: hash does not match the record's bytes: the record was edited
TAMPER_DETECTED: audit chain broken at …/audit.chain:2: hash does not match the record's bytes: the record was edited
$ echo $?
71
```

| Exit | Class | Means |
|---|---|---|
| 0 | `OK` | every chain holds |
| 71 | `TAMPER_DETECTED` | a record was edited, deleted, reordered or inserted; the break names file and line. Permanent: keep the file as evidence |
| 3 | `NOT_FOUND` | no chain is configured, or a configured one does not exist |
| 1 | `GENERIC` | a chain could not be read |

`audit verify` is kit-reserved, so every served surface lists it as
`management-only` and never runs it: a remote caller cannot ask the
tool to vouch for its own trail.

What a chain cannot show on its own: records cut from the end leave a
shorter chain that still holds. Record the `HEAD` hash somewhere the
tool cannot write (a ticket, another host) and compare it later.

Durability, rotation and retention are per entry:

| Key | Default | Effect |
|---|---|---|
| `path` | `$XDG_STATE_HOME/<tool>/audit.chain` | The active file. Services naming the same path share one chain; a second process opening it is refused at startup. |
| `fsync` | `never` | `never`: one `write(2)` per record, safe from a process crash, not from power loss. `always`: synced before the call returns (one fsync per record). A duration such as `1s`: synced at most that often. |
| `max_bytes` | `0` (never) | Rotate before the file would pass this size. The rotated file is renamed `<path>.<first seq>` and the chain continues across it. |
| `max_files` | `0` (keep all) | Rotated files kept; the oldest are deleted after a rotation. Verification then starts at the oldest kept record. |
| `on`, `surfaces`, `paths` | every outcome, surface and command | Filters, as in `cmdsurface.SinkSpec`. |

A write cut short by a crash leaves a final line with no newline;
`audit verify` reports it as a torn tail, not tampering, and the next
start moves it to `<path>.torn-<time>` before appending.

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
| `traceparent` | `Meta.TraceID`, `Meta.Traceparent` | the W3C trace-id field; `X-Trace-ID` is the fallback. A well-formed value also lands whole in `Meta.Traceparent` and reaches a subprocess as `TRACEPARENT` |
| `tracestate` | `Meta.Tracestate` | kept only beside a well-formed `traceparent` |
| `Idempotency-Key` | `Meta.IdempotencyKey` | makes the call replayable (below); also forwarded to the command's `--idempotency-key` flag when it has one, scoped to the caller the same way |

Over the socket the same values are request fields:

```console
$ echo '{"path":["widget","list"],"request_id":"req-42","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","idempotency_key":"8f1c2a"}' \
    | socat - UNIX-CONNECT:/tmp/mytool.sock
```

A caller that disconnects mid-command cancels the command's context
on both transports; a command that honors its context stops.

A caller that retries a write sends the same `Idempotency-Key` again.
If the first call finished, the retry gets its answer and nothing runs
twice:

```bash
curl -s -i -X POST http://10.0.0.5:8080/v1/commands/widget/add \
  -H 'Authorization: Bearer t0k3n-alice' \
  -H 'Idempotency-Key: 8f1c2a' -d '{"flags":{"name":"w1"}}'
```

```http
HTTP/1.1 200 OK
Idempotent-Replayed: true

{"exit_code":0,"stdout":"added w1\n"}
```

- The key is scoped to the caller: alice's key never answers bob. A
  caller your `Auth` verified gets its answer on any service. On the
  owner-only socket every caller is you, the owner, whatever name a
  request claims. A caller merely named in a request is scoped to that
  service and, when it names none, its client host, so unauthenticated
  callers behind one host or proxy share a scope: authenticate the
  service to separate them.
- The same key for a different command or different flags is refused
  `422 idempotency_key_reused`; a retry while the first call still runs
  is refused `409 idempotency_conflict`.
- Only a call that succeeded (exit code `0`) is recorded, so a failed
  call retried with its key runs again.
- A record replays for 24 hours; set `services.<svc>.idempotency.ttl`
  to change it, or `services.<svc>.idempotency.enabled: false` to run
  every call. The records live in `serve-idempotency.db` in the tool's
  state directory.

The rpc and mcp services and the socket take the key too; the
[contract](../../contracts/serve-lifecycle.md#idempotency) lists where
each carries it and how each marks a replay.

A command with its own `--idempotency-key` replay (`cli.WithIdempotencyStore`)
gets the key too, scoped the same way: a served key never replays a
record made on your own command line, and a command run by
`cmdsurface.SubprocessRunner` receives the scope through
`KIT_IDEMPOTENCY_SCOPE`.

### 8. Keep browsers out: Host, Origin, response headers

Loopback is not private from a browser. Any page the operator opens
can make the browser send requests to `127.0.0.1:8080`, and a page
that re-resolves its own name to `127.0.0.1` (DNS rebinding) can read
the answers too. Every kit HTTP listener — the api service, the mcp
service's HTTP transport, the rpc service — closes both paths by
default, each configured under its own `services.<svc>`:

- **Host check.** The `Host` header must name a host the listener
  answers for. A loopback bind answers to `localhost`, `127.0.0.1` and
  `[::1]` on any port (so `ssh -L` forwards keep working); a named or
  IP bind answers to that host. A wildcard bind (`0.0.0.0`, `::`,
  `:8080`) cannot know its names, so it checks nothing until you list
  them. The rebinding page arrives with its own name in `Host` and is
  refused:

  ```bash
  curl -s -i http://127.0.0.1:8080/v1/commands -H 'Host: attacker.example'
  ```

  ```http
  HTTP/1.1 403 Forbidden
  Content-Type: application/json
  X-Content-Type-Options: nosniff

  {"status":403,"code":"host_rejected","message":"host \"attacker.example\" is not served here"}
  ```

- **Origin check.** A `POST`, `PUT`, `PATCH` or `DELETE` a browser
  sends from another origin is refused with `403` and
  `origin_rejected`. Requests without `Origin` (curl, SDKs, other
  servers), same-origin requests, and `GET`/`HEAD`/`OPTIONS` pass.
  A local dev server on another port is another origin.

  ```bash
  curl -s -X POST http://127.0.0.1:8080/v1/commands/widget/add \
    -H 'Origin: https://attacker.example'
  ```

  ```json
  {"status":403,"code":"origin_rejected","message":"cross-origin request from \"https://attacker.example\" refused"}
  ```

- **Security headers.** Every response, refusals included, carries
  `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`
  and `Content-Security-Policy: default-src 'none'; frame-ancestors
  'none'; base-uri 'none'; form-action 'none'`. The OpenAPI `/docs`
  page keeps its own, looser policy. `Strict-Transport-Security` is
  sent only when the client reached the tool over TLS: on this
  server, or at a [trusted proxy](#behind-a-proxy-name-it) that
  forwarded `https`.

Health probes are answered before the Host check, so an orchestrator
addressing a pod by IP needs no entry.

On the mcp and rpc listeners the same checks run under
`services.mcp.*` and `services.rpc.*`. A refusal arrives in the
listener's protocol: a JSON-RPC error with a null id over MCP, a
Connect `permission_denied` over RPC, its message led by
`host_rejected` or `origin_rejected`.

Serving under a DNS name, or to a browser app on another origin, is
configuration:

```yaml
# ~/.config/mytool/config.yaml
services:
  api:
    addr: 0.0.0.0:8443
    host_check:
      allow: [api.example.com, "tool.internal:8443"]
    origin_check:
      allow: ["https://console.example.com"]
```

A `host_check.allow` entry without a port matches any port; with one,
only that port. `origin_check.allow` entries are bare origins,
`scheme://host[:port]`; anything else fails validation at exit `2`,
as does an unknown key in any of the three blocks. The list adds to
the listener's own hosts; it never removes `localhost` from a
loopback bind.

Any of the keys can be set once for every service under
`services.all`, and the service's own key wins. Lists replace; they
are never merged:

```yaml
services:
  all:
    host_check:
      allow: [tool.internal]
  api:
    host_check:
      allow: [api.example.com]   # api answers to this, not tool.internal
```

To turn a check off, set its block's `enabled: false`. An origin you
grant through `api.CORS` is only readable by that page; to let it
write, list it in `origin_check.allow` too.

### 9. Trace and measure served commands

Propagation needs nothing. To export spans and metrics, link the
provider once in `main`:

```go
root := cli.New(cfg,
    cli.WithAPI(apiCfg),
    cli.WithObservability(observability.NewServe()), // hop.top/kit/go/transport/observability
)
```

Then turn it on in configuration; both signals default to off:

```yaml
# ~/.config/mytool/config.yaml
services:
  all:
    tracing:
      enabled: true
      endpoint: http://127.0.0.1:4318   # your OTLP collector; this is the default
    metrics:
      enabled: true
```

The call from step 7 now produces one trace: the HTTP server span
(child of the caller's `traceparent`), an `invoke widget list` span
below it, and anything the command or a child process records below
that. Metrics count every verdict per service and surface, refusals by
code. To have Prometheus scrape them instead, set
`services.api.metrics.scrape.enabled: true` (and `exporter: none` to
push nothing); the endpoint skips authentication, so beyond loopback
it also needs `scrape.allow_remote: true`. Keys, span and instrument
names are in
[served-observability.md](../reference/served-observability.md).

### 10. Encrypt the connection: at a proxy, or on the listener

Everything above travels in plaintext unless something encrypts it.
Choose where TLS ends:

| | Proxy-terminated | Direct |
|---|---|---|
| Who holds the certificate | the proxy (nginx, Caddy, a cloud load balancer) | the tool, `services.<svc>.tls` |
| The tool's listener | plaintext, on loopback or a network only the proxy reaches | TLS only, HTTP/2 and HTTP/1.1 |
| Who authenticates callers | the tool's `Auth`; a client certificate the proxy checks never reaches kit | the tool: `Auth`, or the client certificate with `auth.mode: mtls` |
| `Strict-Transport-Security` | the proxy sends it, or the tool when it trusts the proxy | the tool sends it |
| The client address kit sees | the proxy, until you list it in `trusted_proxies` | the client |

**Behind a proxy**, keep the tool on `127.0.0.1` and point the proxy
at it, then [name it](#behind-a-proxy-name-it). A proxy on another host reaches
a non-loopback bind, which still needs `Auth` (or the `insecure_remote`
opt-in, when only the proxy can reach that network) and a policy.

#### Behind a proxy: name it

Until you say otherwise every call comes from the proxy: one rate-limit
bucket for everybody and the proxy's address in every audit record.
List the proxies whose forwarding headers you trust:

```yaml
# ~/.config/mytool/config.yaml
services:
  all:
    trusted_proxies: [127.0.0.1, "::1"]   # the proxy on this host
  api:
    trusted_proxies: [10.0.0.0/8]         # replaces the shared list for api
```

From a listed peer, kit reads `Forwarded`, else `X-Forwarded-For`,
else `X-Real-IP`, and walks right to left past every trusted hop to
the first address you do not trust: that is the client. It becomes
the rate limiter's key and `remote_addr` in the audit record, with
the proxy in `peer_addr`; an `X-Forwarded-Proto: https` from it turns
on HSTS. From any other peer the headers are ignored, so a client
cannot name itself. Have the proxy overwrite the header it sets and
strip the other: a request carrying `Forwarded` and `X-Forwarded-For`
that disagree is attributed to the proxy. The rules are in
[the contract](../../contracts/serve-lifecycle.md#client-address).

**On the listener**, name a certificate and key:

```yaml
# ~/.config/mytool/config.yaml
services:
  api:
    addr: 0.0.0.0:8443
    tls:
      cert_file: /etc/mytool/tls/server.crt   # PEM chain, leaf first
      key_file: /etc/mytool/tls/server.key
```

```bash
curl -s -i --cacert ca.crt https://api.example.com:8443/v1/commands/item/list \
  -H 'Authorization: Bearer t0k3n-alice'
```

```http
HTTP/2 200
content-type: application/json
strict-transport-security: max-age=31536000
x-content-type-options: nosniff

{"exit_code":0,"data":[{"name":"bolt"},{"name":"nut"}]}
```

The listener no longer answers plaintext (`400 Client sent an HTTP
request to an HTTPS server`), accepts TLS 1.2 and up
(`tls.min_version: "1.3"` raises the floor), and loads the files at
start, so restart after renewing them. The same keys under
`services.rpc` or `services.mcp` encrypt those listeners; the rpc
service then negotiates HTTP/2 for native gRPC by ALPN instead of
serving h2c. Put the keys under `services.all.tls` to encrypt every
listener with one certificate, and set `enabled: false` on a service
to leave it plaintext.

To have the tool obtain and renew its own certificate by ACME, name
the domains instead of files. The CA's TLS-ALPN-01 challenge is
answered on the listener itself, so it must be reachable on port 443
under each name:

```yaml
services:
  api:
    addr: 0.0.0.0:443
    tls:
      acme:
        domains: [api.example.com]
        email: ops@example.com
        # cache_dir defaults to <state dir>/mytool/acme; directory_url
        # to Let's Encrypt production (point it at staging to rehearse)
```

TLS proves the server to the client, not the client to the server, so
it does not satisfy step 2's refusal: a non-loopback address with TLS
and no `Auth` is still refused at exit `2`.

**Client certificates** are the exception. `auth.mode: mtls` makes the
certificate the credential, and it satisfies the refusal the way
`Auth` does:

```yaml
services:
  all:
    tls:
      cert_file: /etc/mytool/tls/server.crt
      key_file: /etc/mytool/tls/server.key
    auth:
      mode: mtls
      mtls:
        ca_file: /etc/mytool/tls/clients-ca.crt   # the CA your client certificates chain to
        principal: san                             # URI SAN, else DNS, else email; or cn
        tenant_oid: 2.5.4.11                       # OU; or tenant_san_pattern: '^spiffe://example\.org/tenant/([^/]+)/'
```

```bash
curl -s --cacert ca.crt --cert alice.crt --key alice.key \
  https://api.example.com:8443/v1/commands/item/list
```

The call is attributed to the certificate: the audit record reads
`"caller":"spiffe://example.org/tenant/acme/svc/alice","tenant":"acme"`,
and a `kit/auth-required` command admits it. A client that presents no
certificate is answered like a missing token, and the refusal is
audited:

```http
HTTP/2 401

{"status":401,"code":"unauthorized","message":"client certificate required"}
```

A certificate the CA bundle does not verify never gets that far: the
handshake fails, and the server logs it. The health probes answer
without a certificate, so an orchestrator needs none. Under `mtls` the
certificate is the only verifier; `APIConfig.Auth` is not consulted.

### 11. Bound how fast a caller may call

Every service listening beyond loopback counts each caller's calls in
a token bucket per side-effect tier, with no configuration. The
socket service, stdio, and a loopback bind leave it off. Once
permission has admitted a call, a call with no token left is refused
before it reaches storage, a person or the command:

```console
$ curl -si https://tool.example/v1/commands/widget/list -H 'Authorization: Bearer …'
HTTP/1.1 429 Too Many Requests
Retry-After: 1

{"status":429,"code":"rate_limited","message":"api: rate limited: cmdsurface: rate limited: widget list on rest: read tier; retry after 100ms"}
```

The same refusal is Connect `ResourceExhausted` with `Retry-After`
metadata over RPC, an `isError` result whose `_meta["hop.top/refusal"]`
is `{"code":"rate_limited","retry_after_ms":100}` over MCP, and
`RATE_LIMITED` with `retry_after_ms` on the socket. A client that
turns it into an exit status uses `64` (`RATE_LIMITED`). Every
refusal reaches the audit sinks as `cmdsurface.ErrRateLimited` and is
counted as `rate_limited` in the refusal metrics.

A caller is the principal and tenant your `Auth` verified; on a
transport that vouches only for the connection (the owner-only socket,
cron) it is the owner, one bucket per transport whatever name a request
claims; otherwise its client address (an IPv6 address by its `/64`);
without that, the surface, so bus calls share one bucket. Behind a reverse
proxy the client address is the proxy's until you
[list it in `trusted_proxies`](#behind-a-proxy-name-it). The defaults, per
caller:

| Tier | Commands | `per_minute` | `burst` |
|---|---|---|---|
| `read` | `kit/side-effect: read` | 600 | 60 |
| `write` | the write tiers, and commands that declare no tier | 120 | 20 |
| `destructive` | the destructive tiers | 12 | 3 |

Change them per service, or for every service under `services.all`;
each key resolves on its own:

```yaml
# ~/.config/mytool/config.yaml
services:
  all:
    rate_limit:
      write:
        per_minute: 60
        burst: 10
  api:
    rate_limit:
      read:
        burst: 200          # api only; per_minute still comes from the default
  socket:
    rate_limit:
      enabled: true         # on for the socket too
```

`enabled: false` lifts the limit; a `per_minute` or `burst` below 1
is refused at startup with exit `2`, as is an unknown key. Buckets
live in memory, one set per service, and start full on every restart.

To ship different numbers with the tool, set them in code; a
configured key still wins, key by key, and the code does not switch
the limit on where it is off:

```go
cli.New(cfg, cli.WithServeRateLimit(cmdsurface.RateLimit{
    Destructive: cmdsurface.RateRule{PerMinute: 6, Burst: 1},
}))
```

#### Cap how much a caller uses per hour or day

A rate limit bounds bursts and forgets on restart. A quota bounds
totals, and remembers: set how many calls, or how many bytes of
output, each caller may use per window, and kit counts them in
`$XDG_STATE_HOME/mytool/usage.db` (or the store
`cli.WithUsageStore` names), so a restart resets nothing. It is off
until you set a limit:

```yaml
# ~/.config/mytool/config.yaml
services:
  all:
    quota:
      window: 24h           # default 1h; days run midnight to midnight UTC
      ops: 10000            # calls per window
      bytes: 104857600      # output bytes per window (stdout, stderr, data)
      per: principal        # or tenant: a tenant's principals share one quota
  socket:
    quota:
      enabled: false        # not for the owner's socket
```

The caller is counted as the rate limit counts it: the principal and
tenant your `Auth` verified; on the owner-only socket, the owner, one
count whatever name a request claims; else the client address, else
the surface. Only
a call that ran successfully is counted, after it ran; a replay or a
result served from the cache is free. Once the window's quota is spent the next call
is refused until the window resets, and `Retry-After` says when:

```console
$ curl -si https://tool.example/v1/commands/widget/list -H 'Authorization: Bearer …'
HTTP/1.1 429 Too Many Requests
Retry-After: 2143

{"status":429,"code":"quota_exceeded","message":"api: quota exceeded: cmdsurface: quota exceeded: widget list on rest: 10000 of 10000 ops per 24h0m0s used; resets at 2026-09-29T00:00:00Z"}
```

Over RPC it is `ResourceExhausted`, over MCP an `isError` result with
`_meta["hop.top/refusal"]` of `{"code":"quota_exceeded","retry_after_ms":…}`,
and `QUOTA_EXCEEDED` with `retry_after_ms` on the socket; exit class
`64`. In Go, `errors.Is(err, cmdsurface.ErrQuotaExceeded)` holds, and
so does `errors.Is(err, cmdsurface.ErrRateLimited)`.

Mount the management verbs with `cli.WithQuotaCommand()` to read and
clear the counts. Like `audit verify` they are management-only: no
served surface reaches them.

```console
$ mytool quota show
SERVICE  CALLER                 OPS    MAX OPS  BYTES     MAX BYTES  WINDOW    RESETS
api      principal/alice/acme   10000  10000    52428800  104857600  24h0m0s   2026-09-29T00:00:00Z
$ mytool quota reset principal/alice/acme
reset 1 quota count(s)
$ mytool quota reset --all --service api
```

### 12. Bound slow clients and long commands

Every HTTP listener (api, rpc, mcp) gives a client 5s to send its
headers and its whole request, and 10s to receive a request/reply
answer. Stream responses — the projection's `/stream` routes, rpc
`InvokeStream`, the mcp endpoint — are exempt from the 10s, since a
stream outlives it by design. Change them per service or for all:

```yaml
# ~/.config/mytool/config.yaml
services:
  api:
    timeouts:
      read: 30s      # a slow upload
      write: 30s
```

Nothing bounds how long a command runs until you ask. Give one command
a deadline with its annotation, or every command of a service one with
`timeouts.command`:

```go
exportCmd.Annotations["kit/timeout"] = "2m" // cmdsurface.AnnotationTimeout
```

```yaml
services:
  all:
    timeouts:
      command: 30s   # commands without kit/timeout
```

The annotation wins. The deadline starts when the call is admitted to
run, so a person answering a confirmation does not spend it, and a
caller can only shorten it (a gRPC or Connect client's own timeout).
When it passes the command is canceled — cooperatively in process,
its whole process group in a subprocess — and the caller gets `504`
with code `deadline_exceeded` (Connect `DeadlineExceeded`, an MCP
`isError` result, the socket's `DEADLINE_EXCEEDED`); the audit record
carries `cmdsurface.ErrDeadlineExceeded`.

A command that routinely runs longer than 10s should be called on its
stream route, not given a longer write timeout: a request/reply
answer that takes longer than `write` is cut whatever its deadline.

### 13. Bound how many calls run at once

Every service, on loopback or beyond it, runs at most 32 calls at once
and queues 64 more, first come first served, with no configuration.
A call that finds every slot taken and the queue full is refused at
once rather than piling up:

```console
$ curl -si http://127.0.0.1:8080/v1/commands/widget/list
HTTP/1.1 503 Service Unavailable
Retry-After: 1

{"status":503,"code":"overloaded","message":"api: overloaded: cmdsurface: overloaded: widget list on rest: 1 running and 64 queued; retry after 1s"}
```

The same refusal is Connect `Unavailable` with `Retry-After` metadata
over RPC, an `isError` result whose `_meta["hop.top/refusal"]` is
`{"code":"overloaded","retry_after_ms":1000}` over MCP, and
`OVERLOADED` with `retry_after_ms` on the socket; a stream route
answers it before the stream opens. A client that turns it into an
exit status uses `6` (`TRANSIENT`). It reaches the audit sinks as
`cmdsurface.ErrOverloaded` and is counted as `overloaded`. The retry
hint is how long the queue ahead would take to drain, from recent run
times: between 1s and 30s.

How many actually run at once depends on how your tool builds its
tree. Without `cli.WithRootFactory` every call runs on one shared
tree, one at a time: the service runs one and queues the rest, as the
`1 running` above shows. With a root factory, up to `max_inflight`
run in parallel. Either way the time a call waits counts against its
command deadline (step 12), and a caller that disconnects gives its
place up.

```yaml
# ~/.config/mytool/config.yaml
services:
  all:
    concurrency:
      max_inflight: 8     # with a root factory; a shared tree runs one
      max_queue: 16
  socket:
    concurrency:
      max_queue: 0        # socket only: refuse at once, never wait
```

`enabled: false` lifts the bound, and callers wait for the runner as
they did before it existed. A `max_inflight` below 1, a `max_queue`
below 0, or an unknown key is refused at startup with exit `2`. The
running and waiting counts are the `kit.serve.requests.active` and
`kit.serve.requests.queued` gauges ([served
observability](../reference/served-observability.md)).

## Option reference

| Option | Default | Effect |
|---|---|---|
| `APIConfig.Addr` | `127.0.0.1:8080` | Listen address. Non-loopback needs `Auth` or `InsecureRemote`. |
| `APIConfig.Auth` | none | Authenticates every route (OpenAPI document, docs and unmatched paths included; not the health probes) and permits any address. Claims attribute the call. |
| `APIConfig.InsecureRemote` | `false` | Serve unauthenticated beyond loopback. `services.api.insecure_remote` / `--insecure-remote` set the same. |
| `APIConfig.InsecureNoPolicy` | `false` | Beyond loopback with no `--policy`, serve with no policy instead of `kit-default`. `services.api.insecure_no_policy` / `--insecure-no-policy` set the same. |
| `APIConfig.MaxBodyBytes` | `0` (1 MiB) | Request body cap on every api route; over it is `413 body_too_large`, audited as `cmdsurface.ErrBodyTooLarge`. Negative disables. `services.api.body_limit.max_bytes` / `.enabled`, then `services.all.body_limit.*`, override it. |
| `SocketConfig.Auth` | none | Verifies each socket request; the verified identity, and its `Scopes`, replace the claimed one. |
| `services.socket.auth.mode: peer` | unset | Names each socket caller by its kernel-reported uid, verified; replaces `SocketConfig.Auth`. `auth.peer.require_same_uid` refuses other uids, `auth.peer.resolve_names` uses the user name. A peer holds no scopes until `auth.peer.scopes` or `SocketConfig.PeerScopes` grants them. Linux, macOS, FreeBSD. |
| `kit/permissions` annotation | none | Scopes a verified caller must all hold; otherwise `403 insufficient_scope`. The owner (socket file, stdio, CLI) is not asked. |
| `cli.WithPermission(fn)` | permit all | Permission decision on every kit-shipped transport service, after the scope check and `--policy`; can only narrow. |
| `cli.WithAuditSinks(specs...)` | none | Audit sinks on every kit-shipped transport service. Records are always redacted. |
| `services.<svc>.audit.redact.secret_flags` | none | Extra flag names masked in audit records; `services.all` applies to every service. |
| `services.<svc>.audit.redact.patterns` | none | Extra content patterns (RE2) masked in audit records. |
| `services.<svc>.audit.redact.max_field_bytes` | `4096` | Longest field the content rules scan; a longer one is withheld from the record whole. |
| `services.<svc>.audit.sinks` | none | Audit sinks from configuration; `[chain]` appends to a tamper-evident log. See [Keep a tamper-evident trail](#keep-a-tamper-evident-trail). |
| `cli.WithAuditCommand()` | not mounted | Mounts `<tool> audit verify` (exit 71 `TAMPER_DETECTED` on a broken chain); management-only when served. |
| `cli.WithObservability(p)` | none | Links a tracing and metrics provider; `services.<svc>.tracing.enabled` / `.metrics.enabled` (or `services.all.*`) turn it on. |
| `services.api.metrics.scrape.enabled` | `false` | Answer a Prometheus scrape at `/metrics`, after the Host check, before auth. Beyond loopback needs `services.api.metrics.scrape.allow_remote: true`. |
| `--policy=<name>` | `kit-default` beyond loopback, none on loopback | The tool's policy engine, applied to remote calls; its `callers` section answers per caller. `kit-default` is reserved for the shipped policy. |
| `cli.WithUsageStore(store)` | `$XDG_STATE_HOME/<tool>/usage.db` | Where quotas and the `max_ops` budgets of a policy's caller rules are counted. |
| `services.api.host_check.enabled` | `true` | Refuse a `Host` the listener does not answer for (`403`, `host_rejected`). |
| `services.api.host_check.allow` | `[]` | Hosts accepted beyond the listener's own; `name` or `name:port`. Required for a wildcard bind to check anything. |
| `services.api.origin_check.enabled` | `true` | Refuse cross-origin browser writes (`403`, `origin_rejected`). |
| `services.api.origin_check.allow` | `[]` (same-origin only) | Cross-origin browser origins permitted to write, `scheme://host[:port]`. |
| `services.api.security_headers.enabled` | `true` | `nosniff`, `no-referrer`, a deny-all CSP; HSTS over TLS only, here or at a trusted proxy. |
| `services.<svc>.trusted_proxies` | `[]` | CIDRs and addresses of the proxies whose `Forwarded`, `X-Forwarded-For`, `X-Real-IP` and `X-Forwarded-Proto` are believed. Empty believes none. |
| `services.<svc>.tls.cert_file`, `.key_file` | unset | Serve TLS only, HTTP/2 and HTTP/1.1, with this PEM chain and key. `tls.enabled: false` turns it off. |
| `services.<svc>.tls.min_version` | `1.2` | `1.2` or `1.3`. |
| `services.<svc>.tls.acme.domains` | unset | Obtain and renew the certificate by ACME for these names; `.email`, `.cache_dir`, `.directory_url` tune it. |
| `services.<svc>.auth.mode` | unset | `mtls`: the client certificate is the credential; `jwt`, `jwks`, `oidc`: a verified bearer token is. Counts as authentication beyond loopback, and replaces the code `Auth`. |
| `services.<svc>.auth.jwt.public_key_files` | unset | PEM public keys trusted beside the tool's identity key under `jwt`. |
| `services.<svc>.auth.jwks.url` / `auth.oidc.issuer` | unset | The key set, or the provider whose discovery yields it; `https` (or loopback `http`). |
| `services.<svc>.auth.apikey.backend`, `.path` | `sqlite`, `<data dir>/<tool>/apikeys.db` | The store `auth.mode: apikey` checks keys against; `cli.WithAPIKeys` mounts `token key create|list|revoke`. |
| `services.<svc>.auth.<mode>.audience`, `.issuer`, `.clock_skew`, `.refresh`, `.tenant_claim` | audience required under `jwks`/`oidc`; skew `1m`; refresh `1h`; tenant claim `tenant` | Claim checks and key-set caching for the bearer modes. |
| `services.<svc>.auth.mtls.ca_file` | unset | CA bundle client certificates must chain to; required under `mtls`. |
| `services.<svc>.auth.mtls.principal` | `san` | `san`, `san_uri`, `san_dns`, `san_email` or `cn`. |
| `services.<svc>.auth.mtls.tenant_oid` / `.tenant_san_pattern` | unset | Where the tenant comes from: a subject attribute or extension OID, or a SAN regular expression (first capture group). One or the other. |
| `services.<svc>.timeouts.read_header` / `.read` / `.write` / `.idle` | `5s` / `5s` / `10s` / read | HTTP listener timeouts (api, rpc, mcp); stream responses are exempt from `write`. `0` is none. |
| `services.<svc>.timeouts.command` | none | Per-command deadline for commands without `kit/timeout`; past it, `504 deadline_exceeded`. Reaches the socket too. |
| `services.all.<block>.<key>` | unset | Shared default for the blocks above; the service's own key wins. |
| `services.<svc>.rate_limit.enabled` | on beyond loopback, off on loopback, the socket and stdio | Per-caller rate limit; over it is `429 rate_limited` with `Retry-After`, audited as `cmdsurface.ErrRateLimited`. |
| `services.<svc>.quota.ops` / `.bytes` | unset (no quota) | Calls, and output bytes, each caller may use per window; over either is `429 quota_exceeded` with `Retry-After` at the window's reset. Counted in the usage store, so restarts keep them. `enabled: false` switches a set quota off. |
| `services.<svc>.quota.window` / `.per` | `1h` / `principal` | The window (aligned to the epoch; `24h` is a UTC day) and what is counted: `principal` or `tenant`. |
| `cli.WithQuotaCommand()` | not mounted | Mounts `<tool> quota show` and `quota reset`; management-only when served. |
| `services.<svc>.rate_limit.<tier>.per_minute` | read `600`, write `120`, destructive `12` | Tokens a caller's bucket for the tier refills per minute. `services.all.rate_limit.*` applies to every service. |
| `services.<svc>.rate_limit.<tier>.burst` | read `60`, write `20`, destructive `3` | Tokens the bucket holds at most. |
| `services.<svc>.concurrency.enabled` | `true`, on loopback and beyond | Bound calls running at once; past the queue is `503 overloaded` with `Retry-After`, audited as `cmdsurface.ErrOverloaded`. |
| `services.<svc>.concurrency.max_inflight` | `32` | Calls running at once; one at a time over a shared tree (no `cli.WithRootFactory`). |
| `services.<svc>.concurrency.max_queue` | `64` | Calls waiting for a slot, first come first served; `0` refuses at once. |

Precedence for either opt-in is flag, then config key, then code.
The guard keys have no flags: the service's key, then
`services.all`, then the default. The
socket path, exposure patterns, and destructive policy are documented
in their own guides and are unchanged.

## What it does not implement

Absence here is deliberate; each of these belongs somewhere else:

- **An identity provider.** Kit verifies tokens (`auth.mode: jwt`,
  `jwks`, `oidc`) but runs no login, consent or token endpoint; a
  bearer mode has no revocation list beyond expiry (a code verifier
  from `go/transport/authn` takes a `Check` hook for one). With
  `auth.mode: mtls` kit verifies the certificate chain against your
  CA bundle; issuing and revoking certificates stays with you (no CRL
  or OCSP check).
- **A tenant registry.** `Meta.Tenant` is a label your claims
  supply. Nothing scopes state by it.
- **Socket peer credentials on Windows and most BSDs.**
  `services.socket.auth.mode: peer` names each socket caller by the
  uid the kernel reports, on Linux, macOS and FreeBSD only; elsewhere
  it is refused at start. See
  [serve-cli-over-unix-socket.md](serve-cli-over-unix-socket.md#12-know-who-is-calling).
- **Conflict detection across processes.** Replicas that share a
  store (`cli.WithServeIdempotencyStore`) replay each other's records,
  but a call still running is known only to its own process: the same
  key sent to two replicas at the same moment runs on both.
- **Forced remote execution.** Interactive commands, destructive
  commands the policy withholds, and commands the permission gate
  refuses stay refused. There is no override.

## Related pages

- [expose-cli-over-rest.md](expose-cli-over-rest.md) — the api
  service: routes, discovery, destructive commands, confirmation
- [serve-cli-over-unix-socket.md](serve-cli-over-unix-socket.md) —
  the socket service: wire format, permissions, restrictions
- [served-observability.md](../reference/served-observability.md) —
  tracing and metrics keys, spans and instruments
- [serve-lifecycle contract](../../contracts/serve-lifecycle.md#security)
  — the normative rules this guide applies
- [api README](../reference/transport-api.md#auth) — claims
  shapes, request provenance, refusal codes
- [socket README](../../../go/transport/socket/README.md) — request
  fields, error codes, the authenticator hook
- [cmdsurface README](../../../go/transport/cmdsurface/README.md) —
  the bridge, its gates, and the sinks
