use super::*;

fn cfg(base: &str, model: &str, key: &str) -> ResolvedConfig {
    let mut c = ResolvedConfig::default();
    c.provider.base_url = base.into();
    c.provider.model = model.into();
    c.provider.api_key = key.into();
    c
}

fn build(c: ResolvedConfig, env: &'static [(&str, &str)]) -> Result<Adapter, LlmError> {
    Adapter::builder(c)
        .env(move |k| {
            env.iter()
                .find(|(n, _)| *n == k)
                .map(|(_, v)| v.to_string())
        })
        .build()
}

#[test]
fn model_and_key_are_required() {
    let e = build(cfg("", "", "k"), &[]).unwrap_err().to_string();
    assert!(e.contains("model is required"), "{e}");
    let e = build(cfg("", "gemini-2.0-flash", ""), &[])
        .unwrap_err()
        .to_string();
    assert!(e.contains("API key is required"), "{e}");
    assert!(build(cfg("", "gemini-2.0-flash", "k"), &[]).is_ok());
}

#[test]
fn key_env_precedence_and_blanks() {
    let key = |c: ResolvedConfig, env| build(c, env).map(|a| a.key);
    assert_eq!(
        key(cfg("", "m", ""), &[("GEMINI_API_KEY", "fake-gemini")]).unwrap(),
        "fake-gemini"
    );
    assert_eq!(
        key(
            cfg("", "m", ""),
            &[
                ("GEMINI_API_KEY", "fake-gemini"),
                ("GOOGLE_API_KEY", "fake-google")
            ]
        )
        .unwrap(),
        "fake-google"
    );
    assert_eq!(
        key(cfg("", "m", ""), &[("LLM_API_KEY", "fake-universal")]).unwrap(),
        "fake-universal"
    );
    assert_eq!(
        key(cfg("", "m", "  "), &[("GOOGLE_API_KEY", "fake-google")]).unwrap(),
        "fake-google"
    );
    assert_eq!(
        key(
            cfg("", "m", ""),
            &[
                ("GOOGLE_API_KEY", " \t "),
                ("GEMINI_API_KEY", "fake-gemini")
            ]
        )
        .unwrap(),
        "fake-gemini"
    );
    assert!(key(cfg("", "m", " "), &[("GOOGLE_API_KEY", "  ")]).is_err());
    // Hermetic reads nothing ambient.
    let mut c = cfg("", "m", "");
    c.hermetic = true;
    assert!(key(c, &[("GOOGLE_API_KEY", "fake-google")]).is_err());
}

#[test]
fn urls() {
    let a = build(cfg("", "gemini-2.0-flash", "k"), &[]).unwrap();
    assert_eq!(
        a.url("gemini-2.0-flash", "generateContent")
            .unwrap()
            .as_str(),
        "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.0-flash:generateContent"
    );
    let a = build(cfg("http://h:1/v1beta/", "m", "k"), &[]).unwrap();
    assert_eq!(
        a.url("m", "streamGenerateContent?alt=sse")
            .unwrap()
            .as_str(),
        "http://h:1/v1beta/models/m:streamGenerateContent?alt=sse"
    );
}

#[test]
fn new_call_ids_look_like_gos() {
    let mode = ToolNameMode::from_params(&Default::default()).unwrap();
    let names = ToolNames::new(GEMINI_TOOL_NAMES, mode, &[]).unwrap();
    let id = wire::call_of(&serde_json::json!({"functionCall": {"name": "f"}}), &names).id;
    assert!(id.starts_with("call_") && id.len() == 31, "{id}");
}
