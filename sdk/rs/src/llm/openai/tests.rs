//! Where requests go: scheme defaults and the `/v1` rules, checked on
//! the built adapter's endpoint, no network involved.

use std::sync::{Arc, Mutex};

use super::*;

/// The endpoint `uri` resolves to through a registry holding this
/// adapter's declarations.
fn endpoint_of(uri: &str) -> String {
    let seen = Arc::new(Mutex::new(String::new()));
    let r = Registry::new();
    for s in SCHEMES {
        let seen = seen.clone();
        r.register(
            s,
            Arc::new(move |cfg| {
                let a = Adapter::new(cfg)?;
                *seen.lock().unwrap() = a.endpoint();
                Ok(Box::new(a) as Box<dyn Provider>)
            }),
            declaration(s),
        )
        .unwrap();
    }
    r.resolve(uri).unwrap_or_else(|e| panic!("{uri}: {e}"));
    let got = seen.lock().unwrap().clone();
    got
}

#[test]
fn default_base_urls() {
    for (scheme, want) in [
        ("openrouter", "https://openrouter.ai/api/v1"),
        ("xai", "https://api.x.ai/v1"),
        ("groq", "https://api.groq.com/openai/v1"),
        ("together", "https://api.together.xyz/v1"),
        ("fireworks", "https://api.fireworks.ai/inference/v1"),
        ("deepseek", "https://api.deepseek.com"),
        ("mistral", "https://api.mistral.ai/v1"),
        ("lmstudio", "http://localhost:1234/v1"),
    ] {
        assert_eq!(default_base_url(scheme), Some(want), "{scheme}");
    }
    // openai takes the hardcoded fallback, not the table.
    assert_eq!(default_base_url("openai"), None);
}

#[test]
fn all_schemes_registered() {
    let r = Registry::new();
    register(&r).unwrap();
    let mut want: Vec<&str> = SCHEMES.to_vec();
    want.sort();
    assert_eq!(r.schemes(), want);
    assert_eq!(want.len(), 9);
}

#[test]
fn every_scheme_builds_without_a_base_url() {
    for scheme in SCHEMES {
        let mut cfg = ResolvedConfig::default();
        cfg.uri.scheme = scheme.to_string();
        cfg.uri.model = "test-model".into();
        cfg.provider.api_key = "test-key".into();
        let a = Adapter::new(cfg).unwrap_or_else(|e| panic!("{scheme}: {e}"));
        assert_eq!(a.scheme, *scheme);
        assert_eq!(a.model, "test-model");
    }
}

#[test]
fn lmstudio_request_path() {
    for (name, uri, want) in [
        (
            "bare",
            "lmstudio://local-model",
            "http://localhost:1234/v1/chat/completions",
        ),
        (
            "host form",
            "lmstudio://localhost:1234/local-model",
            "http://localhost:1234/v1/chat/completions",
        ),
        (
            "host form other host",
            "lmstudio://gpu-box:5678/local-model",
            "http://gpu-box:5678/v1/chat/completions",
        ),
        (
            "host form org model",
            "lmstudio://gpu-box:5678/org/local-model",
            "http://gpu-box:5678/v1/chat/completions",
        ),
        (
            "base_url with v1",
            "lmstudio://local-model?base_url=http://gpu-box:5678/v1",
            "http://gpu-box:5678/v1/chat/completions",
        ),
        (
            "base_url with v1 and slash",
            "lmstudio://local-model?base_url=http://gpu-box:5678/v1/",
            "http://gpu-box:5678/v1/chat/completions",
        ),
        (
            "base_url without v1",
            "lmstudio://local-model?base_url=http://gpu-box:5678",
            "http://gpu-box:5678/v1/chat/completions",
        ),
        (
            "base_url without v1 with slash",
            "lmstudio://local-model?base_url=http://gpu-box:5678/",
            "http://gpu-box:5678/v1/chat/completions",
        ),
        (
            "base_url proxy prefix",
            "lmstudio://local-model?base_url=https://proxy.example/lmstudio",
            "https://proxy.example/lmstudio/v1/chat/completions",
        ),
    ] {
        assert_eq!(endpoint_of(uri), want, "{name}");
    }
}

#[test]
fn openai_request_path() {
    for (name, uri, want) in [
        (
            "bare",
            "openai://gpt-4o",
            "https://api.openai.com/v1/chat/completions",
        ),
        (
            "host form",
            "openai://localhost:8000/my-model",
            "http://localhost:8000/v1/chat/completions",
        ),
        (
            "host form org model",
            "openai://gpu-box:8000/meta-llama/Llama-3.1-8B",
            "http://gpu-box:8000/v1/chat/completions",
        ),
        (
            "host form ipv6",
            "openai://[::1]:8000/my-model",
            "http://[::1]:8000/v1/chat/completions",
        ),
        (
            "base_url with v1",
            "openai://my-model?base_url=http://gpu-box:8000/v1",
            "http://gpu-box:8000/v1/chat/completions",
        ),
        (
            "base_url with v1 and slash",
            "openai://my-model?base_url=http://gpu-box:8000/v1/",
            "http://gpu-box:8000/v1/chat/completions",
        ),
        (
            "base_url without v1",
            "openai://my-model?base_url=http://gpu-box:8000",
            "http://gpu-box:8000/chat/completions",
        ),
        (
            "base_url proxy prefix",
            "openai://gpt-4o?base_url=https://gateway.example/acct/gw/openai",
            "https://gateway.example/acct/gw/openai/chat/completions",
        ),
        (
            "host form with base_url",
            "openai://gpu-box:8000/my-model?base_url=http://other:9000",
            "http://other:9000/chat/completions",
        ),
        (
            "host form with same base_url",
            "openai://gpu-box:8000/my-model?base_url=http://gpu-box:8000",
            "http://gpu-box:8000/chat/completions",
        ),
    ] {
        assert_eq!(endpoint_of(uri), want, "{name}");
    }
}

#[test]
fn other_schemes_keep_the_given_base_url() {
    for (uri, want) in [
        (
            "groq://gpu-box:5678/llama",
            "http://gpu-box:5678/chat/completions",
        ),
        (
            "openrouter://gpu-box:5678/meta/llama",
            "http://gpu-box:5678/chat/completions",
        ),
        (
            "deepseek://gpu-box:5678/deepseek-chat",
            "http://gpu-box:5678/chat/completions",
        ),
        (
            "openai://local-model?base_url=http://gpu-box:5678",
            "http://gpu-box:5678/chat/completions",
        ),
    ] {
        assert_eq!(endpoint_of(uri), want, "{uri}");
    }
}

#[test]
fn unparseable_base_url_is_masked() {
    const SECRET: &str = "fake-base-url-secret-must-not-leak";
    for base in [
        format!("http://{SECRET}@h\x7f/v1"),
        format!("http://user:{SECRET}@h\x7f/v1"),
        format!("http://h\x7f/v1?key={SECRET}"),
    ] {
        let mut cfg = ResolvedConfig::default();
        cfg.provider.base_url = base;
        cfg.provider.model = "gpt-4o".into();
        cfg.provider.api_key = "k".into();
        let e = Adapter::new(cfg).unwrap_err().to_string();
        assert!(e.contains("openai: invalid base URL"), "{e}");
        assert!(e.contains("REDACTED"), "{e}");
        assert!(!e.contains(SECRET), "{e}");
    }
}

#[test]
fn limit_params_must_be_positive_byte_counts() {
    for (param, value) in [
        ("max_body_bytes", "0"),
        ("max_body_bytes", "-1"),
        ("max_event_bytes", "lots"),
    ] {
        let mut cfg = ResolvedConfig::default();
        cfg.uri.scheme = "groq".into();
        cfg.provider.model = "m".into();
        cfg.provider.params.insert(param.into(), value.into());
        assert_eq!(
            Adapter::new(cfg).unwrap_err().to_string(),
            format!("groq: {param} must be a positive byte count"),
            "{value}"
        );
    }
}
