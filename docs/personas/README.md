# Personas

Target users for kit. Personas describe who kit serves, what they
need, and where they hurt today. Stories and features reference
these personas to keep design choices grounded in real users.

| Persona | Scope |
|---------|-------|
| [cli-author](cli-author.md) | Builds CLIs with kit in any language |
| [go-toolmaker](go-toolmaker.md) | Builds Go CLI tools with kit packages |
| [ts-toolmaker](ts-toolmaker.md) | Builds TypeScript CLI tools with kit packages |
| [py-toolmaker](py-toolmaker.md) | Builds Python CLI tools with kit packages |
| [oss-contributor](oss-contributor.md) | Contributes to any hop-top repo |
| [kit-contributor](kit-contributor.md) | Contributes to kit packages specifically |
| [secret-consumer](secret-consumer.md) | Developer reading secrets in kit-based apps |
| [security-operator](security-operator.md) | Configures and manages secret backends for teams |

Personas form an extension chain: `oss-contributor` is the broadest
audience; `cli-author` narrows to CLI builders; the language-specific
toolmakers and the security personas extend further from there.
