# integrations

Connecting a kit-powered CLI to specific external tools: AI harnesses, Claude Code, repository hosts.

## Contents

| Path | What it is | Start here when |
|------|------------|-----------------|
| [`claude-code-permissions.md`](claude-code-permissions.md) | Claude Code permission rules derived from the kit-toolspec contract | you run a kit-powered CLI under Claude Code and want permissions generated from its manifest |
| [`toolspec-adopter-guide.md`](toolspec-adopter-guide.md) | how a kit-powered CLI publishes its capability manifest | you build a CLI and want AI harnesses (Claude Code, MCP hosts, agent frameworks) to read it |
| [`toolspec-harness-guide.md`](toolspec-harness-guide.md) | how a harness consumes the toolspec contract | you implement an MCP host, agent framework, IDE extension or Claude Code adapter |
| [`repohost/`](repohost/README.md) | repository host facade over go-scm | you authenticate kit against GitHub, GitLab or another provider |
