# i18n-v1 specification

Normative text for the files beside it. MUST, SHOULD and MAY are RFC 2119 words. Where this file and a vector disagree, fix the one that is wrong; neither wins by default.

Not covered: message rendering APIs, TMS wire protocols, code generation. They consume this contract and add no rules to it.

## Standards chosen

| Concern | Standard |
|---------|----------|
| Message syntax and formatting | Unicode MessageFormat 2 (LDML Part 9), release tag `LDML48.2` of unicode-org/message-format-wg |
| Message conformance | the official test suite at that tag, vendored in `mf2-suite/` |
| Locale tags | BCP 47 (RFC 5646), canonical form per CLDR aliases and UTS 35 |
| Plural categories, parent locales, likely subtags | Unicode CLDR, minimum release 47 |
| Number, date, time formatting | CLDR data through the platform facility; never hand-rolled |
| Catalog encoding | JSON (RFC 8259), UTF-8, no BOM |
| Interchange | XLIFF 2.x, GNU gettext PO, flat JSON and ARB |

## Catalog document

One document per locale:

```json
{ "v": 1, "locale": "en", "messages": { "cli": { "files": ".input {$n :number}\n.match $n\none {{{$n} file}}\n* {{{$n} files}}" } } }
```

- `v`: the number `1`. Any other value, including the string `"1"`, is `unsupported_version`.
- `locale`: a tag accepted by Locale tags below. The file name is not significant.
- `messages`: an object tree. `$schema` MAY appear at the top level and is ignored. No other top-level field is allowed.
- Decoding is strict: unknown fields, duplicate object keys at any depth and trailing data after the document are load errors.

### Keys

A message is addressed by its dotted path: object keys from `messages` down, joined with `.`. A key segment matches `^[A-Za-z0-9_-]+$`: non-empty, no dot, no whitespace, no `$`. Segments starting with `$` are reserved for the leaf fields below. Because a segment cannot hold a dot, dotted paths are unique.

### Nodes

A node is one of:

1. a string: shorthand for a message with no description;
2. a leaf object: has `$msg`, plus optional `$desc`;
3. a namespace: a non-empty object of child nodes with no `$` keys.

Any other `$` key in a leaf, or any `$` key in a namespace, is `unknown_reserved_key`. Numbers, booleans, null and arrays are `invalid_node`.

| Field | Type | Meaning |
|-------|------|---------|
| `$msg` | string | the message: one MessageFormat 2 message |
| `$desc` | string | translator note; never rendered; MAY be dropped by tools that do not carry notes |

Typed parameter tables, plural branch objects and a placeholder syntax of this contract do not exist: variables, their types, plural and ordinal selection live inside the MessageFormat 2 message.

## Messages

Each message is a MessageFormat 2 message as defined by tag `LDML48.2`: `simple-message` or `complex-message` of its `message.abnf`. The runtime formats it with the active locale and arguments by that specification; this contract adds no rule to parsing, resolution, selection or formatting.

Load validates every message. A syntax error or a data-model error (duplicate declaration, duplicate option name, duplicate variant, missing fallback variant, missing selector annotation, variant key mismatch) is `invalid_message` at the leaf's key. Errors that depend on the formatting call (unknown function, unresolved variable, bad operand, bad option, bad selector) are not load errors; the runtime handles them as the specification's error handling says, and the catalog still loads. Literal text outside placeholders carries `$`, `%` and `\` as is, except that `{`, `}` and `\` are written `\{`, `\}`, `\\` (an MF2 rule).

Formatting a string uses the specification's Default Bidi Strategy. A port MUST also offer a no-isolation mode so the suite's expectations can be asserted.

### Functions

| Function | Status | Notes |
|----------|--------|-------|
| `:string` | required | selection and formatting |
| `:number` | required | every option the specification marks REQUIRED, among them `select` (`plural`, `ordinal`, `exact`) |
| `:integer` | required | same selection options |
| `:offset` | required | `add` and `subtract`; selection and formatting |
| `:currency`, `:percent` | optional | stable-tag tests in the suite are skipped when not accepted; accepted means the suite cases pass |
| `:date`, `:time`, `:datetime` | optional | draft in `LDML48.2`; same rule. A catalog author who needs them takes them from a port that lists them |
| `:unit` | optional | draft; no suite cases |
| `u:id`, `u:dir`, markup | `u:` options optional; markup placeholders (`{#a}`, `{/a}`) required to parse and to format as the specification says |

"Required" means the port accepts the function and options and passes the mandatory suite files (`mf2-suite/README.md`). A port MAY add functions in its own namespace.

Selection with `select=plural` or `select=ordinal` uses the platform's CLDR plural rules for the active locale; `select=exact` compares the number's string form. The categories each locale produces are pinned by `plural.json`.

## Plurals

Delegated. Each port takes cardinal and ordinal plural rules from its platform library: `Intl.PluralRules` in TypeScript, ICU4X in Rust, Babel in Python, ext-intl (ICU) in PHP. The Go port is the exception: it generates its own tables, see Go port. The contract does not define the rules; it pins behavior with `plural.json`, which every port MUST reproduce:

- CLDR 47 at least; rows whose result changed in a later CLDR carry `since` (first CLDR major with that result). A port skips a row whose `since` is above its CLDR version and passes it from that version on;
- cardinal and ordinal categories, including en one/other and ordinal one/two/few/other, ru/pl/ar few/many/zero, fr (and es, pt, it) many at one million, cy with all six categories, ja other only;
- the numeric argument as a decimal string with no visible trailing zeros.

## Locale tags

Input is a BCP 47 tag, any case. `_` separators are read as `-` before anything else (adapters for POSIX names strip the encoding and modifier suffix first; `en_US.UTF-8` is rejected). Rejected with `invalid_locale`: empty tag, whitespace, non-ASCII, the wildcard `*`, malformed subtags, repeated singletons or subtags, a bare `x-` private tag. Not rejected but not interpreted: extension and private-use subtags are kept in the canonical form and ignored for fallback.

Canonical form is the CLDR/ECMA-402 one: language lower case, script title case, region upper case, variants and extensions lower case, deprecated and aliased tags replaced (`iw` is `he`, `sh` is `sr-Latn`, `tl` is `fil`), a script that is the language's default is kept as written (`en-latn-us` is `en-Latn-US`). The platform provides the mapping; `fallback.json` `canonicalize` pins it. A platform call that does not reject malformed tags or apply aliases (PHP `Locale::canonicalize` does neither) needs a port-side check; the vectors catch the gap.

## Fallback

Delegated to the platform's locale data, pinned by `fallback.json`. Informative description of what the vectors encode, the CLDR locale inheritance of UTS 35:

1. likely subtags: a missing script is inferred from language and region (`zh-TW` becomes `zh-Hant-TW`, `sr-ME` becomes `sr-Latn-ME`), a script equal to the language's default is dropped (`en-Latn-US` becomes `en-US`);
2. parent locales: CLDR `parentLocales` overrides first (`en-AU` to `en-001`, `es-MX` to `es-419`, `pt-AO` to `pt-PT`, `zh-Hant-MO` to `zh-Hant-HK`, `nb` to `no`), otherwise drop the last subtag; a script-only locale such as `zh-Hant` has root as parent, never the bare language;
3. the default locale's chain follows the requested tag's, duplicates removed, first occurrence kept.

`chain(tag, default)` is the list of locales to try. `resolve(tag, available, default)` is the first chain entry present in `available` (compared canonical); none present returns no locale, not an error. Resolution does not widen beyond the chain: `fr` does not select `fr-FR`, `fr-CA` selects `fr`. Per-key lookup walks the chain restricted to loaded catalogs and takes the first catalog that defines the key; fallback is per key, not per catalog. A key defined nowhere is a missing-key condition for the runtime to surface; its representation is not part of this contract.

Both `tag` and `default` are explicit inputs; process environment is never read by the contract. A malformed `tag` or `default` is `invalid_locale`.

Vectors carry `since` like the plural ones (parent locales and likely subtags changed between CLDR releases: `en-EE` gained `en-150` as parent in CLDR 48).

### Go port

The Go implementation generates its plural, parent-locale and likely-subtag tables in-repo with `go generate` from a pinned CLDR release (not older than 47), and does not rely on `golang.org/x/text`, whose CLDR data is release 32. The pin is part of the port's source; bumping it re-runs the vectors.

## Load errors

A load error carries `code`, the catalog `file` (when loaded from one) and the dotted message `key` (empty for the document). Ports MAY add line and column. Only the first fault in document order is reported; the corpus pins `code` and `key`.

| Code | Fault |
|------|-------|
| `invalid_json` | not JSON, or trailing data |
| `duplicate_key` | object key repeated; `key` is the full path of the second |
| `invalid_document` | not an object, unknown or missing top-level field |
| `unsupported_version` | `v` is not the number 1 |
| `invalid_locale` | `locale` rejected by Locale tags |
| `invalid_key` | key segment outside `^[A-Za-z0-9_-]+$` |
| `unknown_reserved_key` | `$` key not defined above, or a non-`$` key beside `$msg` |
| `empty_namespace` | namespace with no children |
| `invalid_node` | wrong value type; `$desc` without `$msg`; wrong type for a leaf field |
| `invalid_message` | message with a MessageFormat 2 syntax or data-model error; `key` is the message's key |

A catalog that fails to load is never partially used.

## Interchange mappings

Mappings are lossless for the fields below; anything else an external format carries (for example PO plural-form headers, XLIFF `<note>` priority) is dropped on import or regenerated on export and is not part of the contract. Vectors for these mappings belong to the interchange work and are not in this corpus.

| i18n-v1 | XLIFF 2.x | gettext PO | flat JSON / ARB |
|---------|-----------|------------|-----------------|
| dotted key | `<unit id>` | `msgctxt` (with `msgid` = source message) | object key |
| locale | `srcLang` / `trgLang` | `Language:` header | file name or `@@locale` (ARB) |
| `$msg` | `<segment>` `<source>` / `<target>`, message kept whole | `msgid` / `msgstr`, message kept whole | string value |
| `$desc` | `<note category="description">` | `#.` extracted comment | `@key.description` |

The MessageFormat 2 message is one opaque string in transit: no per-category units, no `msgid_plural`. A translation MUST keep every variable of the source message and MUST NOT add variables; it MAY change the variants, which are locale-specific, as long as the message stays valid MessageFormat 2 with a `*` variant. Plural exports to formats that only have the older ICU dialect (ARB `{count, plural, ...}`) are lossy and one-way.

## Versioning

`v` in the catalog is the format version, currently 1, matching the `-v1` suffix. A change that makes a catalog parse differently or fail bumps `v`. Additive changes (new vectors, new optional functions) do not. The pinned MessageFormat 2 tag and the minimum CLDR move only by the update procedure in `mf2-suite/README.md` and a vector refresh.
