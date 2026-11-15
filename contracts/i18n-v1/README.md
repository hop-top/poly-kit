# i18n-v1

## What it answers

How a message catalog is written and parsed, how a locale tag becomes a fallback chain, and which plural category a number falls in, so that every SDK loads the same catalog to the same messages, rejects the same malformed ones and resolves the same locale. Wrong place for rendering APIs, TMS wire protocols or typed key generation; they consume this contract.

## Use it when

- you write a catalog parser in any SDK: replay `catalog-corpus.json` and `catalog.schema.json` before anything else
- you resolve a locale (OS list, browser list, CLI flag): replay `fallback.json`; supply only your platform's tag table
- you change a rule in `SPEC.md`: add the case to the corpus first, then make every port pass

## Quick start

```sh
make test-parity-i18n
```

Runs the corpus self-check in this directory: every catalog vector against the schema, plus a small reference scanner and RFC 4647 lookup that must reproduce each recorded expectation. A corpus case that disagrees with the spec text is a corpus bug to fix, not a case to drop.

## Contract

| File | Pins |
|------|------|
| `SPEC.md` | normative rules: catalog shape, keys, parameters and escape, plurals, locale tags, fallback, load errors, interchange mappings |
| `catalog.schema.json` | structure of a catalog document: version, locale shape, key pattern, leaf fields, parameter types, required plural `other` |
| `catalog-corpus.json` | valid documents with their flat parsed messages; invalid documents with the exact error `code` and dotted `key` |
| `fallback.json` | tag canonicalisation, unfiltered chains, `resolve` over an available set, per-key lookup across catalogs |
| `plural.json` | CLDR cardinal category for `(locale, decimal string)` |

Corpus cases marked `schema_gap` fail in code but are not expressible in the schema (placeholder syntax, declared-versus-used parameters, plural parameter type, bytes-level faults such as duplicate keys); every other invalid object document MUST fail the schema.

Standards, not inventions: BCP 47 tags, RFC 4647 Lookup, CLDR plural rules and formatting data. No port ships its own plural or number tables.

All fixture text is fake.

## Parity claim

A port claims i18n-v1 parity when its test suite:

1. loads every `catalog-corpus.json` case: valid ones yield exactly the recorded flat messages (segments, parameter types, plural branches); invalid ones fail with exactly the recorded `code` and `key`
2. validates every object document against `catalog.schema.json`: valid pass, invalid fail unless `schema_gap`
3. reproduces every `fallback.json` section: canonical form, chain, `resolve`, per-key lookup, and `invalid_locale` for each rejected tag
4. returns the `plural.json` category for every case from its platform's CLDR facility, with no hand-written rules
5. substitutes by segment: an argument holding `%{x}`, braces or `$` appears verbatim

The fixtures are edited by hand. `make lint-config` checks they parse; `make test-parity-i18n` checks the corpus agrees with itself. Port loaders are added to that target as they land.

## Neighbours

- `SPEC.md`: the rules these files pin
- `../llm-v1/`, `../typeid-v1/`: sibling corpora with the same layout
