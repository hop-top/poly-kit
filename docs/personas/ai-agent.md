---
id: ai-agent
name: "AI Agent"
role: "Autonomous consumer of a kit-based tool over REST, socket, or MCP"
languages: [go, typescript, python]
---

## Context

Non-human caller handed a running kit-based tool and a task. Reaches
the command tree through the `api` service (REST + OpenAPI) or the
`socket` service (NDJSON over a Unix domain socket), never through a
terminal. Reads no `--help`; the tool describes itself.

Distinct from every other persona here: it is not building a tool with
kit, it is consuming one. Its whole surface is the served projection,
so what kit withholds and how kit explains the withholding is the
entire contract.

## Needs

- One discovery call that describes the whole tree, invocable or not,
  so route probing is never the way to learn what exists
- A closed refusal vocabulary it can branch on, distinguishing "no such
  command" from "real command, not permitted here"
- Structured results (`data`) rather than prose on stdout, so no
  answer has to be scraped
- A confirmation protocol whose refusal names what the retry needs, so
  a gate is passable without guessing
- One exit-code taxonomy shared by every transport, mapped to HTTP

## Pain points

- Tools that publish framework built-ins (`help`, `completion`) as
  callable capabilities, diluting the tool list
- A 404 that could mean either "wrong path" or "withheld", forcing a
  retry loop that can never succeed
- Destructive commands reachable without a gate, or gated with a
  prompt no non-TTY caller can answer
- Refusals worded only for humans, leaving no signal for whether to
  retry, escalate, or stop
- Interactive commands accepting a request and then blocking on a
  terminal that is not there

## Success criteria

- `GET /v1/commands` is sufficient to plan every call; `route` and
  `method` are absent exactly when the command has no route
- Every non-invocable entry carries one reason from the closed set,
  and the reason determines the next action without inference
- A read with an output schema answers in `data` with `stdout` empty
- A typed-token refusal carries the token value, so the retry is
  mechanical and no token is ever invented
- `serve`, `upgrade`, and interactive commands are unreachable
  remotely on every transport and under every configuration
