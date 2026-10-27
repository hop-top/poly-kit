/**
 * Go `%q` quoting, for error texts the llm-v1 vectors pin byte for
 * byte across ports.
 *
 * @internal
 */

const ESCAPES: Record<string, string> = {
  '"': '\\"',
  "\\": "\\\\",
  "\u0007": "\\a",
  "\b": "\\b",
  "\f": "\\f",
  "\n": "\\n",
  "\r": "\\r",
  "\t": "\\t",
  "\v": "\\v",
};

const PRINTABLE = /^[\p{L}\p{M}\p{N}\p{P}\p{S}]$/u;

/** Quotes `s` as Go's `strconv.Quote` (and `%q`) does. */
export function goQuote(s: string): string {
  let out = '"';
  for (const ch of s) {
    const esc = ESCAPES[ch];
    if (esc !== undefined) {
      out += esc;
      continue;
    }
    const c = ch.codePointAt(0)!;
    if (c >= 0xd800 && c <= 0xdfff) {
      // A lone surrogate reaches Go as U+FFFD, which prints as itself.
      out += "�";
    } else if (ch === " " || PRINTABLE.test(ch)) {
      out += ch;
    } else if (c < 0x20 || c === 0x7f) {
      out += "\\x" + c.toString(16).padStart(2, "0");
    } else if (c < 0x10000) {
      out += "\\u" + c.toString(16).padStart(4, "0");
    } else {
      out += "\\U" + c.toString(16).padStart(8, "0");
    }
  }
  return out + '"';
}
