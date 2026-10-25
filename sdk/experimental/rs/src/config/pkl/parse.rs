//! Source-text parsing of a PKL config module. Ports Go
//! `pkl/parse.go` and `pkl/schema.go`: the same line-oriented regex
//! reading, not a PKL evaluator.

use std::collections::HashMap;
use std::sync::OnceLock;

use regex::Regex;

use super::{
    Constraint, ConstraintKind, ConstraintValue, FieldCondition, FieldDef, FieldType, Schema,
};
use crate::config::Value;

// Go's RE2 classes are ASCII; spell them out (Rust's are Unicode).
const W: &str = "[0-9A-Za-z_]";

macro_rules! re {
    ($name:ident, $pat:expr) => {
        fn $name() -> &'static Regex {
            static R: OnceLock<Regex> = OnceLock::new();
            R.get_or_init(|| Regex::new(AsRef::<str>::as_ref(&$pat)).expect("static pattern"))
        }
    };
}

re!(
    re_module,
    r"(?m)^module[\t\n\f\r ]+([^\t\n\f\r ]+)".to_string()
);
re!(re_field, format!(r"^({W}+)[\t\n\f\r ]*:[\t\n\f\r ]*(.+)$"));
re!(
    re_class_decl,
    format!(r"^class[\t\n\f\r ]+({W}+)[\t\n\f\r ]*\{{")
);
re!(re_union_val, r#""([^"]+)""#);
re!(
    re_default,
    r"^(.+?)[\t\n\f\r ]*=[\t\n\f\r ]*(.+)$".to_string()
);
re!(
    re_between,
    r"isBetween\(([0-9]+),[\t\n\f\r ]*([0-9]+)\)".to_string()
);
re!(re_pattern, r"matches\((.+)\)");
re!(
    re_min_len,
    r"length[\t\n\f\r ]*>=[\t\n\f\r ]*([0-9]+)".to_string()
);
re!(
    re_max_len,
    r"length[\t\n\f\r ]*<=[\t\n\f\r ]*([0-9]+)".to_string()
);
re!(
    re_len_btw,
    r"length\.isBetween\(([0-9]+),[\t\n\f\r ]*([0-9]+)\)".to_string()
);
re!(
    re_group_ann,
    r#"@wizard\.group[\t\n\f\r ]+"([^"]+)""#.to_string()
);
re!(re_when_ann, r"@wizard\.when[\t\n\f\r ]+(.+)");
re!(re_interp, r"#\{");
re!(re_const_type, format!(r"^({W}+)\((.+)\)$"));
re!(re_listing, format!(r"^Listing<({W}+)>(\?)?$"));
re!(
    re_ident,
    r"(?-u:\b)([a-z_][0-9A-Za-z_]*)(?-u:\b)".to_string()
);

fn atoi(s: &str) -> i64 {
    s.parse().unwrap_or(0)
}

/// The class name a field refers to, stashed in `enum_values[0]`.
fn type_name(fd: &FieldDef) -> Option<&str> {
    match (fd.field_type, fd.enum_values.as_slice()) {
        (FieldType::String, [n]) if n.starts_with(|c: char| c.is_ascii_uppercase()) => Some(n),
        _ => None,
    }
}

pub(crate) fn parse_field(line: &str, comments: &[String]) -> Option<FieldDef> {
    let m = re_field().captures(line)?;
    let mut fd = FieldDef {
        path: m[1].to_string(),
        required: true,
        ..FieldDef::default()
    };
    let mut type_expr = m[2].trim().to_string();
    let mut default_str = String::new();
    if let Some(dm) = re_default().captures(&type_expr.clone()) {
        type_expr = dm[1].trim().to_string();
        default_str = dm[2].trim().to_string();
    }
    if let Some(t) = type_expr.strip_suffix('?') {
        fd.required = false;
        type_expr = t.to_string();
    }
    if let Some(vals) = parse_union_type(&type_expr) {
        fd.field_type = FieldType::StringEnum;
        if default_str.is_empty() {
            fd.default = Some(Value::String(vals[0].clone()));
        } else {
            set_default(&mut fd, &default_str);
        }
        fd.enum_values = vals;
        apply_annotations(&mut fd, comments);
        return Some(fd);
    }
    if let Some(lm) = re_listing().captures(&type_expr) {
        fd.field_type = FieldType::StringList;
        if lm.get(2).is_some() {
            fd.required = false;
        }
        if !default_str.is_empty() {
            set_default(&mut fd, &default_str);
        }
        apply_annotations(&mut fd, comments);
        return Some(fd);
    }
    if let Some(cm) = re_const_type().captures(&type_expr) {
        fd.field_type = resolve_base_type(&cm[1]);
        fd.constraints = parse_constraints(&cm[2]);
        if !default_str.is_empty() {
            set_default(&mut fd, &default_str);
        }
        apply_annotations(&mut fd, comments);
        return Some(fd);
    }
    fd.field_type = resolve_base_type(&type_expr);
    if fd.field_type == FieldType::String
        && type_expr.starts_with(|c: char| c.is_ascii_uppercase())
        && type_expr != "String"
    {
        fd.enum_values = vec![type_expr.clone()];
    }
    if !default_str.is_empty() {
        set_default(&mut fd, &default_str);
    }
    apply_annotations(&mut fd, comments);
    Some(fd)
}

fn set_default(fd: &mut FieldDef, s: &str) {
    let (v, computed) = parse_default(s, fd.field_type);
    fd.default = Some(v);
    fd.computed = computed;
}

pub(crate) fn resolve_base_type(name: &str) -> FieldType {
    match name {
        "Int" => FieldType::Int,
        "Float" => FieldType::Float,
        "Boolean" => FieldType::Bool,
        "Duration" => FieldType::Duration,
        _ => FieldType::String,
    }
}

pub(crate) fn parse_union_type(s: &str) -> Option<Vec<String>> {
    if !s.contains('|') || !s.contains('"') {
        return None;
    }
    let vals: Vec<String> = re_union_val()
        .captures_iter(s)
        .map(|c| c[1].to_string())
        .collect();
    (vals.len() >= 2).then_some(vals)
}

pub(crate) fn parse_constraints(expr: &str) -> Vec<Constraint> {
    let mut cs = Vec::new();
    let c = |kind, value| Constraint { kind, value };
    if let Some(m) = re_len_btw().captures(expr) {
        cs.push(c(ConstraintKind::MinLen, ConstraintValue::Int(atoi(&m[1]))));
        cs.push(c(ConstraintKind::MaxLen, ConstraintValue::Int(atoi(&m[2]))));
    } else if let Some(m) = re_between().captures(expr) {
        cs.push(c(
            ConstraintKind::Between,
            ConstraintValue::Between(atoi(&m[1]), atoi(&m[2])),
        ));
    }
    if let Some(m) = re_pattern().captures(expr) {
        cs.push(c(
            ConstraintKind::Pattern,
            ConstraintValue::Str(m[1].trim_matches('"').to_string()),
        ));
    }
    if let Some(m) = re_min_len().captures(expr) {
        cs.push(c(ConstraintKind::MinLen, ConstraintValue::Int(atoi(&m[1]))));
    }
    if let Some(m) = re_max_len().captures(expr) {
        cs.push(c(ConstraintKind::MaxLen, ConstraintValue::Int(atoi(&m[1]))));
    }
    cs
}

/// `(group, when, description)` from `///` comments.
pub(crate) fn parse_annotations(comments: &[String]) -> (String, Option<FieldCondition>, String) {
    let (mut group, mut when) = (String::new(), None);
    let mut desc = Vec::new();
    for c in comments {
        let text = c.strip_prefix("///").unwrap_or(c).trim();
        if let Some(m) = re_group_ann().captures(text) {
            group = m[1].to_string();
            continue;
        }
        if let Some(m) = re_when_ann().captures(text) {
            let expr = m[1].trim().to_string();
            when = Some(FieldCondition {
                depends_on: extract_field_refs(&expr),
                expression: expr,
            });
            continue;
        }
        if !text.is_empty() {
            desc.push(text.to_string());
        }
    }
    (group, when, desc.join(" "))
}

fn apply_annotations(fd: &mut FieldDef, comments: &[String]) {
    let (g, w, d) = parse_annotations(comments);
    fd.group = g;
    fd.when = w;
    fd.description = d;
}

fn extract_field_refs(expr: &str) -> Vec<String> {
    const KEYWORDS: [&str; 9] = [
        "true", "false", "null", "and", "or", "not", "is", "if", "else",
    ];
    let mut refs: Vec<String> = Vec::new();
    for m in re_ident().captures_iter(expr) {
        let id = &m[1];
        if !KEYWORDS.contains(&id) && !refs.iter().any(|r| r == id) {
            refs.push(id.to_string());
        }
    }
    refs
}

pub(crate) fn parse_default(s: &str, ft: FieldType) -> (Value, bool) {
    let computed = re_interp().is_match(s);
    let v = match ft {
        FieldType::String | FieldType::StringEnum => Value::String(s.trim_matches('"').to_string()),
        FieldType::Int => s
            .parse::<i64>()
            .map_or_else(|_| Value::String(s.to_string()), Value::Int),
        FieldType::Float => s
            .parse::<f64>()
            .ok()
            .filter(|f| f.is_finite())
            .map_or_else(|| Value::String(s.to_string()), Value::Float),
        FieldType::Bool => Value::Bool(s == "true"),
        _ => Value::String(s.to_string()),
    };
    (v, computed)
}

fn brace_delta(s: &str) -> i64 {
    s.matches('{').count() as i64 - s.matches('}').count() as i64
}

fn parse_classes(source: &str) -> HashMap<String, String> {
    let lines: Vec<&str> = source.split('\n').collect();
    let mut classes = HashMap::new();
    for (i, line) in lines.iter().enumerate() {
        let trimmed = line.trim();
        let Some(m) = re_class_decl().captures(trimmed) else {
            continue;
        };
        let mut depth = brace_delta(trimmed);
        let mut body = Vec::new();
        for l in lines.iter().skip(i + 1) {
            if depth <= 0 {
                break;
            }
            depth += brace_delta(l);
            if depth > 0 {
                body.push(*l);
            }
        }
        classes.insert(m[1].to_string(), body.join("\n"));
    }
    classes
}

fn parse_nested_class(body: &str) -> Vec<FieldDef> {
    let mut fields = Vec::new();
    let mut comments: Vec<String> = Vec::new();
    for line in body.split('\n') {
        let t = line.trim();
        if t.starts_with("///") {
            comments.push(t.to_string());
            continue;
        }
        if t.is_empty() || t.starts_with("//") {
            comments.clear();
            continue;
        }
        if let Some(fd) = parse_field(t, &comments) {
            fields.push(fd);
        }
        comments.clear();
    }
    fields
}

/// Parses PKL module source into a [`Schema`].
pub fn parse_source(source: &str) -> Schema {
    let mut s = Schema {
        module_name: re_module()
            .captures(source)
            .map(|m| m[1].to_string())
            .unwrap_or_default(),
        fields: Vec::new(),
    };
    let classes = parse_classes(source);
    let mut comments: Vec<String> = Vec::new();
    let (mut in_class, mut depth) = (false, 0i64);
    for line in source.split('\n') {
        let t = line.trim();
        if re_class_decl().is_match(t) {
            in_class = true;
            depth = brace_delta(t);
            comments.clear();
            continue;
        }
        if in_class {
            depth += brace_delta(t);
            if depth <= 0 {
                in_class = false;
            }
            continue;
        }
        if t.starts_with("///") {
            comments.push(t.to_string());
            continue;
        }
        if t.is_empty()
            || t.starts_with("module ")
            || t.starts_with("import ")
            || t.starts_with("//")
        {
            comments.clear();
            continue;
        }
        let fd = parse_field(t, &comments);
        comments.clear();
        let Some(fd) = fd else { continue };
        if let Some(body) = type_name(&fd).and_then(|n| classes.get(n)) {
            for mut nested in parse_nested_class(body) {
                nested.path = format!("{}.{}", fd.path, nested.path);
                s.fields.push(nested);
            }
            continue;
        }
        s.fields.push(fd);
    }
    s
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn constraints() {
        let k = |cs: Vec<Constraint>| {
            cs.into_iter()
                .map(|c| (c.kind, c.value))
                .collect::<Vec<_>>()
        };
        assert_eq!(
            k(parse_constraints("length >= 2")),
            [(ConstraintKind::MinLen, ConstraintValue::Int(2))]
        );
        assert_eq!(
            k(parse_constraints("isBetween(1, 100)")),
            [(ConstraintKind::Between, ConstraintValue::Between(1, 100))]
        );
        assert_eq!(
            k(parse_constraints("length <= 9")),
            [(ConstraintKind::MaxLen, ConstraintValue::Int(9))]
        );
    }

    #[test]
    fn union_defaults_and_types() {
        assert_eq!(
            parse_union_type(r#""go"|"py""#),
            Some(vec!["go".to_string(), "py".to_string()])
        );
        assert_eq!(parse_union_type("String"), None);
        assert_eq!(
            parse_default(r#""hello""#, FieldType::String),
            (Value::from("hello"), false)
        );
        assert_eq!(parse_default("42", FieldType::Int), (Value::Int(42), false));
        assert_eq!(resolve_base_type("FooBar"), FieldType::String);
        let (g, w, _) = parse_annotations(&[
            r#"/// @wizard.group "database""#.to_string(),
            r#"/// @wizard.when lang == "go""#.to_string(),
        ]);
        assert_eq!(g, "database");
        assert!(w.unwrap().expression.contains("lang"));
    }
}
