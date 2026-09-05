# integrations

cross-cutting adapters surfaced by adopter reviews; holds no code of its own.

## Sub-packages

- [repohost/](repohost/): unified repository-host driver SPI over
  github, gitlab, gitea, gitee and bitbucket.

Each driver lives at `repohost/<provider>/` with a sibling
`repohost/<provider>/mock/` registering as `"<provider>-mock"` for
adopter unit tests. No sub-package carries its own README yet; read
the package doc comments via `go doc`.
