# i18n-v1 specification

Normative text for the corpora beside it. MUST, SHOULD and MAY are RFC 2119 words. Where this file and a corpus disagree, fix the one that is wrong; neither wins by default.

Not covered: message rendering APIs, TMS wire protocols, code generation. They consume this contract and add no rules to it.

## Standards chosen

| Concern | Standard |
|---------|----------|
| Locale tags | BCP 47 (RFC 5646) syntax, canonical case per RFC 5646 section 2.1.1; grammar subset below |
| Fallback | RFC 4647 section 3.4 Lookup; no CLDR parent-locale or likely-subtags data |
| Plural categories and rules | Unicode CLDR cardinal plural rules, categories `zero one two few many other` |
| Number, date, time formatting | CLDR data through the platform facility (`Intl` in TypeScript, `golang.org/x/text` in Go, ICU or equivalent elsewhere); never hand-rolled |
| Catalog encoding | JSON (RFC 8259), UTF-8, no BOM |
| Interchange | XLIFF 2.x, GNU gettext PO, flat JSON and ARB |

## Catalog document

One document per locale:

```json
{ "v": 1, "locale": "en", "messages": { "cli": { "files": { "$plural": { ... }, "$params": { ... } } } } }
```

- `v`: the number `1`. Any other value, including the string `"1"`, is `unsupported_version`.
- `locale`: a tag accepted by Locale tags below. The file name is not significant.
- `messages`: an object tree. `$schema` MAY appear at the top level and is ignored. No other top-level field is allowed.
- Decoding is strict: unknown fields, duplicate object keys at any depth and trailing data after the document are load errors.

### Keys

A message is addressed by its dotted path: object keys from `messages` down, joined with `.`. A key segment matches `^[A-Za-z0-9_-]+$`: non-empty, no dot, no whitespace, no `$`. Segments starting with `$` are reserved for the leaf fields below. Because a segment cannot hold a dot, dotted paths are unique.

### Nodes

A node is one of:

1. a string: shorthand for a literal message with no parameters;
2. a leaf object: has exactly one of `$text` or `$plural`, plus optional `$params` and `$desc`;
3. a namespace: a non-empty object of child nodes with no `$` keys.

An object holding `$text` or `$plural` is a leaf; any other `$` key in it is `unknown_reserved_key`, and a `$` key in a namespace likewise. Numbers, booleans, null and arrays are `invalid_node`.

Leaf fields:

| Field | Type | Meaning |
|-------|------|---------|
| `$text` | string | the message |
| `$plural` | `{ "on": name, "branches": { category: string } }` | plural message; `on` names the selecting parameter; `branches.other` is required; other keys are CLDR categories |
| `$params` | `{ name: type }` | parameter declarations |
| `$desc` | string | translator note; never rendered; MAY be dropped by tools that do not carry notes |

Parameter names match `^[A-Za-z_][A-Za-z0-9_]*$`. Types: `string`, `number`, `integer`, `date`, `time`, `datetime`. The type fixes how the runtime formats the argument (CLDR number, date, time patterns for the active locale); `string` is inserted as given.

Declaration rules:

- every placeholder in `$text` or in any plural branch MUST be declared in `$params`; a shorthand string has no declarations, so it MUST NOT contain a placeholder;
- a declared parameter need not be used;
- for `$plural`, `on` MUST be declared with type `number` or `integer`;
- the same parameter table applies to all branches.

A catalog MAY define a plural branch its locale never selects (for example `few` in `en`); it is kept and never chosen. Whether the branches a locale does need are all present is coverage reporting, not a load error.

## Parameters

The only placeholder form is `%{name}`. Nothing else is interpreted: `{{name}}`, `{name}`, `${name}`, `$1`, `$$`, `%s`, `%d`, `100%` and every other brace or dollar sequence is literal text and MUST come out byte for byte, in particular through any substitution machinery (no replacement-pattern or template-engine reinterpretation of `$` or `{`).

Escape: scan the text left to right; a run of `n` consecutive `%` immediately followed by `{` yields `floor(n/2)` literal `%`; if `n` is odd the run's last `%` and the `{` open a placeholder, if `n` is even the `{` is literal. A run of `%` not followed by `{` is literal. So `%%{a}` is the text `%{a}`, `%%%{a}` is `%` then placeholder `a`, `%%%%{a}` is `%%{a}`.

An opened placeholder must close with `}` and hold a valid parameter name between; anything else (`%{}`, `%{1a}`, `%{a b}`, `%{abc` with no closing brace) is `placeholder_malformed`. Unknown parameter is `placeholder_undeclared`. A malformed placeholder is never downgraded to literal text.

A parsed message is a segment list: literals (merged, empty ones dropped) and parameter references. The runtime substitutes by segment, never by re-scanning substituted output: an argument containing `%{x}`, braces or `$` is inserted verbatim.

## Plurals

Selection uses the CLDR cardinal rules of the active locale, applied to the numeric argument named by `on`; ports take them from their platform facility and MUST NOT carry their own tables. The result is a category; the branch with that name renders, else `other`. `plural.json` pins category results for a spread of locales (including every category) so a stale or divergent platform table is caught. Ordinal plurals and gender/select are out of scope for v1.

## Locale tags

Accepted grammar (lower-case here; input is case-insensitive):

```
tag      = language ["-" script] ["-" region] *("-" variant) *("-" extension) ["-" private]
language = 2*3ALPHA / 5*8ALPHA
script   = 4ALPHA
region   = 2ALPHA / 3DIGIT
variant  = 5*8alphanum / DIGIT 3alphanum
extension= singleton 1*("-" 2*8alphanum)        ; singleton: one alphanumeric except "x"
private  = "x" 1*("-" 1*8alphanum)
```

Not accepted: empty tag, `_` separators, whitespace, non-ASCII, extlang subtags (`zh-yue`; use `yue`), grandfathered and `i-` tags, a bare `x-` private tag, the wildcard `*`. POSIX forms (`en_US.UTF-8`) are for a platform adapter to convert before calling in. Rejection is `invalid_locale`.

Canonical form: language lower case, script title case, region upper case, variants, extension and private-use subtags lower case, `-` separators.

## Fallback

`lookup(tag)`: canonicalise, then truncate from the right one subtag at a time, dropping a singleton left at the end together with it (RFC 4647 section 3.4): `de-DE-u-co-phonebk` gives `de-DE-u-co-phonebk`, `de-DE-u-co`, `de-DE`, `de`.

`chain(tag, default)`: `lookup(tag)` followed by `lookup(default)`, duplicates removed, first occurrence kept. `zh-CN` with default `en`: `zh-CN`, `zh`, `en`. The default locale's own base tags are part of the chain (`fr-CA` with default `fr-FR` gives `fr-CA`, `fr`, `fr-FR`).

`resolve(tag, available, default)`: the first chain entry that is in `available` (compared canonical); none present returns no locale (not an error). Resolution never widens: `fr` does not select `fr-FR`, `zh-TW` does not select `zh-Hant`. A caller that wants that adds the tag to the request or maps it in its platform table.

`lookup` of a message key: walk the resolved chain (restricted to loaded catalogs) and take the first catalog that defines the key. Fallback is per key, not per catalog. A key defined nowhere is a missing-key condition for the runtime to surface; its representation is not part of this contract.

Both `tag` and `default` are explicit inputs; process environment is never read by the contract. That is the per-call override tests use. A malformed `tag` or `default` is `invalid_locale`.

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
| `unknown_reserved_key` | `$` key not defined above, or a non-`$` key beside `$text` or `$plural` |
| `empty_namespace` | namespace with no children |
| `invalid_node` | wrong value type; both or neither of `$text` and `$plural`; wrong type for a leaf field |
| `invalid_param_name` | `$params` name outside the identifier pattern |
| `invalid_param_type` | `$params` type not in the list |
| `placeholder_malformed` | opened placeholder that is not `%{name}` |
| `placeholder_undeclared` | placeholder with no `$params` entry |
| `plural_missing_other` | `branches` lacks `other` |
| `plural_invalid_category` | branch key not a CLDR category |
| `plural_param_undeclared` | `on` not in `$params` |
| `plural_param_type` | `on` declared with a type other than `number` or `integer` |

A catalog that fails to load is never partially used.

## Interchange mappings

Mappings are lossless for the fields below; anything else an external format carries (for example PO plural-form headers, XLIFF `<note>` priority) is dropped on import or regenerated on export and is not part of the contract. Vectors for these mappings belong to the interchange work and are not in v1 of the corpus.

| i18n-v1 | XLIFF 2.x | gettext PO | flat JSON / ARB |
|---------|-----------|------------|-----------------|
| dotted key | `<unit id>` | `msgctxt` (with `msgid` = source text) | object key |
| locale | `srcLang` / `trgLang` | `Language:` header | file name or `@@locale` (ARB) |
| `$text` | `<segment>` `<source>` / `<target>` | `msgid` / `msgstr` | string value |
| `$plural` branches | one `<unit>` per category, id `key#category` | `msgid_plural` with `msgstr[n]` ordered by the locale's CLDR categories | ICU-free: `key#category` entries; ARB `{count, plural, ...}` on export only |
| `%{name}` | `<ph id="name" equiv="%{name}"/>` | literal `%{name}` | literal `%{name}` |
| `$params` | `<ph>` attributes `type` | `#.` extracted comment `param name: type` | ARB `placeholders` |
| `$desc` | `<note category="description">` | `#.` extracted comment | `@key.description` |

Placeholders are opaque in transit: a translation MUST keep every `%{name}` present in the source and MUST NOT add undeclared ones.

## Versioning

`v` in the catalog and the `-v1` suffix move together. Additive, backward-compatible changes (new parameter types, new vectors) stay in v1; a change that makes a v1 document parse differently or fail is `i18n-v2`.
