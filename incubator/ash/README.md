# ash

> [!WARNING]
> **Deprecated.** This package has moved to
> [`hop-top/poly-stem`](https://github.com/hop-top/poly-stem) and
> renamed to `hop.top/stem`. New code MUST import `hop.top/stem`.
> This incubator copy will be removed once all consumers migrate
> (tracking: see consumer migration tracks in each downstream repo).

> **Incubating** — this package is developed in
> [hop-top/poly-kit](https://github.com/hop-top/poly-kit/tree/main/incubator/ash).
> Submit issues, PRs, and discussions there.

AI session history — storage, routing, and runtime for
conversational agents.

Manages session lifecycle, turn persistence, tool execution,
multi-session supervision, and provider-agnostic LLM integration.

## Install

```
go get hop.top/ash
```

## Library

### Session

```go
session := ash.NewSession("chat-001",
    ash.WithStore(ash.NewMemoryStore()),
    ash.WithProvider(myLLMProvider),
    ash.WithRouter(ash.NewDirectRouter()),
    ash.WithPublisher(myEventPublisher),
    ash.WithMetadata(map[string]any{"user": "alice"}),
)

session.Append(ctx, ash.Turn{
    Role:    ash.RoleUser,
    Content: "Hello",
})
```

### Storage Backends

```go
// In-memory (testing, ephemeral)
store := ash.NewMemoryStore()

// JSONL files (append-only, portable)
store := ash.NewJSONLStore("/path/to/sessions")

// SQLite (queryable, persistent)
store, _ := sqlite.New("/path/to/sessions.db")
```

### Runtime

Agent loop with tool execution and depth limits:

```go
reg := ash.NewToolRegistry()
reg.Register(ash.ToolDef{Name: "weather", ...}, handler)

rt := ash.NewRuntime(session,
    ash.RuntimeWithToolRegistry(reg),
    ash.RuntimeWithProvider(provider),
    ash.RuntimeWithMaxToolDepth(5),
    ash.RuntimeWithStore(store),
    ash.RuntimeWithPublisher(pub),
)

rt.Run(ctx)
```

### Tool Registry

```go
reg := ash.NewToolRegistry()

// Function handler
reg.Register(
    ash.ToolDef{Name: "search", Description: "Search the web", InputSchema: schema},
    ash.ToolHandlerFunc(func(ctx context.Context, name string, input json.RawMessage) (json.RawMessage, error) {
        // handle tool call
        return json.Marshal(result)
    }),
)
```

### Router

Route messages to different providers/models:

```go
router := ash.NewDirectRouter()
```

### Supervisor

Manage multiple concurrent sessions with event publishing:

```go
sup := ash.NewSupervisor(publisher)
```

### Session Forking

Create child sessions that inherit context:

```go
child := session.Fork("child-001")
```

### Querying

```go
// List sessions
metas, _ := store.List(ctx, ash.Filter{})

// Filter turns
turns, _ := store.Turns(ctx, "session-id", ash.TurnFilter{})
```

### Event Topics

| Topic | Payload |
|-------|---------|
| `session.created` | Session metadata |
| `session.spawned` | Fork/child session |

## Types

| Type | Description |
|------|-------------|
| `Session` | Conversation state + history |
| `Turn` | Single message (user/assistant/tool) |
| `ContentPart` | Structured content (text, tool_call, tool_result) |
| `Runtime` | Agent execution loop |
| `ToolRegistry` | Tool definition + handler mapping |
| `Supervisor` | Multi-session orchestrator |

## Sub-packages

| Package | Description |
|---------|-------------|
| [sqlite/](sqlite/) | SQLite-backed Store implementation |
| [kitadapter/](kitadapter/) | Type mapping between hop.top/llm and hop.top/ash |

## License

MIT
