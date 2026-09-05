# Migrate to served commands

You're here because your kit tool already serves something — a
hand-written `serve` command that starts its own HTTP server, or a
cobra tree you mount on REST and MCP through `cmdsurface` by hand — and
you want kit's built-in serve instead: the `serve` supervisor, the
`api` and `socket` services, the command projection, and the safety
defaults, with your mounting code gone. This page walks the diff for
both starting points.

## Why migrate

Every kit tool that serves implements the same five things:

1. A listener and an `http.Server`, with a shutdown on signal.
2. A startup line saying where it listens.
3. A flag or config key for the address.
4. A bridge from the command tree to a transport, with a policy on
   what may run remotely.
5. Discovery: some way for a caller to learn what is served.

Kit ships all five. Registering the `api` service is the whole setup:
`serve` supervises it, readiness is an event and a log line, the
address is a config key, every conformant command is projected under
`/v1/commands` behind the policy gate, and `GET /v1/commands` says
what is mounted and why the rest is not. The `socket` service gives
you the same tree over a local Unix socket with one more option. What
you delete is the part that was never specific to your tool.

## Before you start

- **kit version**: `cli.WithSocket`, the loopback default and the
  unauthenticated-remote refusal ship together; the template that
  generates this wiring pins `hop.top/kit v0.5.0-alpha.3`.
- **A kit root**: the projection reflects `cli.New`'s tree and runs it
  through the gates `Root.Execute` installs. A raw `cobra.Command` root
  becomes a kit root first (path B below covers it).
- **Annotations decide everything**: `kit/side-effect` picks the HTTP
  method (`read` is `GET`, everything else `POST`) and what is withheld
  (`destructive` until you name a surface, `interactive` always);
  `kit/self-hosting: true` marks a command that must never run inside
  a served invocation. A kit root refuses to start when a leaf lacks
  `kit/side-effect`, `kit/idempotent`, `Short` or `Long`.
- **The contract**: the observable changes are enumerated in the
  [serve-lifecycle contract's Compatibility section](../../contracts/serve-lifecycle.md#compatibility).
  This page tells you what to do about each; it does not restate the
  rules.

## The mechanical migration

### Path A — from a hand-written `serve` leaf

Your old tool: a kit root, a `widget` command, and a `serve` leaf that
builds a mux, binds `--addr`, prints `Listening on`, and blocks.

```go
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "os"

    "github.com/spf13/cobra"

    "hop.top/kit/go/console/cli"
)

func main() {
    root := cli.New(cli.Config{Name: "mytool", Version: "1.4.2", Short: "Manage widgets"},
        cli.WithStatus(cli.StatusConfig{}),
    )
    root.Cmd.AddCommand(widgetCmd(), serveCmd())
    if err := root.Execute(context.Background()); err != nil {
        os.Exit(1)
    }
}

// serveCmd is the hand-written server: its own listener, its own
// routes, its own startup line, its own flag.
func serveCmd() *cobra.Command {
    cmd := &cobra.Command{
        Use:   "serve",
        Short: "Start the HTTP server",
        Long:  "Start the HTTP server and block until interrupted.",
        Args:  cobra.NoArgs,
        RunE: func(cmd *cobra.Command, _ []string) error {
            addr, _ := cmd.Flags().GetString("addr")
            mux := http.NewServeMux()
            mux.HandleFunc("GET /health", healthHandler)
            srv := &http.Server{Addr: addr, Handler: mux}
            go func() {
                <-cmd.Context().Done()
                _ = srv.Shutdown(context.Background())
            }()
            fmt.Fprintf(cmd.ErrOrStderr(), "Listening on %s\n", addr)
            if err := srv.ListenAndServe(); err != http.ErrServerClosed {
                return err
            }
            return nil
        },
    }
    cmd.Flags().String("addr", ":8080", "Listen address")
    cli.SetSideEffect(cmd, cli.SideEffectWriteShared)
    cli.SetIdempotency(cmd, cli.IdempotencyNo)
    cli.SetTopLevelVerb(cmd)
    return cmd
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
    _ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func widgetCmd() *cobra.Command {
    widget := &cobra.Command{Use: "widget", Short: "Manage widgets"}
    list := &cobra.Command{
        Use:   "list",
        Short: "List widgets",
        Long:  "List every widget.",
        Args:  cobra.NoArgs,
        RunE: func(cmd *cobra.Command, _ []string) error {
            fmt.Fprintln(cmd.OutOrStdout(), "w-1  bolt")
            return nil
        },
    }
    cli.SetSideEffect(list, cli.SideEffectRead)
    cli.SetIdempotency(list, cli.IdempotencyYes)
    widget.AddCommand(list)
    return widget
}
```

#### Step 1 — Delete the leaf, register the api service

Remove `serveCmd` and its `AddCommand`; add `cli.WithAPI` to the root.
Your routes move into `APIConfig.Handlers`, which runs when the
service starts and mounts them ahead of the projection, so every path
you already serve keeps winning.

```go
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "os"

    "github.com/spf13/cobra"

    "hop.top/kit/go/console/cli"
    "hop.top/kit/go/transport/api"
)

func main() {
    root := cli.New(cli.Config{Name: "mytool", Version: "1.4.2", Short: "Manage widgets"},
        cli.WithStatus(cli.StatusConfig{}),
        // The api service owns the listener, the lifecycle, and the
        // startup trace. Your routes ride on it.
        cli.WithAPI(cli.APIConfig{
            Handlers: func(r *api.Router) {
                r.Handle("GET", "/health", healthHandler)
            },
        }),
    )
    root.Cmd.AddCommand(widgetCmd())
    if err := root.Execute(context.Background()); err != nil {
        os.Exit(1)
    }
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
    _ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func widgetCmd() *cobra.Command {
    widget := &cobra.Command{Use: "widget", Short: "Manage widgets"}
    list := &cobra.Command{
        Use:   "list",
        Short: "List widgets",
        Long:  "List every widget.",
        Args:  cobra.NoArgs,
        RunE: func(cmd *cobra.Command, _ []string) error {
            fmt.Fprintln(cmd.OutOrStdout(), "w-1  bolt")
            return nil
        },
    }
    cli.SetSideEffect(list, cli.SideEffectRead)
    cli.SetIdempotency(list, cli.IdempotencyYes)
    widget.AddCommand(list)
    return widget
}
```

The whole diff is in [Concrete diffs](#concrete-diffs). Exactly one
command owns the `serve` word: the option mounts kit's, and yours must
go. A leaf you keep adding after `cli.New` is a second owner, which
the contract forbids.

#### Step 2 — Move the address into configuration

`--addr` keeps working on `serve` — it is the documented exception for
adopters whose scripts pass it — but its value has moved: the listen
address is now `services.api.addr`, and its default is loopback.

| Before | After |
|--------|-------|
| `cmd.Flags().String("addr", ":8080", ...)` on your leaf | `services.api.addr` in the config file; `--addr` on `serve` still overrides it |
| a hard-coded `":8080"` | `services.api.addr: 127.0.0.1:8080`, or `Auth`, or `services.api.insecure_remote: true` — see step 4 |
| `MYTOOL_ADDR` or your own env var | `MYTOOL_SERVICES_API_ADDR` (kit's `<TOOL>_` prefix, dots as underscores) |
| your own enable/disable switch | `services.api.enabled` — `true` by default for `WithAPI`, an explicit `false` wins |
| your own timeouts | `services.api.ready_timeout`, `services.api.stop_timeout`, `services.shutdown_timeout` |

```yaml
# ~/.config/mytool/config.yaml
services:
  api:
    addr: 127.0.0.1:8080
```

This only takes effect if the file reaches `root.Viper`, which is what
`serve` reads. Layer it in a `PrePersistentRunE` hook with
`config.OptionsForTool` and `config.BindEnv`; the
[cli-go template's `cmd/root.go`](../../../templates/cli-go/cmd/root.go.tmpl)
is the reference wiring.

#### Step 3 — Replace the startup line

The leaf printed `Listening on :8080`. Nothing prints that now. The api
service reports readiness through `kit.serve.service.ready_reported`
and its log counterpart, and both carry the resolved address under a
structured key — which for a `:0` address is the only place the bound
port is knowable:

```console
$ mytool serve --addr 127.0.0.1:0
INFO serve: started object=service elapsed_ms=0 service=api
INFO serve: ready_reported object=service elapsed_ms=1 service=api address=127.0.0.1:63911
INFO serve: ready_reported object=supervisor elapsed_ms=1
```

A script that scraped the literal string reads the `address=` field of
the `ready_reported` line instead. A program that needs the address —
to print a startup document for a sidecar, say — subscribes to the
event through `cli.WithServiceBus`:

```go
package migrate

import (
    "context"
    "fmt"
    "os"

    "hop.top/kit/go/console/cli"
    "hop.top/kit/go/console/serve"
    "hop.top/kit/go/runtime/bus"
)

// announce replaces the "Listening on" line. It prints one JSON line
// on stdout when the api service reports ready, with the address the
// event carries.
func announce(b bus.Bus) func(*cli.Root) {
    b.Subscribe("kit.serve.service.ready_reported", func(_ context.Context, e bus.Event) error {
        if p, ok := e.Payload.(serve.EventPayload); ok && p.Service == cli.APIServiceName {
            fmt.Fprintf(os.Stdout, `{"address":%q}`+"\n", p.Address)
        }
        return nil
    })
    return cli.WithServiceBus(b)
}
```

Kit's own `kit serve` does exactly this to keep the startup JSON its SDK
sidecars read.

#### Step 4 — Decide who may reach it

The api service binds `127.0.0.1:8080` and refuses, at the
configuration gate, to serve unauthenticated anywhere else. If your
leaf listened on `:8080`, the first `serve` after migration says:

```console
$ mytool serve --addr :8080
USAGE: service "api": addr: ":8080" is not a loopback address and the api service has no authentication; set APIConfig.Auth, listen on 127.0.0.1, or set services.api.insecure_remote: true (or --insecure-remote) to serve unauthenticated beyond loopback
```

Pick one of the three, in this order of preference: set
`APIConfig.Auth` ([secure-remote-serving.md](secure-remote-serving.md)
walks it), keep loopback, or opt into the old exposure by name with
`services.api.insecure_remote: true`. The opt-in is a config key so a
reviewer can find every such deployment.

#### Step 5 — Keep the contract's exit codes

The refusal above is exit `2`. A `main` that answers every error with
`os.Exit(1)` flattens it. Map kit's structured errors:

```go
package migrate

import (
    "errors"

    "hop.top/kit/go/console/output"
)

// exitCode keeps the taxonomy the contract assigns: a refusal at the
// configuration gate is 2, an unknown service 3, a policy denial 5.
func exitCode(err error) int {
    var kitErr *output.Error
    if errors.As(err, &kitErr) && kitErr.ExitCode != 0 {
        return kitErr.ExitCode
    }
    return 1
}
```

### Path B — from manual `cmdsurface` mounting

Your old tool: a raw cobra tree, a mode switch on `os.Args`, a bridge,
`Expose`, `MountREST`, `MountMCP`, and `http.ListenAndServe` on
`:8080`.

```go
package main

import (
    "fmt"
    "log"
    "net/http"
    "os"

    "github.com/spf13/cobra"

    "hop.top/kit/go/transport/api"
    "hop.top/kit/go/transport/cmdsurface"
)

func main() {
    root := buildTree()

    // CLI mode: arguments beyond the program name run the tree locally.
    if len(os.Args) > 1 {
        if err := root.Execute(); err != nil {
            os.Exit(1)
        }
        return
    }

    // Server mode: bridge the tree, mount it on REST and MCP by hand,
    // serve on :8080.
    b := cmdsurface.New(root)
    b.Expose("*", cmdsurface.SurfaceREST, cmdsurface.SurfaceMCP)

    r := api.NewRouter(api.WithMiddleware(api.RequestID(), api.Recovery(nil)))
    if err := cmdsurface.MountREST(b, r); err != nil {
        log.Fatal(err)
    }
    if err := cmdsurface.MountMCP(b, r, cmdsurface.WithMCPServerInfo("mytool", "1.4.2")); err != nil {
        log.Fatal(err)
    }
    log.Printf("Listening on :8080")
    log.Fatal(http.ListenAndServe(":8080", r))
}

func buildTree() *cobra.Command {
    root := &cobra.Command{Use: "mytool", Short: "Manage widgets"}
    widget := &cobra.Command{Use: "widget", Short: "Manage widgets"}
    list := &cobra.Command{
        Use:   "list",
        Short: "List widgets",
        RunE: func(cmd *cobra.Command, _ []string) error {
            fmt.Fprintln(cmd.OutOrStdout(), "w-1  bolt")
            return nil
        },
        Annotations: map[string]string{"kit/side-effect": "read"},
    }
    remove := &cobra.Command{
        Use:   "delete <id>",
        Short: "Delete a widget",
        Args:  cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            fmt.Fprintf(cmd.OutOrStdout(), "deleted widget %s\n", args[0])
            return nil
        },
        Annotations: map[string]string{"kit/side-effect": "destructive"},
    }
    widget.AddCommand(list, remove)
    root.AddCommand(widget)
    return root
}
```

#### Step 1 — Make the tree a kit root

`cli.New` replaces the bare root and the mode switch: `serve` is a
command now, so there is no argument-count test to write. Add
`cli.WithStatus` (a kit root is validated to carry it), give every leaf
a `Long`, and write the annotations through `cli.SetSideEffect` and
`cli.SetIdempotency` so the validator reads what the projection reads.

#### Step 2 — Drop the bridge; REST is the service's own

Delete `cmdsurface.New`, `Expose`, `MountREST`, the router, and
`ListenAndServe`. `cli.WithAPI` mounts the projection under
`/v1/commands` when the service starts. The route shape changes —
`MountREST` served `POST /cmd` with an envelope; the projection serves
one route per command with the method its side-effect class selects —
so a client of the old mount reads
[expose-cli-over-rest.md](expose-cli-over-rest.md) for the new one.

#### Step 3 — Keep MCP on the service's router

MCP is not a kit-shipped service yet, so it keeps its mount — through
`Handlers`, over a bridge you build when the service starts. That
moment matters: the tree is complete only then, and `Root.Execute` has
installed the confirmation and policy gates on it, so the bridge runs
gated commands.

```go
package main

import (
    "context"
    "fmt"
    "log"
    "os"

    "github.com/spf13/cobra"

    "hop.top/kit/go/console/cli"
    "hop.top/kit/go/transport/api"
    "hop.top/kit/go/transport/cmdsurface"
)

func main() {
    var root *cli.Root
    root = cli.New(cli.Config{Name: "mytool", Version: "1.4.2", Short: "Manage widgets"},
        cli.WithStatus(cli.StatusConfig{}),
        // REST is the api service's own projection. MCP is not a
        // kit-shipped service yet, so it keeps its mount — on the
        // service's router, over a bridge built when the service
        // starts, which is when the tree is complete and gated.
        cli.WithAPI(cli.APIConfig{
            Handlers: func(r *api.Router) {
                b := cmdsurface.New(root.Cmd)
                b.Expose("*", cmdsurface.SurfaceMCP)
                if err := cmdsurface.MountMCP(b, r, cmdsurface.WithMCPServerInfo("mytool", "1.4.2")); err != nil {
                    log.Fatal(err)
                }
            },
        }),
    )
    root.Cmd.AddCommand(widgetCmd())
    if err := root.Execute(context.Background()); err != nil {
        os.Exit(1)
    }
}

func widgetCmd() *cobra.Command {
    widget := &cobra.Command{Use: "widget", Short: "Manage widgets"}
    list := &cobra.Command{
        Use:   "list",
        Short: "List widgets",
        Long:  "List every widget.",
        Args:  cobra.NoArgs,
        RunE: func(cmd *cobra.Command, _ []string) error {
            fmt.Fprintln(cmd.OutOrStdout(), "w-1  bolt")
            return nil
        },
    }
    cli.SetSideEffect(list, cli.SideEffectRead)
    cli.SetIdempotency(list, cli.IdempotencyYes)
    remove := &cobra.Command{
        Use:   "delete <id>",
        Short: "Delete a widget",
        Long:  "Delete one widget by id.",
        Args:  cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            fmt.Fprintf(cmd.OutOrStdout(), "deleted widget %s\n", args[0])
            return nil
        },
    }
    cli.SetSideEffect(remove, cli.SideEffectDestructive)
    cli.SetIdempotency(remove, cli.IdempotencyYes)
    widget.AddCommand(list, remove)
    return widget
}
```

`var root *cli.Root` before the assignment is deliberate: the closure
reads `root` when the service starts, after `cli.New` returned.

Both surfaces are up on one listener, and the destructive leaf is
withheld from REST while MCP keeps listing it as a tool and refusing it
at call time under the bridge's default policy:

```console
$ mytool serve --addr 127.0.0.1:0
INFO serve: ready_reported object=service elapsed_ms=1 service=api address=127.0.0.1:64407
$ curl -s http://127.0.0.1:64407/v1/commands | jq -c '.commands[] | select(.name|startswith("widget")) | {name, invocable, reason}'
{"name":"widget list","invocable":true}
{"name":"widget delete","invocable":false,"reason":"unauthorized-destructive"}
$ curl -s -X POST http://127.0.0.1:64407/mcp -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' -d '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' | jq -c '[.result.tools[].name]'
["status","widget.delete","widget.list"]
```

#### Step 4 — Move the bridge policy onto the services

The bridge you built carried one `Policy` for every surface. Each
kit-shipped service now takes its own, and naming a surface widens that
surface only. `Hide` and `Expose` move the same way.

```go
package migrate

import (
    "hop.top/kit/go/console/cli"
    "hop.top/kit/go/transport/cmdsurface"
)

// serviceOptions carries what buildBridge used to decide: destructive
// commands may run over REST and the socket, and nothing under admin
// is reachable over REST.
func serviceOptions() []func(*cli.Root) {
    return []func(*cli.Root){
        cli.WithAPI(cli.APIConfig{
            Policy: cmdsurface.Policy{
                AllowDestructiveOn: []cmdsurface.Surface{cmdsurface.SurfaceREST},
            },
            Hide: []string{"admin *"},
        }),
        cli.WithSocket(cli.SocketConfig{
            Policy: cmdsurface.Policy{
                AllowDestructiveOn: []cmdsurface.Surface{cmdsurface.SurfaceRPC},
            },
        }),
    }
}
```

Lifting the ceiling is necessary and not sufficient: the command's own
confirmation gate still applies, and there is no TTY behind a request,
so an unconfirmed destructive call is refused by the command (exit `5`,
`403` over REST) until the caller passes `confirm`. A permission gate
you installed on the bridge with `WithPermission` moves to
`cli.WithPermission`, which runs on both services; audit sinks move to
`cli.WithAuditSinks`.

#### Step 5 — Add the socket

`cli.WithSocket(cli.SocketConfig{})` registers the same tree over an
owner-only Unix socket. It is not enabled by default: `serve socket`
starts it, or `services.socket.enabled: true` lets a bare `serve` start
it beside the api. [serve-cli-over-unix-socket.md](serve-cli-over-unix-socket.md)
covers the wire format.

## Concrete diffs

Both diffs were produced by migrating the programs above; the before
and after of each compile and run as printed.

### Path A — the leaf becomes an option

```diff
--- a-before/main.go
+++ a-after/main.go
@@ -10,49 +10,26 @@
     "github.com/spf13/cobra"
 
     "hop.top/kit/go/console/cli"
+    "hop.top/kit/go/transport/api"
 )
 
 func main() {
     root := cli.New(cli.Config{Name: "mytool", Version: "1.4.2", Short: "Manage widgets"},
         cli.WithStatus(cli.StatusConfig{}),
+        // The api service owns the listener, the lifecycle, and the
+        // startup trace. Your routes ride on it.
+        cli.WithAPI(cli.APIConfig{
+            Handlers: func(r *api.Router) {
+                r.Handle("GET", "/health", healthHandler)
+            },
+        }),
     )
-    root.Cmd.AddCommand(widgetCmd(), serveCmd())
+    root.Cmd.AddCommand(widgetCmd())
     if err := root.Execute(context.Background()); err != nil {
         os.Exit(1)
     }
 }
 
-// serveCmd is the hand-written server: its own listener, its own
-// routes, its own startup line, its own flag.
-func serveCmd() *cobra.Command {
-    cmd := &cobra.Command{
-        Use:   "serve",
-        Short: "Start the HTTP server",
-        Long:  "Start the HTTP server and block until interrupted.",
-        Args:  cobra.NoArgs,
-        RunE: func(cmd *cobra.Command, _ []string) error {
-            addr, _ := cmd.Flags().GetString("addr")
-            mux := http.NewServeMux()
-            mux.HandleFunc("GET /health", healthHandler)
-            srv := &http.Server{Addr: addr, Handler: mux}
-            go func() {
-                <-cmd.Context().Done()
-                _ = srv.Shutdown(context.Background())
-            }()
-            fmt.Fprintf(cmd.ErrOrStderr(), "Listening on %s\n", addr)
-            if err := srv.ListenAndServe(); err != http.ErrServerClosed {
-                return err
-            }
-            return nil
-        },
-    }
-    cmd.Flags().String("addr", ":8080", "Listen address")
-    cli.SetSideEffect(cmd, cli.SideEffectWriteShared)
-    cli.SetIdempotency(cmd, cli.IdempotencyNo)
-    cli.SetTopLevelVerb(cmd)
-    return cmd
-}
-
 func healthHandler(w http.ResponseWriter, _ *http.Request) {
     _ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
 }
```

### Path B — the mode switch, the bridge and the listener go

```diff
--- b-before/main.go
+++ b-after/main.go
@@ -1,67 +1,68 @@
 package main
 
 import (
+    "context"
     "fmt"
     "log"
-    "net/http"
     "os"
 
     "github.com/spf13/cobra"
 
+    "hop.top/kit/go/console/cli"
     "hop.top/kit/go/transport/api"
     "hop.top/kit/go/transport/cmdsurface"
 )
 
 func main() {
-    root := buildTree()
-
-    // CLI mode: arguments beyond the program name run the tree locally.
-    if len(os.Args) > 1 {
-        if err := root.Execute(); err != nil {
-            os.Exit(1)
-        }
-        return
+    var root *cli.Root
+    root = cli.New(cli.Config{Name: "mytool", Version: "1.4.2", Short: "Manage widgets"},
+        cli.WithStatus(cli.StatusConfig{}),
+        // REST is the api service's own projection. MCP is not a
+        // kit-shipped service yet, so it keeps its mount — on the
+        // service's router, over a bridge built when the service
+        // starts, which is when the tree is complete and gated.
+        cli.WithAPI(cli.APIConfig{
+            Handlers: func(r *api.Router) {
+                b := cmdsurface.New(root.Cmd)
+                b.Expose("*", cmdsurface.SurfaceMCP)
+                if err := cmdsurface.MountMCP(b, r, cmdsurface.WithMCPServerInfo("mytool", "1.4.2")); err != nil {
+                    log.Fatal(err)
+                }
+            },
+        }),
+    )
+    root.Cmd.AddCommand(widgetCmd())
+    if err := root.Execute(context.Background()); err != nil {
+        os.Exit(1)
     }
-
-    // Server mode: bridge the tree, mount it on REST and MCP by hand,
-    // serve on :8080.
-    b := cmdsurface.New(root)
-    b.Expose("*", cmdsurface.SurfaceREST, cmdsurface.SurfaceMCP)
-
-    r := api.NewRouter(api.WithMiddleware(api.RequestID(), api.Recovery(nil)))
-    if err := cmdsurface.MountREST(b, r); err != nil {
-        log.Fatal(err)
-    }
-    if err := cmdsurface.MountMCP(b, r, cmdsurface.WithMCPServerInfo("mytool", "1.4.2")); err != nil {
-        log.Fatal(err)
-    }
-    log.Printf("Listening on :8080")
-    log.Fatal(http.ListenAndServe(":8080", r))
 }
 
-func buildTree() *cobra.Command {
-    root := &cobra.Command{Use: "mytool", Short: "Manage widgets"}
+func widgetCmd() *cobra.Command {
     widget := &cobra.Command{Use: "widget", Short: "Manage widgets"}
     list := &cobra.Command{
         Use:   "list",
         Short: "List widgets",
+        Long:  "List every widget.",
+        Args:  cobra.NoArgs,
         RunE: func(cmd *cobra.Command, _ []string) error {
             fmt.Fprintln(cmd.OutOrStdout(), "w-1  bolt")
             return nil
         },
-        Annotations: map[string]string{"kit/side-effect": "read"},
     }
+    cli.SetSideEffect(list, cli.SideEffectRead)
+    cli.SetIdempotency(list, cli.IdempotencyYes)
     remove := &cobra.Command{
         Use:   "delete <id>",
         Short: "Delete a widget",
+        Long:  "Delete one widget by id.",
         Args:  cobra.ExactArgs(1),
         RunE: func(cmd *cobra.Command, args []string) error {
             fmt.Fprintf(cmd.OutOrStdout(), "deleted widget %s\n", args[0])
             return nil
         },
-        Annotations: map[string]string{"kit/side-effect": "destructive"},
     }
+    cli.SetSideEffect(remove, cli.SideEffectDestructive)
+    cli.SetIdempotency(remove, cli.IdempotencyYes)
     widget.AddCommand(list, remove)
-    root.AddCommand(widget)
-    return root
+    return widget
 }
```

### A larger one: `kit serve` itself

Kit's own `serve` was path A at scale — a 650-line leaf with a document
store, minted tokens, a WebSocket hub, and a startup JSON line that two
SDKs parse. After the migration the routes are unchanged and the
server, listener, startup line and shutdown are the api service's;
`--port` maps onto `services.api.addr` on loopback, the startup JSON
rides on the readiness event, and `POST /shutdown` stops the service
so the supervisor exits cleanly. Read
[`cmd/kit/serve.go`](../../../cmd/kit/serve.go) and
[`cmd/kit/main.go`](../../../cmd/kit/main.go) for the shape when the
"before" has real state to carry.

## What changes observably

Each row is one item of the contract's
[What changed observably](../../contracts/serve-lifecycle.md#what-changed-observably),
with what to do and how to check it.

| You will see | What to do | Check |
|--------------|------------|-------|
| `serve` has children and flags: `serve api`, `serve socket`, `--list`, `--enable`, `--disable`, the timeouts | nothing; a bare `serve` still starts a single-API tool | `mytool serve --help`, `mytool serve --list` |
| the startup line is gone; a `ready_reported` line carries `address=` | read the structured field, or subscribe (step 3) | `mytool serve 2>&1 \| grep ready_reported` |
| `services.api.enabled` defaults to `true` for `WithAPI` | an explicit `false` in the config file turns it off | `mytool serve` with the key set exits 2, "no services configured and enabled" |
| `GET /v1/commands` and `/v1/commands/<path>` exist | nothing to mount; withhold with `Hide` if a command must stay off | `curl -s http://127.0.0.1:8080/v1/commands` |
| the api binds loopback and refuses `:8080` unauthenticated | `Auth`, `127.0.0.1`, or `insecure_remote` (step 4) | the refusal message names the three |
| a schema-declaring command answers in `data`, not `stdout` | declare schemas with `cli.SetOutputSchema` where a client wants data | `curl` a read command: `{"exit_code":0,"data":[...]}` |

## Keep custom routes

`Handlers` is for plain routes, `Resources` for the `ResourceRouter`
form with its OpenAPI hook. Both run before the projection mounts, so
your paths always win a collision.

```go
package migrate

import (
    "net/http"

    "hop.top/kit/go/console/cli"
    "hop.top/kit/go/transport/api"
)

// Widget is the resource the old server exposed under /widgets.
type Widget struct {
    ID   string `json:"id"`
    Name string `json:"name"`
}

func (w Widget) GetID() string { return w.ID }

// apiOption keeps every route the leaf served: /health as a handler,
// /widgets as a resource, both ahead of /v1/commands.
func apiOption(widgets api.Service[Widget]) func(*cli.Root) {
    return cli.WithAPI(cli.APIConfig{
        Handlers: func(r *api.Router) {
            r.Handle("GET", "/health", func(w http.ResponseWriter, _ *http.Request) {
                w.WriteHeader(http.StatusOK)
            })
        },
        Resources: func(r *api.Router, _ interface{}) {
            r.Mount("/widgets", api.ResourceRouter[Widget](widgets,
                api.WithHumaAPI[Widget](api.HumaAPI(r), "/widgets")))
        },
    })
}
```

## Common pitfalls

### The address still says `:8080`

```console
USAGE: service "api": addr: ":8080" is not a loopback address and the api service has no authentication; set APIConfig.Auth, listen on 127.0.0.1, or set services.api.insecure_remote: true (or --insecure-remote) to serve unauthenticated beyond loopback
```

An empty host binds every interface, and that is not loopback. Step 4.

### `main` exits 1 for everything

The refusal above is exit `2`, and a script branching on it sees `1`
until `main` maps `*output.Error` (step 5). The contract's table is
only as good as the process that returns it.

### Something still scrapes `Listening on`

There is no such line. The `ready_reported` log line and the bus event
carry `address=`; wait for either, or simply retry the connection —
the listener exists only once the service is ready.

### Both `serve` commands exist

`WithAPI` and `WithService` mount kit's `serve` and drop a leaf mounted
before them; a leaf you add after `cli.New` is a second owner of the
word and the tree is ambiguous. Delete it.

### Your routes own the first path segment

A route such as `/{type}/` is a subtree pattern: it also matches
`/v1/commands/<withheld command>`, so a request for a command the
projection withheld is answered by your handler — as document type
`v1` — rather than by a 404. Kit's own document engine is in exactly
this position. The projection's mounted routes are more specific and
still win, and `GET /v1/commands` still lists what is withheld and
why; a client must read discovery rather than probe routes. A
`/v1/commands/` guard route is not the fix: Go's mux refuses it as
ambiguous against `/{type}/{id}/history`.

### The destructive command has no route

```json
{"name":"widget delete","invocable":false,"reason":"unauthorized-destructive"}
```

Expected. Name the surface in `Policy` (path B step 4), then pass
`confirm` — the command's own gate answers `403` with exit `5` until
you do:

```json
{"exit_code":5,"stderr":"UNAUTHORIZED: destructive command mytool widget delete refused: --confirm=no (or non-TTY default)\n"}
```

### Setting `Auth` refuses to start

```console
cli validation failed: 2 leaf command(s) missing kit/side-effect annotation: mytool token claims, mytool token decode; 2 leaf command(s) missing kit/idempotent annotation: mytool token claims, mytool token decode; 2 leaf command(s) missing Long: mytool token claims, mytool token decode
```

`WithAPI` mounts a `token` command whenever `Auth` is set, and its two
leaves arrive without the annotations a validating root requires. Until
kit annotates them at the source, carry them yourself after `cli.New`
— both leaves are reads:

```go
package migrate

import "hop.top/kit/go/console/cli"

// annotateTokenLeaves gives kit's token claims and token decode the
// annotations the validator requires.
func annotateTokenLeaves(root *cli.Root) {
    for _, c := range root.Cmd.Commands() {
        if c.Name() != "token" {
            continue
        }
        for _, leaf := range c.Commands() {
            if leaf.Long == "" {
                leaf.Long = leaf.Short + "."
            }
            if _, ok := cli.GetSideEffect(leaf); !ok {
                cli.SetSideEffect(leaf, cli.SideEffectRead)
            }
            if _, ok := cli.GetIdempotency(leaf); !ok {
                cli.SetIdempotency(leaf, cli.IdempotencyYes)
            }
        }
    }
}
```

### The socket path is too long

```console
USAGE: service "socket": path: "/very/long/…/mytool.sock" is 121 bytes, over the 103-byte limit for a unix socket path
```

`services.socket.path` or `--socket` with a shorter path; the default
under the runtime dir is short by construction.

## Test it

The claims this page makes are pinned by
[`examples/served`](../../../examples/served/README.md): a kit CLI
built the way this page ends up, with a test per claim, all through
the real `Execute` path. Diff your root against its `newRoot` when a
claim holds there and not in your tool. A project generated by
`kit init --from cli-go` starts in the migrated state; the template's
gate in `cmd/kit/init` drives the rendered binary through the same
checks.

## Reference

- [serve-lifecycle contract](../../contracts/serve-lifecycle.md) —
  the normative rules, including
  [Compatibility](../../contracts/serve-lifecycle.md#compatibility).
- [expose-cli-over-rest.md](expose-cli-over-rest.md) — the projection's
  route shape, discovery, policy, `Hide`, OpenAPI.
- [serve-cli-over-unix-socket.md](serve-cli-over-unix-socket.md) — the
  socket service.
- [secure-remote-serving.md](secure-remote-serving.md) — `Auth`, the
  permission gate, the audit trail.
- [build-a-transport-service.md](build-a-transport-service.md) — a
  transport of your own as a `serve` sibling.
- [`examples/served`](../../../examples/served/README.md) — the
  conformance fixture.
