# Register custom URI schemes

> Audience: Go adopters who want `yourtool://...` links (task links, deep links from a browser
> or chat) to open their CLI. Package: `hop.top/kit/go/console/uri`, mounted through
> `cli.WithURI`. Package README: [uri](../../../go/console/uri/README.md).

The package gives your CLI a `uri` command group that parses a scheme value, plans the command
an action link maps to, completes identifiers, and generates the OS-level handler metadata.
Parsing and handler generation delegate to the `hop.top/cite` library; kit adds the commands.

Two limits to know first:

- `uri resolve` only prints a plan. It never executes the command. Your launcher (the OS
  handler, or a script) runs the plan.
- `uri handler generate` produces a snippet to install. It does not register the scheme with
  the OS by itself.

## 1. Mount the command group

```go
import (
    "hop.top/cite/handle/generate"
    "hop.top/cite/scheme"

    "hop.top/kit/go/console/cli"
)

root := cli.New(cli.Config{Name: "spaced", Version: "1.0.0", Short: "Mission control"},
    cli.WithURI(cli.URIConfig{
        Policy: scheme.Policy{
            DefaultNamespaceSegments: 1,
            SchemeNamespaceSegments:  map[string]int{"spaced": 2},
            ActionRoutes: map[string]scheme.ActionRoute{
                "mission.open": {
                    Command: "spaced",
                    Args:    []string{"-C", "{namespace}", "mission", "show", "{id}"},
                },
            },
        },
        Handler: cli.URIHandlerConfig{
            Vendor: "example", App: "spaced", Scheme: "spaced",
            Language: generate.LanguageGo, AppPath: "/usr/local/bin/spaced",
        },
    }))
```

As in any kit root, a shipping tool also passes `Root.Validate`. `cli.URIConfig` and `cli.URIHandlerConfig` are aliases of `uri.Config` and `uri.HandlerConfig`.
Without `cli`, call `uri.Register(parent, cfg)` or add `uri.Command(cfg)` yourself. Set
`CommandName` to mount it under another name, such as `link`.

The group is marked `kit/reserved`: it is management surface, and no served transport (REST,
MCP, RPC, socket) runs it. See
[declare and read command annotations](declare-and-read-command-annotations.md).

## 2. Choose a namespace policy

`scheme.Policy` decides how a URI splits into `namespace` and `id`:

- `SchemeNamespaceSegments["spaced"] = 2`: the first two path segments are the namespace.
- `DefaultNamespaceSegments`: the fallback for other schemes.

With the policy above:

```console
$ spaced uri parse 'spaced://acme/apollo/M-7?name=mission&action=open' --format json
{
  "scheme": "spaced",
  "namespace": "acme/apollo",
  "id": "M-7",
  "query": "name=mission&action=open",
  "action": "mission.open"
}
```

If you pass a zero `scheme.Policy`, the library default applies. Once you set any field of it,
that policy is used as written.

## 3. Map actions to commands

`ActionRoutes` keys are action names. A link whose query carries `name=mission&action=open`
parses to action `mission.open`; `uri resolve` expands the route template. Placeholders are
`{scheme}`, `{namespace}`, `{id}`, `{query}` and `{fragment}`:

```console
$ spaced uri resolve 'spaced://acme/apollo/M-7?name=mission&action=open' --format text
spaced -C acme/apollo mission show M-7
```

Default format is `table`; `json` and `yaml` give `action`, `command` and `args` for a launcher
to run. A link with no action, or an action with no route, is an error
(`cite: action is required`, `cite: unknown action`).

Treat the plan as untrusted input to your launcher: run `command` with `args` as an argv list,
never through a shell string, and only for actions you routed.

## 4. Add vanity aliases and completion

A `VanityAlias` maps a short link to a canonical one. With `Prefix: true` and
`PreserveSuffix: true`, `spaced://mine/...` keeps its suffix. Lookup is fuzzy by default, so a
typo in the alias still resolves; `--strict` turns fuzzy matching off and `--json-ambiguity`
returns ambiguous matches as JSON in the error.

```go
VanityAliases: []scheme.VanityAlias{{
    From: "spaced://mine", To: "spaced://acme/apollo/M-7",
    Prefix: true, PreserveSuffix: true,
}},
```

`Original` in the parse output keeps the link as typed, so you can see an alias was used.

`uri complete` has two modes:

```console
$ spaced uri complete --input spaced://mnie
spaced://mine    canonical: spaced://acme/apollo/M-7
$ spaced uri complete --type spaced --prefix M-
acme/apollo/M-7
acme/apollo/M-8
```

`--type` calls the completer you register in `Config.Types`:

```go
Types: []scheme.TypeRegistration{{
    Name: "spaced",
    Completer: func(ctx context.Context, prefix string) ([]string, error) {
        return listMissionIDs(ctx, prefix)
    },
}},
```

One of `--type` or `--input` is required.

## 5. Generate the OS handler

`Config.Handler` supplies defaults for the flags of `uri handler`:

```console
$ spaced uri handler id
example.spaced.go.spaced
$ spaced uri handler generate --platform linux
[Desktop Entry]
Type=Application
Name=example.spaced.go.spaced
Exec=/usr/local/bin/spaced %u
MimeType=x-scheme-handler/spaced;
NoDisplay=true
X-Hop-Handler-ID=example.spaced.go.spaced
```

`--platform` takes `linux`, `macos`, `ios` or `windows` (default `linux`). `macos` prints a
`CFBundleURLTypes` plist fragment. `--language` is one of `go`, `ts`, `py`, `rs`, `php`.

`generate` writes only with `--output <file>` (default `-`, stdout). It is tagged
`write-local`; with `--output` and the global `--dry-run` it prints the plan without writing. Install the
result where your OS expects it, then link clicks reach `AppPath` with the URI as the argument.

## 6. Trim the surface

List leaf keys in `DisabledCommands`: `parse`, `resolve`, `complete`, `handler.id`,
`handler.generate`, `completion`. A disabled leaf is not mounted. The `handler` group is
dropped only when both its leaves are disabled.

## Pitfalls

- Each leaf has its own `--policy <file>` flag for loading a policy from JSON or YAML, but a
  kit root also owns a global `--policy` for delegation policy, and that one wins on a
  `cli.New` root: the path is read as a policy name and fails with
  `--policy is set but no policy loader is wired`. Set `Config.Policy` in code instead.
- A bare `parse` of a string with no scheme or no id errors (`cite: scheme is required`,
  `cite: id is required`).
- `uri completion` prints a shell-completion script for the whole CLI. Disable it with
  `DisabledCommands: []string{"completion"}` if your tool already ships one, such as
  [completion](completion-user-guide.md).

## See also

- [`getting-started-cli`](getting-started-cli.md)
- [spaced example wiring](../../../examples/spaced/go/main.go) (`spacedURIConfig`): a full
  `URIConfig` in a real tool.
