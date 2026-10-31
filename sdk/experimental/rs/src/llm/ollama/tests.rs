use super::*;

fn cfg(base: &str, model: &str) -> ResolvedConfig {
    let mut c = ResolvedConfig::default();
    c.provider.base_url = base.into();
    c.provider.model = model.into();
    c
}

#[test]
fn default_base_url() {
    let a = Adapter::new(cfg("", "llama3")).unwrap();
    assert_eq!(a.endpoint(), "http://localhost:11434/api/chat");
}

#[test]
fn model_is_required() {
    assert_eq!(
        Adapter::new(cfg("", "")).unwrap_err().to_string(),
        "ollama: model is required"
    );
}

#[test]
fn custom_base_url_is_trimmed() {
    let a = Adapter::new(cfg("http://gpu-box:9999/", "llama3")).unwrap();
    assert_eq!(a.endpoint(), "http://gpu-box:9999/api/chat");
}

#[test]
fn host_form_stays_at_the_root() {
    let seen = std::sync::Arc::new(std::sync::Mutex::new(String::new()));
    let r = Registry::new();
    let s = seen.clone();
    r.register(
        SCHEME,
        Arc::new(move |cfg| {
            let a = Adapter::new(cfg)?;
            *s.lock().unwrap() = a.endpoint();
            Ok(Box::new(a) as Box<dyn Provider>)
        }),
        Declaration::default(),
    )
    .unwrap();
    r.resolve("ollama://gpu-box:11434/llama3").unwrap();
    assert_eq!(*seen.lock().unwrap(), "http://gpu-box:11434/api/chat");
}
