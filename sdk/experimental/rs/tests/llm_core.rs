//! The provider-neutral llm core: key resolution and config precedence
//! (Go's keysource, blankkey and loadconfig tests), redaction of keys
//! in Debug and errors, the registry, and the client's fallback chain
//! and bus events.

#![cfg(feature = "llm")]

use std::collections::BTreeMap;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};

use hop_top_kit::bus::DynBus;
use hop_top_kit::llm::{
    BoxFuture, Capability, Client, ConfigFile, Declaration, KeySourceKind, LlmError, Provider,
    ProviderKey, Registry, Request, ResolvedConfig, Response, SecretStore, Sources, TokenStream,
    Topics, FALLBACK_ENV_KEY,
};

const SECRET: &str = "fake-secret-must-not-leak";

struct Stub;
impl Provider for Stub {
    fn capabilities(&self) -> &'static [Capability] {
        &[]
    }
}

fn stub_factory() -> hop_top_kit::llm::Factory {
    Arc::new(|_cfg| Ok(Box::new(Stub) as Box<dyn Provider>))
}

/// A registry serving `openrouter` (convention key), `google` (two
/// declared names), `ollama` (optional key) and `lmstudio` (no key).
fn registry() -> Registry {
    let r = Registry::new();
    r.register("openrouter", stub_factory(), Declaration::default())
        .unwrap();
    r.register(
        "google",
        stub_factory(),
        Declaration {
            key: Some(ProviderKey {
                env_vars: vec!["GOOGLE_API_KEY".into(), "GEMINI_API_KEY".into()],
                optional: false,
            }),
            ..Declaration::default()
        },
    )
    .unwrap();
    r.register(
        "ollama",
        stub_factory(),
        Declaration {
            key: Some(ProviderKey {
                env_vars: vec!["OLLAMA_API_KEY".into()],
                optional: true,
            }),
            ..Declaration::default()
        },
    )
    .unwrap();
    r.register(
        "lmstudio",
        stub_factory(),
        Declaration {
            key: Some(ProviderKey {
                env_vars: vec![],
                optional: true,
            }),
            ..Declaration::default()
        },
    )
    .unwrap();
    r
}

struct MemStore(BTreeMap<String, String>, Option<String>);
impl SecretStore for MemStore {
    fn get(&self, name: &str) -> Result<Option<String>, String> {
        if let Some(fail) = &self.1 {
            return Err(fail.clone());
        }
        Ok(self.0.get(name).cloned())
    }
}

fn env_of(pairs: &[(&str, &str)]) -> impl Fn(&str) -> Option<String> + Sync {
    let m: BTreeMap<String, String> = pairs
        .iter()
        .map(|(k, v)| (k.to_string(), v.to_string()))
        .collect();
    move |k| m.get(k).cloned()
}

#[test]
fn resolve_api_key_sources() {
    struct Case {
        name: &'static str,
        yaml: &'static str,
        env: Vec<(&'static str, &'static str)>,
        stored: Vec<(&'static str, &'static str)>,
        uri: &'static str,
        value: &'static str,
        kind: KeySourceKind,
        source_name: &'static str,
    }
    let cases = vec![
        Case {
            name: "URI param",
            yaml: "providers:\n  openrouter:\n    api_key: fake-file\n",
            env: vec![("OPENROUTER_API_KEY", "fake-env")],
            stored: vec![],
            uri: "openrouter://m?api_key=fake-uri",
            value: "fake-uri",
            kind: KeySourceKind::Uri,
            source_name: "",
        },
        Case {
            name: "llm.yaml api_key",
            yaml: "providers:\n  openrouter:\n    api_key: fake-file\n",
            env: vec![("OPENROUTER_API_KEY", "fake-env")],
            stored: vec![],
            uri: "openrouter://m",
            value: "fake-file",
            kind: KeySourceKind::Config,
            source_name: "providers.openrouter.api_key",
        },
        Case {
            name: "api_key_env variable",
            yaml: "providers:\n  openrouter:\n    api_key_env: MY_OR_KEY\n",
            env: vec![
                ("MY_OR_KEY", "fake-mine"),
                ("OPENROUTER_API_KEY", "fake-env"),
            ],
            stored: vec![],
            uri: "openrouter://m",
            value: "fake-mine",
            kind: KeySourceKind::Env,
            source_name: "MY_OR_KEY",
        },
        Case {
            name: "secret store",
            yaml: "",
            env: vec![("OPENROUTER_API_KEY", "fake-env")],
            stored: vec![("OPENROUTER_API_KEY", "fake-store")],
            uri: "openrouter://m",
            value: "fake-store",
            kind: KeySourceKind::Store,
            source_name: "OPENROUTER_API_KEY",
        },
        Case {
            name: "provider variable, second name",
            yaml: "",
            env: vec![("GEMINI_API_KEY", "fake-gemini")],
            stored: vec![],
            uri: "google://m",
            value: "fake-gemini",
            kind: KeySourceKind::Env,
            source_name: "GEMINI_API_KEY",
        },
        Case {
            name: "LLM_API_KEY",
            yaml: "",
            env: vec![(FALLBACK_ENV_KEY, "fake-universal")],
            stored: vec![],
            uri: "openrouter://m",
            value: "fake-universal",
            kind: KeySourceKind::Fallback,
            source_name: FALLBACK_ENV_KEY,
        },
        Case {
            name: "blank sources fall through",
            yaml: "providers:\n  openrouter:\n    api_key: \"  \"\n",
            env: vec![
                ("OPENROUTER_API_KEY", " "),
                (FALLBACK_ENV_KEY, "fake-universal"),
            ],
            stored: vec![("OPENROUTER_API_KEY", "")],
            uri: "openrouter://m?api_key=%20",
            value: "fake-universal",
            kind: KeySourceKind::Fallback,
            source_name: FALLBACK_ENV_KEY,
        },
        Case {
            name: "local runtime without its key",
            yaml: "",
            env: vec![(FALLBACK_ENV_KEY, "fake-universal")],
            stored: vec![],
            uri: "ollama://llama3",
            value: "",
            kind: KeySourceKind::None,
            source_name: "",
        },
        Case {
            name: "scheme taking no key",
            yaml: "",
            env: vec![(FALLBACK_ENV_KEY, "fake-universal")],
            stored: vec![],
            uri: "lmstudio://m",
            value: "",
            kind: KeySourceKind::None,
            source_name: "",
        },
    ];
    let r = registry();
    for c in cases {
        let file = ConfigFile::parse(c.yaml).unwrap();
        let env = env_of(&c.env);
        let store = MemStore(
            c.stored
                .iter()
                .map(|(k, v)| (k.to_string(), v.to_string()))
                .collect(),
            None,
        );
        let src = Sources {
            file: &file,
            env: &env,
            store: Some(&store),
        };
        // "%20" is not unescaped (ParseURI does not), so make the URI
        // param literally blank for the blank case.
        let uri = c.uri.replace("%20", " ");
        let res = r.resolve_api_key(&src, &uri).unwrap();
        assert_eq!(res.value(), c.value, "{}", c.name);
        assert_eq!(res.source.kind, c.kind, "{}", c.name);
        assert_eq!(res.source.name, c.source_name, "{}", c.name);
        assert!(res.known, "{}", c.name);

        let applied = r.apply_api_key(&src, &uri).unwrap();
        match c.kind {
            KeySourceKind::None | KeySourceKind::Uri => assert_eq!(applied, uri, "{}", c.name),
            _ if uri.contains("api_key=") => {
                assert_eq!(
                    applied,
                    format!("{}?api_key={}", c.uri.split('?').next().unwrap(), c.value),
                    "{}",
                    c.name
                )
            }
            _ => assert_eq!(applied, format!("{uri}?api_key={}", c.value), "{}", c.name),
        }
    }
}

#[test]
fn missing_key_names_every_variable_and_no_value() {
    let r = registry();
    let file = ConfigFile::default();
    let env = env_of(&[]);
    let src = Sources {
        file: &file,
        env: &env,
        store: None,
    };
    let err = r.resolve_api_key(&src, "google://gemini").unwrap_err();
    let LlmError::MissingKey(m) = &err else {
        panic!("want MissingKey, got {err:?}");
    };
    assert_eq!(
        m.env_vars,
        ["GOOGLE_API_KEY", "GEMINI_API_KEY", FALLBACK_ENV_KEY]
    );
    assert_eq!(
        err.to_string(),
        "llm: no API key for provider \"google\" (model \"gemini\"): set GOOGLE_API_KEY or GEMINI_API_KEY or LLM_API_KEY"
    );
    assert!(r.apply_api_key(&src, "google://gemini").is_err());
}

#[test]
fn store_failure_does_not_stop_resolution() {
    let r = registry();
    let file = ConfigFile::default();
    let env = env_of(&[("OPENROUTER_API_KEY", "fake-env")]);
    let store = MemStore(BTreeMap::new(), Some("keychain locked".into()));
    let src = Sources {
        file: &file,
        env: &env,
        store: Some(&store),
    };
    let res = r.resolve_api_key(&src, "openrouter://m").unwrap();
    assert_eq!(res.value(), "fake-env");
    assert!(res
        .store_err
        .as_deref()
        .unwrap()
        .contains("keychain locked"));
}

#[test]
fn unknown_scheme_borrows_no_credential() {
    let r = registry();
    let file = ConfigFile::default();
    let env = env_of(&[(FALLBACK_ENV_KEY, "fake-universal")]);
    let src = Sources {
        file: &file,
        env: &env,
        store: None,
    };
    let res = r.resolve_api_key(&src, "nowhere://m").unwrap();
    assert!(!res.known);
    assert_eq!(res.value(), "");
}

#[test]
fn apply_api_key_rejects_separators_in_a_key() {
    let r = registry();
    let file = ConfigFile::default();
    let env = env_of(&[("OPENROUTER_API_KEY", "a&b")]);
    let src = Sources {
        file: &file,
        env: &env,
        store: None,
    };
    let err = r.apply_api_key(&src, "openrouter://m").unwrap_err();
    assert!(!err.to_string().contains("a&b"));
}

#[test]
fn bad_uri_errors_never_echo_a_key() {
    let r = registry();
    let file = ConfigFile::default();
    let env = env_of(&[]);
    let src = Sources {
        file: &file,
        env: &env,
        store: None,
    };
    let bad = format!("openai/gpt-4?api_key={SECRET}");
    for err in [
        r.resolve_api_key(&src, &bad).unwrap_err(),
        r.apply_api_key(&src, &bad).unwrap_err(),
        r.load_config(&src, &bad).unwrap_err(),
        r.resolve(&bad).err().unwrap(),
    ] {
        let msg = err.to_string();
        assert!(!msg.contains(SECRET), "{msg}");
    }
    let msg = r.resolve(&bad).err().unwrap().to_string();
    assert!(
        msg.contains("llm: invalid URI") && msg.contains("REDACTED"),
        "{msg}"
    );
}

#[test]
fn debug_never_prints_a_key() {
    let r = registry();
    let file = ConfigFile::parse(&format!(
        "providers:\n  openrouter:\n    api_key: {SECRET}\n"
    ))
    .unwrap();
    let env = env_of(&[]);
    let src = Sources {
        file: &file,
        env: &env,
        store: None,
    };
    let res = r.resolve_api_key(&src, "openrouter://m").unwrap();
    assert_eq!(res.value(), SECRET);
    assert!(!format!("{res:?}").contains(SECRET));
    assert!(!format!("{file:?}").contains(SECRET));

    let cfg = r
        .load_config(&src, &format!("openrouter://m?api_key={SECRET}"))
        .unwrap();
    assert_eq!(cfg.provider.api_key, SECRET);
    for shown in [
        format!("{cfg:?}"),
        format!("{:?}", cfg.provider),
        format!("{:?}", cfg.uri),
    ] {
        assert!(!shown.contains(SECRET), "{shown}");
    }
}

#[test]
fn load_config_base_url_precedence() {
    let r = registry();
    let file_yaml = "providers:\n  openrouter:\n    base_url: http://127.0.0.1:9/file\n";
    let cases = [
        (
            "param beats env and file",
            file_yaml,
            Some("http://127.0.0.1:9/env"),
            "openrouter://m?base_url=http://127.0.0.1:9/param",
            "http://127.0.0.1:9/param",
        ),
        (
            "param beats host",
            file_yaml,
            None,
            "openrouter://127.0.0.1:7/m?base_url=http://127.0.0.1:9/param",
            "http://127.0.0.1:9/param",
        ),
        (
            "host beats env and file",
            file_yaml,
            Some("http://127.0.0.1:9/env"),
            "openrouter://127.0.0.1:7/m",
            "http://127.0.0.1:7",
        ),
        (
            "env beats file",
            file_yaml,
            Some("http://127.0.0.1:9/env"),
            "openrouter://m",
            "http://127.0.0.1:9/env",
        ),
        (
            "file alone",
            file_yaml,
            None,
            "openrouter://m",
            "http://127.0.0.1:9/file",
        ),
        ("nothing", "", None, "openrouter://m", ""),
    ];
    for (name, yaml, env_base, uri, want) in cases {
        let file = ConfigFile::parse(yaml).unwrap();
        let pairs: Vec<(&str, &str)> = env_base.map(|b| ("LLM_BASE_URL", b)).into_iter().collect();
        let env = env_of(&pairs);
        let src = Sources {
            file: &file,
            env: &env,
            store: None,
        };
        let cfg = r.load_config(&src, uri).unwrap();
        assert_eq!(cfg.provider.base_url, want, "{name}");
    }
}

#[test]
fn load_config_default_uri_model_and_fallbacks() {
    let r = registry();
    let file = ConfigFile::parse(
        "default: openrouter://from-file\nfallback: [ollama://llama3]\nproviders:\n  openrouter:\n    model: file-model\n    temperature: 0.2\n",
    )
    .unwrap();
    let env = env_of(&[]);
    let src = Sources {
        file: &file,
        env: &env,
        store: None,
    };
    let cfg = r.load_config(&src, "").unwrap();
    assert_eq!(cfg.uri.scheme, "openrouter");
    assert_eq!(cfg.provider.model, "from-file");
    assert_eq!(cfg.fallbacks, ["ollama://llama3"]);
    assert!(cfg.provider.extras.contains_key("temperature"));

    let cfg = r.load_config(&src, "openrouter://").unwrap();
    assert_eq!(cfg.provider.model, "file-model");

    let env = env_of(&[
        ("LLM_PROVIDER", "google://env-model"),
        ("LLM_FALLBACK", " a://x , ,b://y"),
    ]);
    let src = Sources {
        file: &file,
        env: &env,
        store: None,
    };
    let cfg = r.load_config(&src, "").unwrap();
    assert_eq!(cfg.uri.scheme, "google");
    assert_eq!(cfg.fallbacks, ["a://x", "b://y"]);

    let empty = ConfigFile::default();
    let env = env_of(&[]);
    let src = Sources {
        file: &empty,
        env: &env,
        store: None,
    };
    assert_eq!(
        r.load_config(&src, "").unwrap_err().to_string(),
        "no URI provided and no default configured"
    );
}

#[test]
fn registry_resolves_key_and_base_url_from_the_uri() {
    let seen: Arc<Mutex<Option<ResolvedConfig>>> = Arc::default();
    let r = Registry::new();
    let s = seen.clone();
    r.register(
        "probe",
        Arc::new(move |cfg| {
            *s.lock().unwrap() = Some(cfg);
            Ok(Box::new(Stub) as Box<dyn Provider>)
        }),
        Declaration {
            base_url: "https://default.invalid".into(),
            ..Declaration::default()
        },
    )
    .unwrap();
    assert!(r
        .register("probe", stub_factory(), Declaration::default())
        .is_err());
    assert_eq!(r.schemes(), ["probe"]);

    r.resolve("probe://m?api_key=k1").unwrap();
    let cfg = seen.lock().unwrap().take().unwrap();
    assert_eq!(cfg.provider.api_key, "k1");
    assert_eq!(cfg.provider.base_url, "https://default.invalid");
    assert_eq!(cfg.provider.model, "m");

    r.resolve("probe://127.0.0.1:7/m?api_key=%20").unwrap();
    let cfg = seen.lock().unwrap().take().unwrap();
    assert_eq!(cfg.provider.base_url, "http://127.0.0.1:7");

    r.resolve("probe://m?api_key= ").unwrap();
    let cfg = seen.lock().unwrap().take().unwrap();
    assert_eq!(cfg.provider.api_key, "", "a blank key is no key");
    assert!(!cfg.provider.params.contains_key("api_key"));

    assert!(matches!(
        r.resolve("missing://m").err().unwrap(),
        LlmError::ProviderNotFound { .. }
    ));
}

#[test]
fn fallbackable_classification() {
    let yes = [
        LlmError::RateLimit {
            provider: "p".into(),
            retry_after: None,
        },
        LlmError::HttpStatus {
            status: 529,
            message: "overloaded".into(),
        },
        LlmError::HttpStatus {
            status: 500,
            message: "x".into(),
        },
        LlmError::Transport {
            message: "connection refused".into(),
        },
    ];
    let no = [
        LlmError::HttpStatus {
            status: 400,
            message: "x".into(),
        },
        LlmError::Auth {
            provider: "p".into(),
            message: "x".into(),
        },
        LlmError::PermissionDenied {
            provider: "p".into(),
            message: "x".into(),
        },
        LlmError::Model {
            model: "m".into(),
            provider: "p".into(),
        },
        LlmError::Cancelled,
        LlmError::Invalid("x".into()),
    ];
    for e in yes {
        assert!(e.is_fallbackable(), "{e:?}");
    }
    for e in no {
        assert!(!e.is_fallbackable(), "{e:?}");
    }
}

#[test]
fn permission_denied_is_an_auth_error() {
    let denied = LlmError::PermissionDenied {
        provider: "anthropic".into(),
        message: "permission_error: no".into(),
    };
    let auth = LlmError::Auth {
        provider: "anthropic".into(),
        message: "authentication_error: bad key".into(),
    };
    assert!(denied.is_auth() && auth.is_auth());
    assert!(!LlmError::Cancelled.is_auth());
    assert_eq!(
        denied.to_string(),
        "permission denied (provider \"anthropic\"): permission_error: no"
    );
}

/// A provider answering `complete` with a fixed outcome, counting calls.
struct Scripted {
    calls: Arc<AtomicUsize>,
    result: fn() -> Result<Response, LlmError>,
}

impl Provider for Scripted {
    fn capabilities(&self) -> &'static [Capability] {
        &[Capability::Complete]
    }
    fn complete<'a>(&'a self, _req: &'a Request) -> BoxFuture<'a, Result<Response, LlmError>> {
        self.calls.fetch_add(1, Ordering::SeqCst);
        let r = (self.result)();
        Box::pin(async move { r })
    }
}

fn scripted(result: fn() -> Result<Response, LlmError>) -> (Box<dyn Provider>, Arc<AtomicUsize>) {
    let calls = Arc::new(AtomicUsize::new(0));
    (
        Box::new(Scripted {
            calls: calls.clone(),
            result,
        }),
        calls,
    )
}

fn rate_limited() -> Result<Response, LlmError> {
    Err(LlmError::RateLimit {
        provider: "p".into(),
        retry_after: None,
    })
}

fn auth_failed() -> Result<Response, LlmError> {
    Err(LlmError::Auth {
        provider: "p".into(),
        message: "bad key".into(),
    })
}

fn answered() -> Result<Response, LlmError> {
    Ok(Response {
        content: "fallback answer".into(),
        ..Response::default()
    })
}

fn block_on<F: std::future::Future>(f: F) -> F::Output {
    tokio::runtime::Builder::new_current_thread()
        .build()
        .unwrap()
        .block_on(f)
}

#[test]
fn client_falls_back_on_fallbackable_errors_and_publishes() {
    let bus = DynBus::builder().build();
    let seen: Arc<Mutex<Vec<String>>> = Arc::default();
    let s = seen.clone();
    bus.subscribe("kit.ai.#", move |e| {
        s.lock().unwrap().push(e.topic.as_str().to_string());
        Ok(())
    });
    let (primary, pc) = scripted(rate_limited);
    let (fallback, fc) = scripted(answered);
    let client = Client::new(primary).with_fallback(fallback).with_bus(bus);
    let resp = block_on(client.complete(&Request::default())).unwrap();
    assert_eq!(resp.content, "fallback answer");
    assert_eq!(
        (pc.load(Ordering::SeqCst), fc.load(Ordering::SeqCst)),
        (1, 1)
    );
    assert_eq!(
        *seen.lock().unwrap(),
        [
            "kit.ai.request.started",
            "kit.ai.fallback.applied",
            "kit.ai.response.received"
        ]
    );
}

#[test]
fn client_stops_on_non_fallbackable_and_reports_exhaustion() {
    let (primary, _) = scripted(auth_failed);
    let (fallback, fc) = scripted(answered);
    let client = Client::new(primary).with_fallback(fallback);
    let err = block_on(client.complete(&Request::default())).unwrap_err();
    assert!(matches!(err, LlmError::Auth { .. }));
    assert_eq!(fc.load(Ordering::SeqCst), 0);

    let (a, _) = scripted(rate_limited);
    let (b, _) = scripted(rate_limited);
    let err = block_on(
        Client::new(a)
            .with_fallback(b)
            .complete(&Request::default()),
    )
    .unwrap_err();
    let LlmError::FallbackExhausted(errs) = &err else {
        panic!("{err:?}");
    };
    assert_eq!(errs.len(), 2);
    assert_eq!(err.to_string(), "all providers failed (2 errors)");
}

#[test]
fn client_reports_missing_capability() {
    let client = Client::new(Box::new(Stub)).with_fallback(Box::new(Stub));
    assert!(client.capabilities().is_empty());
    let err = block_on(client.complete(&Request::default())).unwrap_err();
    assert_eq!(
        err.to_string(),
        "capability \"complete\" not supported by provider \"provider[1]\""
    );
    let res: Result<Box<dyn TokenStream>, LlmError> = block_on(client.stream(&Request::default()));
    assert!(matches!(
        res.err().unwrap(),
        LlmError::CapabilityNotSupported { .. }
    ));
}

#[test]
fn topics_prefix_and_validation() {
    let t = Topics::with_prefix("myapp.ai").unwrap();
    assert_eq!(t.request_start, "myapp.ai.request.started");
    assert_eq!(t.eva_result, "myapp.ai.eva.evaluated");
    assert!(Topics::with_prefix("myapp").is_err());
    assert!(Topics::with_prefix("myapp.ai.").is_err());
    assert_eq!(Topics::default().request_end, "kit.ai.response.received");
}
