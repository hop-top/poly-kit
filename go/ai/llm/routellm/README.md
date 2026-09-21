# routellm

adapter for the RouteLLM routing library.

## Model field

The `routellm://` model field takes either shape a RouteLLM server
advertises on `/v1/models`:

| URI | Sent as `model` | Router + threshold chosen by |
|-----|-----------------|------------------------------|
| `routellm://mf:0.7` | `router-mf-0.7` | client |
| `routellm://private` | `private` | server, from its tier config |

A value whose text after the last colon parses as a finite float is a
router/threshold pair; the threshold must be in `[0,1]`. Anything else
is a tier name and travels verbatim, so tier names may contain colons.
