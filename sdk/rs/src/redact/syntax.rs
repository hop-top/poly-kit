//! Go RE2 pattern syntax compiled on the `regex` crate.
//!
//! Both engines are linear-time and leftmost-first, and most syntax is
//! shared. The differences that change what a pattern matches are
//! rewritten here, before compiling:
//!
//! - `\d`, `\s`, `\w` (and negations) are ASCII in Go, Unicode in Rust.
//!   They become ASCII classes; Go's `\s` is `[\t\n\f\r ]`, without
//!   `\v`.
//! - `\b`, `\B` are ASCII in Go; they become `(?-u:\b)`, `(?-u:\B)`.
//! - `\Q…\E` (Go literal text) has no Rust form; its text is escaped.
//! - Inside a class, Go reads `[` (outside `[:name:]`), `&` and `~`
//!   literally; Rust reads `[`, `&&` and `~~` as nesting and set
//!   operators. They are escaped.
//! - Go reads a `{` that does not open `{n}`, `{n,}` or `{n,m}` as a
//!   literal (`{{`, `a{,3}`); Rust rejects it. It is escaped.
//!
//! Anything else Rust rejects (lookaround, octal escapes) is an error,
//! never a silently different pattern.

use regex_syntax::ast::{
    self, Ast, ClassBracketed, ClassPerlKind, ClassSet, ClassSetItem, ClassSetRange, ClassSetUnion,
    Flag, Flags, FlagsItem, FlagsItemKind, Group, GroupKind, Literal, LiteralKind, Position, Span,
};

/// Rewrites a Go RE2 pattern into `regex` crate syntax with Go's meaning.
pub(crate) fn translate(pattern: &str) -> Result<String, String> {
    let lexed = prelex(pattern)?;
    let mut parsed = ast::parse::Parser::new()
        .parse(&lexed)
        .map_err(|e| e.to_string())?;
    rewrite(&mut parsed);
    let mut out = String::with_capacity(lexed.len() + 16);
    ast::print::Printer::new()
        .print(&parsed, &mut out)
        .map_err(|e| e.to_string())?;
    Ok(out)
}

/// Expands `\Q…\E` and escapes class characters Go reads literally.
fn prelex(p: &str) -> Result<String, String> {
    let chars: Vec<char> = p.chars().collect();
    let mut out = String::with_capacity(p.len() + 8);
    let mut i = 0;
    // in_class: inside [...]; class_start: at its first position, where
    // `]` (after an optional `^`) is literal.
    let (mut in_class, mut class_start) = (false, false);
    while i < chars.len() {
        let c = chars[i];
        if c == '\\' {
            let Some(&n) = chars.get(i + 1) else {
                return Err("trailing backslash".into());
            };
            if n == 'Q' && !in_class {
                i += 2;
                while i < chars.len() && !(chars[i] == '\\' && chars.get(i + 1) == Some(&'E')) {
                    out.push_str(&regex_syntax::escape(&chars[i].to_string()));
                    i += 1;
                }
                i += 2; // past \E, or past the end
                continue;
            }
            out.push(c);
            out.push(n);
            i += 2;
            // \p{Name}, \x{hex}: the braces belong to the escape.
            if matches!(n, 'p' | 'P' | 'x') && chars.get(i) == Some(&'{') {
                while i < chars.len() {
                    out.push(chars[i]);
                    i += 1;
                    if chars[i - 1] == '}' {
                        break;
                    }
                }
            }
            class_start = false;
            continue;
        }
        if !in_class {
            if c == '{' && !is_repeat(&chars[i..]) {
                out.push_str("\\{");
                i += 1;
                continue;
            }
            out.push(c);
            if c == '[' {
                in_class = true;
                class_start = true;
                if chars.get(i + 1) == Some(&'^') {
                    out.push('^');
                    i += 1;
                }
            }
            i += 1;
            continue;
        }
        match c {
            ']' if class_start => out.push_str("\\]"),
            ']' => {
                in_class = false;
                out.push(']');
            }
            '[' if chars.get(i + 1) == Some(&':') => {
                let rest: String = chars[i..].iter().collect();
                let Some(end) = rest.find(":]") else {
                    return Err("unclosed POSIX class".into());
                };
                out.push_str(&rest[..end + 2]);
                i += rest[..end + 2].chars().count();
                class_start = false;
                continue;
            }
            '[' | '&' | '~' => {
                out.push('\\');
                out.push(c);
            }
            _ => out.push(c),
        }
        class_start = false;
        i += 1;
    }
    Ok(out)
}

/// Whether `s` (starting at `{`) is a Go repetition: `{n}`, `{n,}` or
/// `{n,m}` with decimal n and m.
fn is_repeat(s: &[char]) -> bool {
    let mut i = 1;
    let digits = |i: &mut usize| {
        let from = *i;
        while s.get(*i).is_some_and(char::is_ascii_digit) {
            *i += 1;
        }
        *i > from
    };
    if !digits(&mut i) {
        return false;
    }
    if s.get(i) == Some(&',') {
        i += 1;
        digits(&mut i);
    }
    s.get(i) == Some(&'}')
}

fn span() -> Span {
    let p = Position::new(0, 1, 1);
    Span::new(p, p)
}

/// ASCII ranges for a Perl class, as Go defines them.
fn perl_ranges(kind: &ClassPerlKind) -> &'static [(char, char)] {
    match kind {
        ClassPerlKind::Digit => &[('0', '9')],
        ClassPerlKind::Space => &[('\t', '\n'), ('\x0c', '\r'), (' ', ' ')],
        ClassPerlKind::Word => &[('0', '9'), ('A', 'Z'), ('_', '_'), ('a', 'z')],
    }
}

fn ascii_class(kind: &ClassPerlKind, negated: bool) -> ClassBracketed {
    let lit = |c: char| Literal {
        span: span(),
        kind: LiteralKind::Verbatim,
        c,
    };
    let items = perl_ranges(kind)
        .iter()
        .map(|&(lo, hi)| {
            if lo == hi {
                ClassSetItem::Literal(lit(lo))
            } else {
                ClassSetItem::Range(ClassSetRange {
                    span: span(),
                    start: lit(lo),
                    end: lit(hi),
                })
            }
        })
        .collect();
    ClassBracketed {
        span: span(),
        negated,
        kind: ClassSet::Item(ClassSetItem::Union(ClassSetUnion {
            span: span(),
            items,
        })),
    }
}

fn rewrite(node: &mut Ast) {
    match node {
        Ast::ClassPerl(p) => {
            *node = Ast::class_bracketed(ascii_class(&p.kind, p.negated));
        }
        Ast::Assertion(a)
            if matches!(
                a.kind,
                ast::AssertionKind::WordBoundary | ast::AssertionKind::NotWordBoundary
            ) =>
        {
            let inner = Ast::assertion((**a).clone());
            let flags = Flags {
                span: span(),
                items: vec![
                    FlagsItem {
                        span: span(),
                        kind: FlagsItemKind::Negation,
                    },
                    FlagsItem {
                        span: span(),
                        kind: FlagsItemKind::Flag(Flag::Unicode),
                    },
                ],
            };
            *node = Ast::group(Group {
                span: span(),
                kind: GroupKind::NonCapturing(flags),
                ast: Box::new(inner),
            });
        }
        Ast::ClassBracketed(c) => rewrite_set(&mut c.kind),
        Ast::Repetition(r) => rewrite(&mut r.ast),
        Ast::Group(g) => rewrite(&mut g.ast),
        Ast::Alternation(a) => a.asts.iter_mut().for_each(rewrite),
        Ast::Concat(c) => c.asts.iter_mut().for_each(rewrite),
        _ => {}
    }
}

fn rewrite_set(set: &mut ClassSet) {
    match set {
        ClassSet::Item(item) => rewrite_item(item),
        ClassSet::BinaryOp(op) => {
            rewrite_set(&mut op.lhs);
            rewrite_set(&mut op.rhs);
        }
    }
}

fn rewrite_item(item: &mut ClassSetItem) {
    match item {
        ClassSetItem::Perl(p) => {
            *item = ClassSetItem::Bracketed(Box::new(ascii_class(&p.kind, p.negated)));
        }
        ClassSetItem::Bracketed(b) => rewrite_set(&mut b.kind),
        ClassSetItem::Union(u) => u.items.iter_mut().for_each(rewrite_item),
        _ => {}
    }
}

#[cfg(test)]
mod tests {
    use super::translate;

    #[test]
    fn rewrites_go_only_meanings() {
        for (go, rust) in [
            (r"\d+", "[0-9]+"),
            (r"[\w.-]", "[[0-9A-Z_a-z].-]"),
            (r"\S", "[^\t-\n\x0c-\r ]"),
            (r"\bx", r"(?-u:\b)x"),
            (r"\Qa.b\E", r"a\.b"),
            (r#"["'[]"#, r#"["'\[]"#),
            (r"[]a]", r"[\]a]"),
            (r"[^]a]", r"[^\]a]"),
            (r"[a&&b~~c]", r"[a\&\&b\~\~c]"),
            (r"[[:alpha:]_]", r"[[:alpha:]_]"),
            (r"a{,3}b{2}c{1,}d{1,2}", r"a\{,3}b{2}c{1,}d{1,2}"),
            (r"\${{[^}]+}}", r"\$\{\{[^}]+}}"),
            (r"\p{Nd}\x{e9}[\p{L}\x{41}]", r"\p{Nd}\x{E9}[\p{L}\x{41}]"),
        ] {
            assert_eq!(translate(go).unwrap(), rust, "{go}");
        }
    }

    #[test]
    fn rejects_what_rust_cannot_read() {
        assert!(translate("a(?=b)").is_err());
    }
}
