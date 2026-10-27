//! Tests for BOM handling, the repair pass and the dash-tab check.

use super::*;

fn text_of<'a>(src: &'a str, n: &Node) -> &'a str {
    let (s, e) = n.span.unwrap();
    &src[s..e]
}

#[test]
fn bom_is_skipped_and_spans_stay_in_source() {
    let src = "\u{feff}name: x\n";
    let root = parse(src).unwrap().unwrap();
    let v = root.get("name").unwrap();
    assert_eq!(v.scalar_value(), Some("x"));
    assert_eq!(text_of(src, v), "x");
}

#[test]
fn tab_after_colon_reads_like_libyaml() {
    let src = "name:\tx\na:\n  b:\t\ty # c\n";
    let root = parse(src).unwrap().unwrap();
    assert_eq!(root.get("name").unwrap().scalar_value(), Some("x"));
    let y = root.get("a").unwrap().get("b").unwrap();
    assert_eq!((y.scalar_value(), text_of(src, y)), (Some("y"), "y"));
    // A tab inside a quoted value is content, not a separator.
    let root = parse("k: \"a:\tb\"\n").unwrap().unwrap();
    assert_eq!(root.get("k").unwrap().scalar_value(), Some("a:\tb"));
}

#[test]
fn quoted_continuation_at_column_zero() {
    let src = "name: \"x\ny\"\nz: 'p\n\nq' # c\nn:\n  k: \"u\n v\"\nend: 1\n";
    let root = parse(src).unwrap().unwrap();
    let name = root.get("name").unwrap();
    assert_eq!(name.scalar_value(), Some("x y"));
    assert_eq!(text_of(src, name), "\"x\ny\"");
    let z = root.get("z").unwrap();
    assert_eq!(z.scalar_value(), Some("p\nq"));
    assert_eq!(text_of(src, z), "'p\n\nq'");
    let k = root.get("n").unwrap().get("k").unwrap();
    assert_eq!(k.scalar_value(), Some("u v"));
    let end = root.get("end").unwrap();
    assert_eq!(text_of(src, end), "1");
}

#[test]
fn flow_span_ends_at_bracket() {
    let src = "t: [a, b] # c\nm: {a: 1}   # d\n";
    let root = parse(src).unwrap().unwrap();
    assert_eq!(text_of(src, root.get("t").unwrap()), "[a, b]");
    assert_eq!(text_of(src, root.get("m").unwrap()), "{a: 1}");
}

#[test]
fn to_orig_maps_inserted_runs() {
    let ins = [(4, 2), (10, 1)];
    assert_eq!(repair::OrigMap::new(&ins).orig(3), 3);
    assert_eq!(repair::OrigMap::new(&ins).orig(4), 4);
    assert_eq!(repair::OrigMap::new(&ins).orig(5), 4);
    assert_eq!(repair::OrigMap::new(&ins).orig(6), 4);
    assert_eq!(repair::OrigMap::new(&ins).orig(12), 10);
    assert_eq!(repair::OrigMap::new(&ins).orig(13), 10);
    assert_eq!(repair::OrigMap::new(&ins).orig(14), 11);
}

#[test]
fn tab_led_continuation_reads_like_libyaml() {
    let root = parse("name: \"x\n\ty\"\nz: 'a\n\t b'\n").unwrap().unwrap();
    assert_eq!(root.get("name").unwrap().scalar_value(), Some("x y"));
    assert_eq!(root.get("z").unwrap().scalar_value(), Some("a b"));
}

#[test]
fn tab_after_sequence_dash_is_rejected() {
    let err = parse("tags:\n  -\tx\n").unwrap_err();
    assert_eq!(
        err.to_string(),
        "yaml: line 2: found character that cannot start any token"
    );
    assert!(parse("tags:\n  - \tx\n").is_err());
    assert!(parse("tags:\n  - x\t# c\n").is_ok());
    assert!(parse("b: |\n  -\tx\n").is_ok());
    assert!(parse("q: \"a\n  -\tx\"\n").is_ok());
}

#[test]
fn wrong_candidates_are_withdrawn() {
    // A tab after `:` inside a quoted value, next to a real one.
    let src = "a: \"k:\tv\"\nb:\tw\nc: |\n  d:\te\n";
    let root = parse(src).unwrap().unwrap();
    assert_eq!(root.get("a").unwrap().scalar_value(), Some("k:\tv"));
    assert_eq!(root.get("b").unwrap().scalar_value(), Some("w"));
    assert_eq!(root.get("c").unwrap().scalar_value(), Some("d:\te\n"));
    // Document markers inside quotes stay errors, as in Go.
    assert!(parse("a: \"x\n---\ny\"\n").is_err());
}

#[test]
fn repair_is_linear() {
    let mut src = String::new();
    for i in 0..20_000 {
        src.push_str(&format!("k{i}:\tv\nq{i}: \"x\ny\"\n"));
    }
    PARSES.set(0);
    let start = std::time::Instant::now();
    let root = parse(&src).unwrap().unwrap();
    assert!(PARSES.get() <= 3, "{} parses", PARSES.get());
    assert_eq!(root.get("q19999").unwrap().scalar_value(), Some("x y"));
    assert!(
        start.elapsed() < std::time::Duration::from_secs(2),
        "{:?}",
        start.elapsed()
    );
}

#[test]
fn quotes_inside_plain_scalars_are_text() {
    for (src, key, want) in [
        (
            "name: Rock, 'n roll\nport:\t8080\ntags: ['x']\n",
            "name",
            "Rock, 'n roll",
        ),
        (
            "extra: what? 'tis\nport: 1\nname:\tx\n",
            "extra",
            "what? 'tis",
        ),
        ("extra: a - \"b\nport: 1\nname:\tx\n", "extra", "a - \"b"),
    ] {
        let root = parse(src)
            .unwrap_or_else(|e| panic!("{src:?}: {e}"))
            .unwrap();
        assert_eq!(root.get(key).unwrap().scalar_value(), Some(want), "{src:?}");
    }
}

#[test]
fn explicit_value_indicator_tab_is_rejected() {
    let err = parse("? name\n:\tb\n").unwrap_err();
    assert_eq!(
        err.to_string(),
        "yaml: line 2: found character that cannot start any token"
    );
    assert!(parse("? name\n: b\n").is_ok());
}

#[test]
fn long_line_repair_is_linear() {
    // One repairable error and a 500 KB line of quoted items.
    let mut src = String::from("name:\tx\nextra: [");
    for i in 0..100_000 {
        if i > 0 {
            src.push_str(", ");
        }
        src.push_str("'a'");
    }
    src.push_str("]\n");
    assert!(src.len() > 500_000);
    let start = std::time::Instant::now();
    let f = repair::candidates(&src);
    assert_eq!(f.tabs.len(), 1);
    assert!(f.inserts.is_empty());
    let took = start.elapsed();
    assert!(took < std::time::Duration::from_millis(500), "{took:?}");
    PARSES.set(0);
    let root = parse(&src).unwrap().unwrap();
    assert_eq!(root.get("name").unwrap().scalar_value(), Some("x"));
    assert!(PARSES.get() <= 2, "{} parses", PARSES.get());
}

#[test]
fn quote_context_is_lexical() {
    // No quoted scalar is seen inside plain text, so nothing is padded.
    for src in [
        "name: Rock, 'n roll\nport:\t8080\nx: y\n",
        "extra: what? 'tis\nname:\tx\nport: 1\n",
        "extra: a - \"b\nname:\tx\nport: 1\n",
    ] {
        assert!(repair::candidates(src).inserts.is_empty(), "{src:?}");
    }
    // In flow, `,` opens a node.
    assert_eq!(
        repair::candidates("t: [a, 'b\nc']\nname:\tx\n")
            .inserts
            .len(),
        1
    );
}

#[test]
fn a_misread_quote_falls_back_to_tab_fixes() {
    // A plain continuation line that starts with a quote looks like a
    // quoted scalar to the scan; the inserts break the parse, the tab
    // fixes alone do not.
    let src = "extra: a\n  'b\nname:\tx\nport: 1\n";
    assert!(!repair::candidates(src).inserts.is_empty());
    let root = parse(src).unwrap().unwrap();
    assert_eq!(root.get("extra").unwrap().scalar_value(), Some("a 'b"));
    assert_eq!(root.get("name").unwrap().scalar_value(), Some("x"));
}
