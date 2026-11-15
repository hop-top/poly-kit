# i18n-v1

## What it answers

How a message catalog is written and loaded, how messages are formatted (Unicode MessageFormat 2), which plural category a number falls in, and how a locale tag becomes a fallback chain, so that every SDK loads the same catalog to the same messages, rejects the same malformed ones, formats the same way and resolves the same locale. Wrong place for rendering APIs, TMS wire protocols or typed key generation; they consume this contract.

## Use it when

- you write a catalog loader in any SDK: replay `catalog-corpus.json` and `catalog.schema.json`, then the `mf2-suite/` files
- you pick plural or locale data: replay `plural.json` and `fallback.json` against your platform library; the contract owns no algorithm
- you change a rule in `SPEC.md`: add the vector first, then make every port pass

## Quick start

```sh
make test-parity-i18n
```

Validates every fixture against its schema (catalogs, plural vectors, locale vectors, the vendored suite against upstream's test schema) and checks the vendored suite is byte for byte what `mf2-suite/source.json` records. It does not format a message or compute a plural: that is each port's job against the same files.

## Contract

| File | Pins |
|------|------|
| `SPEC.md` | normative rules: catalog shape, MessageFormat 2 profile, required functions, locale tags, platform delegation, load errors |
| `catalog.schema.json` | structure of a catalog document: version, locale shape, key pattern, leaf fields |
| `catalog-corpus.json` | valid documents with their flat messages; invalid documents with the exact error `code` and dotted `key` |
| `mf2-suite/` | the official MessageFormat 2 test suite at tag `LDML48.2`, vendored unmodified; see its README |
| `plural.json`, `plural.schema.json` | cardinal and ordinal category for `(locale, type, decimal string)`; CLDR 47 minimum |
| `fallback.json`, `fallback.schema.json` | tag canonicalisation, likely subtags, parent locales, fallback chains, `resolve`, per-key lookup |

Corpus cases marked `schema_gap` fail in code but are not expressible in the schema (MessageFormat 2 syntax and data-model faults, bytes-level faults such as duplicate keys); every other invalid object document MUST fail the schema.

Standards, not inventions: MessageFormat 2 (Unicode LDML 48.2, Part 9), BCP 47, CLDR plural rules, parent locales and likely subtags. No port ships its own message parser grammar, plural rules or locale tables beyond what its platform library and the Go note below provide. Go generates its tables in-repo; see `SPEC.md`.

All fixture text is fake.

## Parity claim

A port claims i18n-v1 parity when its test suite:

1. loads every `catalog-corpus.json` case: valid ones yield exactly the recorded flat messages and every message parses as MessageFormat 2; invalid ones fail with exactly the recorded `code` and `key`
2. validates every object document against `catalog.schema.json`: valid pass, invalid fail unless `schema_gap`
3. passes the mandatory `mf2-suite/` files (list in `mf2-suite/README.md`)
4. returns the `plural.json` category for every row from its platform's CLDR facility, with no hand-written rules, skipping rows whose `since` exceeds its CLDR version
5. reproduces every `fallback.json` section from its platform's locale data, same `since` rule, and `invalid_locale` for each rejected tag

A port records the CLDR version of its data in its own test output. Minimum CLDR is 47.

## Recommended libraries (non-normative)

Latest releases as of the contract draft; each port decides and may vendor instead.

| Port | MessageFormat 2 | Plurals, tags, parents |
|------|-----------------|------------------------|
| TypeScript | `messageformat` 4.1.0 (reference implementation; runs the official suite) | `Intl.PluralRules`, `Intl.Locale`, `Intl.getCanonicalLocales` |
| Python | `messageformat2` 1.1.0 | `Babel` 2.18.0 |
| Go | `go.expect.digital/mf2` v0.1.0 (young; evaluate against the suite) or in-repo | tables generated in-repo from a pinned CLDR release (`SPEC.md`) |
| Rust | `mf2` 3.0.0 (very recent; evaluate against the suite) | `icu` 2.4.0 (ICU4X) |
| PHP | none; `MessageFormatter` in ext-intl is the older ICU dialect, not MessageFormat 2 | ext-intl `Locale` (ICU 79.1 here); see `SPEC.md` for its gaps |

## Neighbours

- `SPEC.md`: the rules these files pin
- `mf2-suite/README.md`: vendored suite, license, update procedure
- `../llm-v1/`, `../typeid-v1/`: sibling corpora with the same layout
