//! Redacting transforms for the bus content opt-in (Go `RedactRequest`,
//! `RedactResponse`), for [`Client::with_request_content`] and
//! [`Client::with_response_content`](super::Client::with_response_content).
//! Feature `redact`.
//!
//! [`Client::with_request_content`]: super::Client::with_request_content

use std::borrow::Borrow;
use std::collections::BTreeMap;

use crate::redact::Redactor;

use super::types::{Part, Request, Response, ThinkingBlock, ToolCall};

/// What masked tool-call arguments become when they cannot be cleared:
/// a fixed JSON string, never derived from the input.
const PLACEHOLDER: &str = "\"REDACTED\"";

/// The value a member flagged in key context gets.
const REDACTED: &str = "REDACTED";

/// Nesting deeper than this does not decode: the arguments fail closed.
const MAX_DEPTH: usize = 128;

/// A request transform for
/// [`Client::with_request_content`](super::Client::with_request_content)
/// passing every text of the request through `r`: message content, text
/// parts, tool call arguments and thinking text.
///
/// Tool call arguments are masked on their decoded JSON, every string,
/// key and number on its own text, each object member also in key
/// context (`"key":value`, `key=value`), always, for rules that need
/// the key beside the value; a member matching there gets its value
/// replaced whole. Members keep their order. Arguments that do not
/// decode, a member that still matches with its value replaced, a match
/// across members of the input, or a result `r` still matches, become
/// the JSON string `"REDACTED"`: nothing is derived from their raw
/// text.
///
/// What `r` cannot vouch for is left out: `extensions`, media bytes (a
/// part keeps its kind and MIME type), thinking signatures and redacted
/// thinking, `provider_data`. Tool names, roles and the model stay.
///
/// `r` is a [`Redactor`], an `Arc<Redactor>` or a `&'static Redactor`
/// such as [`crate::redact::shared`].
pub fn redact_request<R>(r: R) -> impl Fn(Request) -> Request + Send + Sync + 'static
where
    R: Borrow<Redactor> + Send + Sync + 'static,
{
    move |mut req| {
        let r = r.borrow();
        req.extensions.clear();
        for m in &mut req.messages {
            m.content = apply(r, &m.content);
            redact_parts(r, &mut m.parts);
            redact_calls(r, &mut m.tool_calls);
            redact_thinking(r, &mut m.thinking);
        }
        req
    }
}

/// [`redact_request`] for the response payload: content and thinking
/// text through `r`, thinking signatures and redacted thinking left out.
pub fn redact_response<R>(r: R) -> impl Fn(Response) -> Response + Send + Sync + 'static
where
    R: Borrow<Redactor> + Send + Sync + 'static,
{
    move |mut resp| {
        let r = r.borrow();
        resp.content = apply(r, &resp.content);
        redact_thinking(r, &mut resp.thinking);
        resp
    }
}

fn apply(r: &Redactor, s: &str) -> String {
    r.apply(s).into_owned()
}

/// Whether `r` would change `s`.
fn flags(r: &Redactor, s: &str) -> bool {
    r.apply(s) != s
}

fn redact_parts(r: &Redactor, parts: &mut [Part]) {
    for p in parts {
        match p {
            Part::Text(t) => *t = apply(r, t),
            Part::Media(m) => m.data = Vec::new(),
        }
    }
}

fn redact_calls(r: &Redactor, calls: &mut [ToolCall]) {
    for c in calls {
        c.provider_data = BTreeMap::new();
        if !c.arguments.is_empty() {
            c.arguments = redact_json(r, &c.arguments);
        }
    }
}

fn redact_thinking(r: &Redactor, tbs: &mut [ThinkingBlock]) {
    for t in tbs {
        t.thinking = apply(r, &t.thinking);
        t.signature = String::new();
        t.redacted = String::new();
    }
}

/// `raw`, a JSON value, masked with `r`, failing closed; see
/// [`redact_request`]. Numbers keep their literal text, so a number
/// past what `f64` or `u64` hold is masked on the digits it was sent
/// with, never on a rounded copy. Members keep their order.
///
/// Placeholder when: `raw` does not decode; a member still matches in
/// key context with its value replaced; a match in the compact input
/// spans more than one token and is not a member the context check
/// replaced (masking a neighbor could break such a match up and let
/// the rest pass); or `r` still matches the result.
fn redact_json(r: &Redactor, raw: &str) -> String {
    let Some(v) = Json::parse(raw) else {
        return PLACEHOLDER.into();
    };
    let mut w = Walk {
        r,
        compact: String::new(),
        cleared: Vec::new(),
    };
    let Some(out) = w.value(&v) else {
        return PLACEHOLDER.into();
    };
    let covered = r.spans(&w.compact).iter().all(|s| {
        w.cleared
            .iter()
            .any(|&(from, to)| from <= s.start && s.end <= to)
    });
    let out = out.encode();
    if !covered || flags(r, &out) {
        return PLACEHOLDER.into();
    }
    out
}

/// Masks a decoded value while writing the input back compact, in
/// order, noting the ranges of it the masking clears on its own: each
/// scalar token, and each member the key-context check replaced.
struct Walk<'a> {
    r: &'a Redactor,
    compact: String,
    cleared: Vec<(usize, usize)>,
}

impl Walk<'_> {
    /// The masked value; `None` fails the whole arguments closed.
    fn value(&mut self, v: &Json) -> Option<Json> {
        match v {
            Json::Null | Json::Bool(_) => {
                v.write(&mut self.compact);
                Some(v.clone())
            }
            Json::Num(_) | Json::Str(_) => {
                self.token(v);
                Some(scalar(self.r, v))
            }
            Json::Arr(items) => {
                self.compact.push('[');
                let mut out = Vec::with_capacity(items.len());
                for (i, e) in items.iter().enumerate() {
                    if i > 0 {
                        self.compact.push(',');
                    }
                    out.push(self.value(e)?);
                }
                self.compact.push(']');
                Some(Json::Arr(out))
            }
            Json::Obj(members) => {
                self.compact.push('{');
                let mut out: Vec<(String, Json)> = Vec::with_capacity(members.len());
                for (i, (k, e)) in members.iter().enumerate() {
                    if i > 0 {
                        self.compact.push(',');
                    }
                    let start = self.compact.len();
                    self.token(&Json::Str(k.clone()));
                    self.compact.push(':');
                    let masked = match e {
                        Json::Num(_) | Json::Str(_) => {
                            self.token(e);
                            if in_context(self.r, k, e) {
                                // Replaced whole; still matching with
                                // the value gone means the key holds it.
                                if in_context(self.r, k, &Json::Str(REDACTED.into())) {
                                    return None;
                                }
                                self.cleared.push((start, self.compact.len()));
                                Json::Str(REDACTED.into())
                            } else {
                                scalar(self.r, e)
                            }
                        }
                        _ => self.value(e)?,
                    };
                    // Two keys masked to the same text keep one member,
                    // in the first one's place, with the later value:
                    // the copy only describes.
                    let key = apply(self.r, k);
                    match out.iter_mut().find(|(k, _)| *k == key) {
                        Some(slot) => slot.1 = masked,
                        None => out.push((key, masked)),
                    }
                }
                self.compact.push('}');
                Some(Json::Obj(out))
            }
        }
    }

    /// Write a scalar token, noting its range as cleared.
    fn token(&mut self, v: &Json) {
        let start = self.compact.len();
        v.write(&mut self.compact);
        self.cleared.push((start, self.compact.len()));
    }
}

/// A string or number masked on its decoded text; a masked number
/// becomes a string.
fn scalar(r: &Redactor, v: &Json) -> Json {
    match v {
        Json::Str(s) => Json::Str(apply(r, s)),
        Json::Num(n) => {
            let m = apply(r, n);
            if m != *n {
                Json::Str(m)
            } else {
                Json::Num(n.clone())
            }
        }
        other => other.clone(),
    }
}

/// Whether `r` matches the member `k` with the scalar `e` rendered
/// beside its key, as `"key":value` or `key=value`. Always checked,
/// whether or not the key or value alone matches.
fn in_context(r: &Redactor, k: &str, e: &Json) -> bool {
    let (text, wire) = match e {
        Json::Str(s) => (s.as_str(), json_string(s)),
        Json::Num(n) => (n.as_str(), n.clone()),
        _ => return false,
    };
    [format!("{}:{wire}", json_string(k)), format!("{k}={text}")]
        .iter()
        .any(|s| flags(r, s))
}

fn json_string(s: &str) -> String {
    serde_json::to_string(s).expect("a string encodes")
}

/// A decoded JSON value that keeps each number's literal text.
#[derive(Debug, Clone, PartialEq)]
enum Json {
    Null,
    Bool(bool),
    Num(String),
    Str(String),
    Arr(Vec<Json>),
    Obj(Vec<(String, Json)>),
}

impl Json {
    /// One RFC 8259 value with optional surrounding whitespace; `None`
    /// for anything else, trailing data and nesting past [`MAX_DEPTH`]
    /// included.
    fn parse(src: &str) -> Option<Json> {
        let mut p = Parser { src, i: 0 };
        let v = p.value(0)?;
        p.ws();
        (p.i == src.len()).then_some(v)
    }

    fn encode(&self) -> String {
        let mut out = String::new();
        self.write(&mut out);
        out
    }

    fn write(&self, out: &mut String) {
        match self {
            Json::Null => out.push_str("null"),
            Json::Bool(b) => out.push_str(if *b { "true" } else { "false" }),
            Json::Num(n) => out.push_str(n),
            Json::Str(s) => out.push_str(&json_string(s)),
            Json::Arr(items) => {
                out.push('[');
                for (i, e) in items.iter().enumerate() {
                    if i > 0 {
                        out.push(',');
                    }
                    e.write(out);
                }
                out.push(']');
            }
            Json::Obj(members) => {
                out.push('{');
                for (i, (k, e)) in members.iter().enumerate() {
                    if i > 0 {
                        out.push(',');
                    }
                    out.push_str(&json_string(k));
                    out.push(':');
                    e.write(out);
                }
                out.push('}');
            }
        }
    }
}

struct Parser<'a> {
    src: &'a str,
    i: usize,
}

impl Parser<'_> {
    fn peek(&self) -> Option<u8> {
        self.src.as_bytes().get(self.i).copied()
    }

    fn ws(&mut self) {
        while matches!(self.peek(), Some(b' ' | b'\t' | b'\n' | b'\r')) {
            self.i += 1;
        }
    }

    fn eat(&mut self, b: u8) -> Option<()> {
        self.ws();
        (self.peek()? == b).then(|| self.i += 1)
    }

    fn value(&mut self, depth: usize) -> Option<Json> {
        if depth > MAX_DEPTH {
            return None;
        }
        self.ws();
        match self.peek()? {
            b'{' => self.object(depth),
            b'[' => self.array(depth),
            b'"' => self.string().map(Json::Str),
            b't' => self.literal("true", Json::Bool(true)),
            b'f' => self.literal("false", Json::Bool(false)),
            b'n' => self.literal("null", Json::Null),
            b'-' | b'0'..=b'9' => self.number(),
            _ => None,
        }
    }

    fn literal(&mut self, word: &str, v: Json) -> Option<Json> {
        self.src[self.i..].starts_with(word).then(|| {
            self.i += word.len();
            v
        })
    }

    fn object(&mut self, depth: usize) -> Option<Json> {
        self.i += 1;
        let mut members = Vec::new();
        self.ws();
        if self.peek()? == b'}' {
            self.i += 1;
            return Some(Json::Obj(members));
        }
        loop {
            self.ws();
            if self.peek()? != b'"' {
                return None;
            }
            let k = self.string()?;
            self.eat(b':')?;
            members.push((k, self.value(depth + 1)?));
            self.ws();
            match self.peek()? {
                b',' => self.i += 1,
                b'}' => {
                    self.i += 1;
                    return Some(Json::Obj(members));
                }
                _ => return None,
            }
        }
    }

    fn array(&mut self, depth: usize) -> Option<Json> {
        self.i += 1;
        let mut items = Vec::new();
        self.ws();
        if self.peek()? == b']' {
            self.i += 1;
            return Some(Json::Arr(items));
        }
        loop {
            items.push(self.value(depth + 1)?);
            self.ws();
            match self.peek()? {
                b',' => self.i += 1,
                b']' => {
                    self.i += 1;
                    return Some(Json::Arr(items));
                }
                _ => return None,
            }
        }
    }

    /// A string token, decoded by serde_json (escapes, control
    /// characters, surrogates).
    fn string(&mut self) -> Option<String> {
        let start = self.i;
        self.i += 1;
        loop {
            match self.peek()? {
                b'"' => break,
                b'\\' => self.i += 2,
                _ => self.i += 1,
            }
        }
        self.i += 1;
        serde_json::from_str(self.src.get(start..self.i)?).ok()
    }

    /// A number token, its text as sent: `-? (0 | [1-9][0-9]*)
    /// (. [0-9]+)? ([eE] [+-]? [0-9]+)?`.
    fn number(&mut self) -> Option<Json> {
        let start = self.i;
        let digits = |p: &mut Self| {
            let from = p.i;
            while matches!(p.peek(), Some(b'0'..=b'9')) {
                p.i += 1;
            }
            p.i > from
        };
        if self.peek() == Some(b'-') {
            self.i += 1;
        }
        match self.peek()? {
            b'0' => self.i += 1,
            b'1'..=b'9' => {
                digits(self);
            }
            _ => return None,
        }
        if self.peek() == Some(b'.') {
            self.i += 1;
            if !digits(self) {
                return None;
            }
        }
        if matches!(self.peek(), Some(b'e' | b'E')) {
            self.i += 1;
            if matches!(self.peek(), Some(b'+' | b'-')) {
                self.i += 1;
            }
            if !digits(self) {
                return None;
            }
        }
        Some(Json::Num(self.src[start..self.i].to_string()))
    }
}

#[cfg(test)]
mod tests {
    use super::Json;

    #[test]
    fn parse_round_trips_and_refuses() {
        for ok in [
            "null",
            " true ",
            "-0.5e-7",
            r#"{"a":[1,{"b":"\u00e9\n"}],"c":{}}"#,
            "[]",
            r#""\ud83d\ude00""#,
        ] {
            let v = Json::parse(ok).unwrap_or_else(|| panic!("{ok}"));
            let back: serde_json::Value = serde_json::from_str(&v.encode()).unwrap();
            let want: serde_json::Value = serde_json::from_str(ok).unwrap();
            assert_eq!(back, want, "{ok}");
        }
        for bad in [
            "",
            "01",
            "1.",
            "-",
            "+1",
            ".5",
            "1e",
            "tru",
            "[1,]",
            "{\"a\":1,}",
            "{a:1}",
            "\"x",
            "\"\\x\"",
            "\"\u{1}\"",
            "[1 2]",
            "{\"a\" 1}",
            "1 2",
            "nul",
        ] {
            assert!(Json::parse(bad).is_none(), "{bad:?}");
        }
    }
}
