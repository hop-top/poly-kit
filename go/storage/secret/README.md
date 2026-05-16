# secret

Secure credential storage and provider integration.

Backends (each in its own subpackage, registered via blank import):
`env`, `file`, `agefile`, `keyring`, `onepassword`, `ghsecrets`,
`openbao`, `infisical`, `memory`.

For routing keys across multiple backends in one store (e.g. CI
secrets in `agefile`, developer creds in `keyring`, env as fallback),
see [`composite`](composite/). Composition is code-level only:
predicates are arbitrary Go funcs and cannot be expressed in
`secret.Config`.
