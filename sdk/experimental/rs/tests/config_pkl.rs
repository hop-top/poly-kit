//! Ports go/core/config/pkl tests: parse, schema, validate, completion,
//! eval. Wizard and command tests are not ported (not yet in kit-rs).
#![cfg(feature = "config-pkl")]

use std::path::{Path, PathBuf};

use hop_top_kit::config::pkl::{
    completion_keys, completion_values, load_schema, parse_source, resolve, validate_value,
    Constraint, ConstraintKind, ConstraintValue, FieldDef, FieldType, Schema,
};
use hop_top_kit::config::{Map, SchemaError, Value};

fn go_testdata(name: &str) -> PathBuf {
    Path::new(env!("CARGO_MANIFEST_DIR"))
        .join("../../../go/core/config/pkl/testdata")
        .join(name)
}

fn field(src: &str) -> FieldDef {
    let s = parse_source(src);
    assert_eq!(s.fields.len(), 1, "{src}");
    s.fields.into_iter().next().unwrap()
}

#[test]
fn parse_field_cases() {
    let f = field("name: String");
    assert_eq!(f.field_type, FieldType::String);
    assert!(f.required);
    assert_eq!(
        field(r#"name: String = "foo""#).default,
        Some(Value::from("foo"))
    );
    let f = field(r#"lang: "go"|"py""#);
    assert_eq!(f.field_type, FieldType::StringEnum);
    assert_eq!(f.enum_values, ["go", "py"]);
    assert_eq!(f.default, Some(Value::from("go")));
    let f = field("port: Int = 5432");
    assert_eq!(
        (f.field_type, f.default),
        (FieldType::Int, Some(Value::Int(5432)))
    );
    let f = field("enabled: Boolean = true");
    assert_eq!(
        (f.field_type, f.default),
        (FieldType::Bool, Some(Value::Bool(true)))
    );
    assert!(!field("db: String?").required);
    let f = field("port: Int(isBetween(1024, 65535))");
    assert_eq!(f.field_type, FieldType::Int);
    assert_eq!(
        f.constraints,
        [Constraint {
            kind: ConstraintKind::Between,
            value: ConstraintValue::Between(1024, 65535)
        }]
    );
    let f = field(r##"db_name: String = "#{name}_db""##);
    assert!(f.computed);
    assert_eq!(f.default, Some(Value::from("#{name}_db")));
    let f = field("tags: Listing<String>?");
    assert_eq!(f.field_type, FieldType::StringList);
    assert!(!f.required);
}

#[test]
fn parse_constraints_and_annotations() {
    // Go's default-splitting regex cuts at the first `=`, so `length >= 2`
    // inside a field type is mangled in both ports; the constraint
    // parser itself is covered by the unit tests in the parse module.
    let c = |src: &str| field(&format!("x: String({src})")).constraints;
    let p = c(r#"matches(Regex("^[a-z]+$"))"#);
    assert_eq!(p[0].kind, ConstraintKind::Pattern);
    assert!(matches!(&p[0].value, ConstraintValue::Str(s) if s.contains("^[a-z]+$")));
    assert_eq!(
        c("length.isBetween(3, 50)"),
        [
            Constraint {
                kind: ConstraintKind::MinLen,
                value: ConstraintValue::Int(3)
            },
            Constraint {
                kind: ConstraintKind::MaxLen,
                value: ConstraintValue::Int(50)
            }
        ]
    );
    let f = field("/// @wizard.group \"database\"\n/// @wizard.when lang != \"go\"\n/// The host\nhost: String");
    assert_eq!(f.group, "database");
    let w = f.when.unwrap();
    assert_eq!(w.expression, r#"lang != "go""#);
    // Go's identifier regex also picks up words inside string literals.
    assert_eq!(w.depends_on, ["lang", "go"]);
    assert_eq!(f.description, "The host");
    assert_eq!(field("x: FooBar").field_type, FieldType::String);
    assert_eq!(field("x: Duration").field_type, FieldType::Duration);
    assert_eq!(field("x: Float = 0.5").default, Some(Value::Float(0.5)));
}

#[test]
fn load_schema_full_module_and_nested_class() {
    let d = tempfile::tempdir().unwrap();
    let p = d.path().join("config.pkl");
    std::fs::write(
        &p,
        "module MyConfig\n\nname: String\nlang: \"go\"|\"python\"|\"typescript\"\nport: Int = 5432\nenabled: Boolean = true\n",
    )
    .unwrap();
    let s = load_schema(&p).unwrap();
    assert_eq!(s.module_name, "MyConfig");
    assert_eq!(s.fields.len(), 4);
    assert_eq!(
        (s.fields[0].path.as_str(), s.fields[0].field_type),
        ("name", FieldType::String)
    );
    assert_eq!(s.fields[1].enum_values.len(), 3);

    let s = parse_source("module App\n\nclass Database {\n  host: String = \"localhost\"\n  port: Int = 5432\n}\n\ndb: Database\n");
    let paths: Vec<&str> = s.fields.iter().map(|f| f.path.as_str()).collect();
    assert_eq!(paths, ["db.host", "db.port"]);

    let s = load_schema(go_testdata("basic.pkl")).unwrap();
    let keys: Vec<String> = completion_keys(&s).into_iter().map(|i| i.value).collect();
    assert_eq!(keys, ["name", "port", "debug"]);
    assert!(validate_value(&s, "port", "abc").is_err());
}

fn test_schema() -> Schema {
    let f = |path: &str, t: FieldType| FieldDef {
        path: path.into(),
        field_type: t,
        ..FieldDef::default()
    };
    Schema {
        module_name: "TestConfig".into(),
        fields: vec![
            FieldDef {
                required: true,
                constraints: vec![Constraint {
                    kind: ConstraintKind::MinLen,
                    value: ConstraintValue::Int(2),
                }],
                ..f("name", FieldType::String)
            },
            FieldDef {
                enum_values: vec!["go".into(), "py".into(), "ts".into()],
                ..f("lang", FieldType::StringEnum)
            },
            FieldDef {
                constraints: vec![Constraint {
                    kind: ConstraintKind::Between,
                    value: ConstraintValue::Between(1024, 65535),
                }],
                ..f("port", FieldType::Int)
            },
            f("debug", FieldType::Bool),
            f("timeout", FieldType::Duration),
            f("ratio", FieldType::Float),
        ],
    }
}

#[test]
fn validate_value_cases() {
    let s = test_schema();
    for (k, v) in [
        ("name", "myapp"),
        ("lang", "go"),
        ("port", "8080"),
        ("debug", "true"),
        ("debug", "T"),
        ("ratio", "0.5"),
        ("timeout", "30s"),
    ] {
        assert_eq!(validate_value(&s, k, v), Ok(()), "{k}={v}");
    }
    let err = |k: &str, v: &str| validate_value(&s, k, v).unwrap_err().to_string();
    assert_eq!(
        err("name", "x"),
        r#"key "name" violates constraint minLen(2): x"#
    );
    assert_eq!(
        err("lang", "rust"),
        r#"key "lang" violates constraint enum([go py ts]): rust"#
    );
    assert_eq!(
        err("port", "abc"),
        r#"key "port" expects integer, got "abc""#
    );
    assert_eq!(
        err("port", "80"),
        r#"key "port" violates constraint between(1024, 65535): 80"#
    );
    assert_eq!(
        err("debug", "maybe"),
        r#"key "debug" expects boolean, got "maybe""#
    );
    assert_eq!(
        err("ratio", "abc"),
        r#"key "ratio" expects float, got "abc""#
    );
    match validate_value(&s, "unknown", "val").unwrap_err() {
        SchemaError::UnknownKey { key, valid_keys } => {
            assert_eq!(key, "unknown");
            assert_eq!(valid_keys.len(), 6);
        }
        e => panic!("{e}"),
    }
}

fn completion_schema() -> Schema {
    let f = |path: &str, t: FieldType, desc: &str| FieldDef {
        path: path.into(),
        field_type: t,
        description: desc.into(),
        ..FieldDef::default()
    };
    Schema {
        module_name: "TestConfig".into(),
        fields: vec![
            f("name", FieldType::String, "application name"),
            FieldDef {
                enum_values: vec!["go".into(), "py".into(), "ts".into()],
                ..f("lang", FieldType::StringEnum, "primary language")
            },
            f("debug", FieldType::Bool, ""),
            FieldDef {
                computed: true,
                ..f("derived", FieldType::String, "")
            },
        ],
    }
}

#[test]
fn completion_cases() {
    let s = completion_schema();
    let items = completion_keys(&s);
    let keys: Vec<&str> = items.iter().map(|i| i.value.as_str()).collect();
    assert_eq!(keys, ["name", "lang", "debug"]);
    assert_eq!(items[0].description, "application name");
    assert_eq!(items[2].description, "boolean");
    let vals: Vec<String> = completion_values(&s, "lang")
        .unwrap()
        .into_iter()
        .map(|i| i.value)
        .collect();
    assert_eq!(vals, ["go", "py", "ts"]);
    let vals: Vec<String> = completion_values(&s, "debug")
        .unwrap()
        .into_iter()
        .map(|i| i.value)
        .collect();
    assert_eq!(vals, ["true", "false"]);
    assert_eq!(completion_values(&s, "name"), None);
    assert_eq!(completion_values(&s, "nope"), None);
}

fn pkl_available() -> bool {
    std::process::Command::new("pkl")
        .arg("--version")
        .output()
        .is_ok_and(|o| o.status.success())
}

fn module_copy() -> (tempfile::TempDir, PathBuf) {
    let d = tempfile::tempdir().unwrap();
    let p = d.path().join("basic.pkl");
    std::fs::copy(go_testdata("basic.pkl"), &p).unwrap();
    (d, p)
}

#[test]
fn resolve_basic_and_defaults() {
    if !pkl_available() {
        eprintln!("skip: pkl binary not found");
        return;
    }
    let (d, p) = module_copy();
    let mut answers = Map::new();
    answers.insert("name".into(), "myapp".into());
    answers.insert("port".into(), Value::Int(9090));
    let got = resolve(&p, &answers).unwrap();
    assert_eq!(got["name"], Value::from("myapp"));
    assert_eq!(got["port"], Value::Int(9090));
    assert_eq!(got["debug"], Value::Bool(false));
    let got = resolve(&p, &Map::new()).unwrap();
    assert_eq!(got["name"], Value::from("default-app"));
    // The amendment temp file is gone.
    assert_eq!(std::fs::read_dir(d.path()).unwrap().count(), 1);
}

#[test]
fn resolve_unsupported_answer() {
    let (_d, p) = module_copy();
    let mut answers = Map::new();
    answers.insert("bad".into(), Value::Map(Map::new()));
    let err = resolve(&p, &answers).unwrap_err();
    assert_eq!(
        err.to_string(),
        r#"pkl: generate amendment: unsupported value type for "bad""#
    );
}
