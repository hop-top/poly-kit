# architecture

How the kit codebase is organised: domains, layering, dependency rules, where new code belongs.

## Contents

| Path | What it is | Start here when |
|------|------------|-----------------|
| [`architecture.md`](architecture.md) | concept page on how kit is structured and what each domain owns | you decide where new code belongs, or want a mental model before browsing packages |
| [`bus-streams.md`](bus-streams.md) | design for bus network streams: capability advertisement, remote subscribe, resume offsets, reset, offline outbox | you change the bus network wire protocol or port it to another SDK |
