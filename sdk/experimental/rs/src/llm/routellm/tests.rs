use super::*;

fn extra(v: serde_json::Value) -> crate::config::Value {
    serde_json::from_value(v).unwrap()
}

fn router(name: &str, threshold: f64) -> ModelField {
    ModelField::Router {
        name: name.into(),
        threshold,
    }
}

fn tier(t: &str) -> ModelField {
    ModelField::Tier(t.into())
}

#[test]
fn parse_model_field_shapes() {
    let ok = [
        ("mf:0.7", router("mf", 0.7)),
        ("bert:0", router("bert", 0.0)),
        ("causal_llm:1", router("causal_llm", 1.0)),
        ("mf:0.123456", router("mf", 0.123456)),
        ("mf:5e-1", router("mf", 0.5)),
        ("a:b:0.5", router("a:b", 0.5)),
        ("private", tier("private")),
        ("coding_fast", tier("coding_fast")),
        ("router-mf-0.5", tier("router-mf-0.5")),
        ("mf:abc", tier("mf:abc")),
        ("a:b", tier("a:b")),
        (":0.5", tier(":0.5")),
        ("mf:", tier("mf:")),
        ("mf:NaN", tier("mf:NaN")),
        ("mf:Inf", tier("mf:Inf")),
    ];
    for (model, want) in ok {
        assert_eq!(parse_model_field(model).unwrap(), want, "{model}");
    }
    assert!(parse_model_field("").is_err());
}

fn cfg(model: &str) -> ResolvedConfig {
    let mut c = ResolvedConfig::default();
    c.uri.scheme = SCHEME.into();
    c.uri.model = model.into();
    c.provider.model = model.into();
    c
}

#[test]
fn thresholds_are_range_checked() {
    for m in ["mf:0", "mf:1", "mf:0.5"] {
        assert!(Adapter::new(cfg(m)).is_ok(), "{m}");
    }
    for m in ["mf:-0.1", "mf:1.5"] {
        let e = Adapter::new(cfg(m)).unwrap_err().to_string();
        assert!(e.contains("out of range [0, 1]"), "{e}");
    }
    let e = Adapter::new(cfg("")).unwrap_err().to_string();
    assert!(e.contains("model field is required"), "{e}");
}

#[test]
fn falls_back_to_the_uri_model() {
    let mut c = cfg("");
    c.uri.model = "bert:0.5".into();
    let a = Adapter::new(c).unwrap();
    assert_eq!(
        (a.router_name(), a.threshold(), a.is_tier()),
        ("bert", 0.5, false)
    );
}

#[test]
fn tier_accessors() {
    let a = Adapter::new(cfg("private")).unwrap();
    assert!(a.is_tier());
    assert_eq!(
        (a.tier(), a.router_name(), a.threshold()),
        ("private", "", 0.0)
    );
}

#[test]
fn threshold_text_is_shortest_without_exponent() {
    for (t, want) in [
        (0.7, "0.7"),
        (0.0, "0"),
        (1.0, "1"),
        (0.5, "0.5"),
        (0.123456, "0.123456"),
    ] {
        assert_eq!(format_threshold(t), want);
    }
}

#[test]
fn config_base_url_override_and_bad_extras() {
    let mut c = cfg("mf:0.5");
    c.provider.extras.insert(
        "routellm".into(),
        extra(serde_json::json!({"base_url": "http://custom:9090"})),
    );
    let a = Adapter::builder(c).env(|_| None).build().unwrap();
    assert_eq!(a.config().base_url, "http://custom:9090");
    assert_eq!(a.base_url(), "http://custom:9090/v1");
    let mut c = cfg("mf:0.5");
    c.provider
        .extras
        .insert("routellm".into(), extra(serde_json::json!("nope")));
    let e = Adapter::new(c).unwrap_err().to_string();
    assert!(e.contains("expected map"), "{e}");
}

#[test]
fn router_env_overrides() {
    let extras = std::collections::BTreeMap::new();
    let env = |n: &str| match n {
        "ROUTELLM_STRONG_MODEL" => Some("gpt-4".to_string()),
        "ROUTELLM_ROUTERS" => Some(" mf, bert ,, ".to_string()),
        _ => None,
    };
    let c = RouterConfig::parse(&extras, env).unwrap();
    assert_eq!((c.strong_model.as_str(), c.grpc_port), ("gpt-4", 6061));
    assert_eq!(c.routers, ["mf", "bert"]);
}

/// Every vector of `routellm_base_url`.
#[test]
fn base_url_vectors() {
    use serde::Deserialize;
    #[derive(Deserialize)]
    #[serde(deny_unknown_fields)]
    struct V {
        name: String,
        base_url: String,
        extras: std::collections::BTreeMap<String, crate::config::Value>,
        env: std::collections::BTreeMap<String, String>,
        hermetic: bool,
        want: String,
    }
    let path = std::path::PathBuf::from(env!("CARGO_MANIFEST_DIR"))
        .join("../../../contracts/llm-v1/vectors.json");
    let raw: serde_json::Value = serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap();
    assert_eq!(raw["version"], "v1");
    let vs: Vec<V> = serde_json::from_value(raw["routellm_base_url"].clone()).unwrap();
    assert!(!vs.is_empty());
    for v in vs {
        let mut c = cfg("mf:0.5");
        c.provider.base_url = v.base_url;
        c.provider.extras = v.extras;
        c.hermetic = v.hermetic;
        let env = v.env;
        let a = Adapter::builder(c)
            .env(move |k| env.get(k).cloned())
            .build()
            .unwrap();
        assert_eq!(a.base_url(), v.want, "{}", v.name);
    }
}
