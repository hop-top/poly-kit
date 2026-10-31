//! Ported from the Go router tests.

#![cfg(not(target_arch = "wasm32"))]

use std::collections::BTreeMap;
use std::sync::{Arc, Mutex};

use super::*;
use crate::llm::picker::{Classification, PromptClassifier};
use crate::llm::triton::Scorer;
use crate::llm::types::{Media, MediaKind, Message, Part, Request, Response, Role};
use crate::llm::{Capability, Provider};

struct Fixed(f64);
impl Router for Fixed {
    fn score<'a>(&'a self, _: &'a str) -> BoxFuture<'a, Result<f64, LlmError>> {
        Box::pin(async move { Ok(self.0) })
    }
}

struct Failing;
impl Router for Failing {
    fn score<'a>(&'a self, _: &'a str) -> BoxFuture<'a, Result<f64, LlmError>> {
        Box::pin(async { Err(LlmError::invalid("scorer unreachable")) })
    }
}

/// Records the text `score` saw.
struct Capturing(f64, Mutex<String>);
impl Router for Capturing {
    fn score<'a>(&'a self, t: &'a str) -> BoxFuture<'a, Result<f64, LlmError>> {
        Box::pin(async move {
            *self.1.lock().unwrap() = t.to_string();
            Ok(self.0)
        })
    }
}

/// Routes on modality: scores 1 for an image, else 0, and records the
/// signal.
struct Modal(Mutex<Option<UserSignal>>);
impl Router for Modal {
    fn score<'a>(&'a self, _: &'a str) -> BoxFuture<'a, Result<f64, LlmError>> {
        Box::pin(async { Ok(0.0) })
    }
    fn score_signal<'a>(&'a self, s: &'a UserSignal) -> BoxFuture<'a, Result<f64, LlmError>> {
        Box::pin(async move {
            *self.0.lock().unwrap() = Some(s.clone());
            Ok(if s.has_image { 1.0 } else { 0.0 })
        })
    }
}

fn pair() -> ModelPair {
    ModelPair {
        strong: "strong".into(),
        weak: "weak".into(),
    }
}

fn ctrl_with(name: &str, r: Arc<dyn Router>) -> Controller {
    let reg = Arc::new(Registry::new());
    reg.register(name, r).unwrap();
    Controller::new(reg, pair())
}

fn sig(text: &str) -> UserSignal {
    UserSignal {
        text: text.into(),
        ..UserSignal::default()
    }
}

#[test]
fn parse_model_string_table() {
    for (model, name, t) in [
        ("router-mf-0.116", "mf", 0.116),
        ("router-bert-0.7", "bert", 0.7),
        ("router-sw_ranking-0.5", "sw_ranking", 0.5),
        ("router-random-0", "random", 0.0),
        ("router-random-1", "random", 1.0),
        ("router-sw-ranking-0.5", "sw-ranking", 0.5),
    ] {
        let (n, th) = parse_model_string(model).unwrap_or_else(|e| panic!("{model}: {e}"));
        assert_eq!(n, name);
        assert!((th - t).abs() < 1e-4, "{model}");
    }
    for model in [
        "mf-0.5",
        "router-mf",
        "router-mf-abc",
        "router-mf-1.5",
        "router-mf-",
        "router--0.5",
        "",
    ] {
        let e = parse_model_string(model).unwrap_err();
        assert!(e.to_string().starts_with("routing: "), "{model}: {e}");
    }
}

#[test]
fn registry_semantics() {
    let reg = Registry::new();
    reg.register("a", Arc::new(Fixed(0.5))).unwrap();
    reg.register("b", Arc::new(Fixed(0.5))).unwrap();
    assert!(reg
        .register("a", Arc::new(Fixed(0.5)))
        .unwrap_err()
        .to_string()
        .contains("already registered"));
    assert_eq!(reg.names(), ["a", "b"]);
    assert!(reg.get("a").is_ok());
    let e = reg.get("zzz").err().unwrap().to_string();
    assert!(
        e.starts_with("routing: unknown router") && e.contains("available"),
        "{e}"
    );
}

#[tokio::test]
async fn route_picks_strong_at_or_above_the_threshold() {
    let c = ctrl_with("fixed", Arc::new(Fixed(0.6)));
    assert_eq!(c.route(&sig("x"), "fixed", 0.5).await.unwrap(), "strong");
    assert_eq!(c.route(&sig("x"), "fixed", 0.6).await.unwrap(), "strong");
    assert_eq!(c.route(&sig("x"), "fixed", 0.7).await.unwrap(), "weak");
    assert_eq!(
        c.route_from_model(&sig("x"), "router-fixed-0.4")
            .await
            .unwrap(),
        "strong"
    );
    assert_eq!(
        c.model_counts()["fixed"],
        BTreeMap::from([("strong".to_string(), 3), ("weak".to_string(), 1)])
    );
}

#[tokio::test]
async fn route_rejects_unknown_routers_bad_thresholds_and_scoring_failures() {
    let c = ctrl_with("fixed", Arc::new(Fixed(0.6)));
    assert!(c.route(&sig("x"), "nope", 0.5).await.is_err());
    for t in [-0.1, 1.5, f64::NAN] {
        let e = c
            .route(&sig("x"), "fixed", t)
            .await
            .unwrap_err()
            .to_string();
        assert!(e.contains("out of [0,1] range"), "{t}: {e}");
    }
    let c = ctrl_with("bad", Arc::new(Failing));
    let e = c
        .route(&sig("x"), "bad", 0.5)
        .await
        .unwrap_err()
        .to_string();
    assert!(e.contains("router \"bad\" scoring failed"), "{e}");
    assert!(c.model_counts().is_empty());
}

#[tokio::test]
async fn plain_routers_get_the_text_and_modal_ones_the_signal() {
    let cap = Arc::new(Capturing(0.6, Mutex::default()));
    let c = ctrl_with("plain", cap.clone());
    assert_eq!(
        c.route(&sig("explain quantum physics"), "plain", 0.5)
            .await
            .unwrap(),
        "strong"
    );
    assert_eq!(*cap.1.lock().unwrap(), "explain quantum physics");

    let modal = Arc::new(Modal(Mutex::default()));
    let c = ctrl_with("modal", modal.clone());
    let s = UserSignal {
        text: "look".into(),
        has_image: true,
        ..UserSignal::default()
    };
    assert_eq!(c.route(&s, "modal", 0.5).await.unwrap(), "strong");
    assert_eq!(modal.0.lock().unwrap().clone().unwrap(), s);
    assert_eq!(
        c.route(&sig("no image"), "modal", 0.5).await.unwrap(),
        "weak"
    );
}

struct Override(ModelPair);
impl Middleware for Override {
    fn model_pair<'a>(&'a self, _: &'a str) -> BoxFuture<'a, Result<Option<ModelPair>, LlmError>> {
        Box::pin(async move { Ok(Some(self.0.clone())) })
    }
}

struct Erroring;
impl Middleware for Erroring {
    fn model_pair<'a>(&'a self, _: &'a str) -> BoxFuture<'a, Result<Option<ModelPair>, LlmError>> {
        Box::pin(async { Err(LlmError::invalid("no")) })
    }
}

#[tokio::test]
async fn middleware_overrides_the_pair_and_errors_are_ignored() {
    let reg = Arc::new(Registry::new());
    reg.register("fixed", Arc::new(Fixed(0.9))).unwrap();
    let alt = ModelPair {
        strong: "code-strong".into(),
        weak: "code-weak".into(),
    };
    let c = Controller::builder(reg, pair())
        .middleware(Arc::new(Override(alt)))
        .middleware(Arc::new(Erroring))
        .build();
    assert_eq!(
        c.route(&sig("x"), "fixed", 0.5).await.unwrap(),
        "code-strong"
    );
}

struct Echo(Mutex<Vec<String>>);
impl Provider for Echo {
    fn capabilities(&self) -> &'static [Capability] {
        &[Capability::Complete]
    }
    fn complete<'a>(&'a self, req: &'a Request) -> BoxFuture<'a, Result<Response, LlmError>> {
        Box::pin(async move {
            self.0.lock().unwrap().push(req.model.clone());
            Ok(Response {
                content: format!("ran {}", req.model),
                ..Response::default()
            })
        })
    }
}

struct Nothing;
impl Provider for Nothing {
    fn capabilities(&self) -> &'static [Capability] {
        &[]
    }
}

#[tokio::test]
async fn complete_routes_then_delegates() {
    let req = Request {
        model: "router-fixed-0.5".into(),
        messages: vec![Message::user("hi")],
        ..Request::default()
    };
    let reg = Arc::new(Registry::new());
    reg.register("fixed", Arc::new(Fixed(0.9))).unwrap();
    // No provider.
    let c = Controller::new(reg.clone(), pair());
    assert_eq!(
        c.complete(&req).await.unwrap_err().to_string(),
        "routing: no provider configured"
    );
    // A provider without complete.
    let c = Controller::builder(reg.clone(), pair())
        .provider(Arc::new(Nothing))
        .build();
    assert_eq!(
        c.complete(&req).await.unwrap_err().to_string(),
        "routing: provider does not support Complete"
    );
    // Delegation with the selected model; a bad model string never reaches it.
    let echo = Arc::new(Echo(Mutex::default()));
    let c = Controller::builder(reg, pair())
        .provider(echo.clone())
        .build();
    assert_eq!(c.complete(&req).await.unwrap().content, "ran strong");
    let bad = Request {
        model: "gpt-4".into(),
        ..req
    };
    assert!(c.complete(&bad).await.is_err());
    assert_eq!(*echo.0.lock().unwrap(), ["strong"]);
}

#[test]
fn last_user_signal_cases() {
    let m = |role: Role, content: &str| Message {
        role,
        content: content.into(),
        ..Message::default()
    };
    let s = last_user_signal(&[
        m(Role::System, "you are helpful"),
        m(Role::User, "first"),
        m(Role::Assistant, "a"),
        m(Role::User, "second"),
    ]);
    assert_eq!(s, sig("second"));
    assert_eq!(
        last_user_signal(&[m(Role::System, "system prompt")]).text,
        "system prompt"
    );
    assert_eq!(last_user_signal(&[]), UserSignal::default());
    let media = |kind| {
        Part::Media(Media {
            kind,
            mime_type: String::new(),
            data: vec![],
        })
    };
    let parts = Message {
        role: Role::User,
        parts: vec![
            Part::Text("hello".into()),
            media(MediaKind::Image),
            Part::Text(" world".into()),
        ],
        ..Message::default()
    };
    assert_eq!(
        last_user_signal(&[parts]),
        UserSignal {
            text: "hello world".into(),
            has_image: true,
            ..UserSignal::default()
        }
    );
    let av = Message {
        role: Role::User,
        parts: vec![media(MediaKind::Audio), media(MediaKind::Video)],
        ..Message::default()
    };
    let s = last_user_signal(&[av]);
    assert!(s.has_audio && s.has_video && !s.has_image);
    let plain = Message {
        role: Role::User,
        content: "plain text".into(),
        ..Message::default()
    };
    assert_eq!(last_user_signal(&[plain]).text, "plain text");
}

#[tokio::test]
async fn random_router_is_in_range_and_seedable() {
    let (a, b) = (RandomRouter::with_seed(123), RandomRouter::with_seed(123));
    for _ in 0..50 {
        let (x, y) = (a.score("t").await.unwrap(), b.score("t").await.unwrap());
        assert!((0.0..1.0).contains(&x));
        assert_eq!(x, y);
    }
    assert!((0.0..=1.0).contains(&RandomRouter::new().score("t").await.unwrap()));
}

struct Stub(Result<Vec<f64>, ()>);
impl Embedder for Stub {
    fn embed<'a>(&'a self, _: &'a str) -> BoxFuture<'a, Result<Vec<f64>, LlmError>> {
        Box::pin(async move { self.0.clone().map_err(|()| LlmError::invalid("embed down")) })
    }
}

/// An embedder that looks the text up.
struct Table(BTreeMap<String, Vec<f64>>, Vec<f64>);
impl Embedder for Table {
    fn embed<'a>(&'a self, t: &'a str) -> BoxFuture<'a, Result<Vec<f64>, LlmError>> {
        Box::pin(async move { Ok(self.0.get(t).cloned().unwrap_or_else(|| self.1.clone())) })
    }
}

struct ScorerStub(Result<f64, ()>, Mutex<Vec<f32>>);
impl Scorer for ScorerStub {
    fn score<'a>(&'a self, input: &'a [f32]) -> BoxFuture<'a, Result<f64, LlmError>> {
        Box::pin(async move {
            *self.1.lock().unwrap() = input.to_vec();
            self.0.map_err(|()| LlmError::invalid("triton down"))
        })
    }
}

#[tokio::test]
async fn mf_router() {
    let emb: Arc<dyn Embedder> = Arc::new(Stub(Ok(vec![0.5, 0.25])));
    let sc = Arc::new(ScorerStub(Ok(0.75), Mutex::default()));
    let e = |r: Result<MfRouter, LlmError>| r.err().unwrap().to_string();
    assert!(e(MfRouter::new(MfConfig {
        embedder: None,
        scorer: Some(sc.clone()),
        strong_model: String::new(),
        weak_model: String::new()
    }))
    .contains("embedder is required"));
    assert!(e(MfRouter::new(MfConfig {
        embedder: Some(emb.clone()),
        scorer: None,
        strong_model: String::new(),
        weak_model: String::new()
    }))
    .contains("scorer is required"));
    let r = MfRouter::new(MfConfig {
        embedder: Some(emb),
        scorer: Some(sc.clone()),
        strong_model: String::new(),
        weak_model: String::new(),
    })
    .unwrap();
    assert_eq!(
        (r.strong_model(), r.weak_model()),
        ("gpt-4-1106-preview", "mixtral-8x7b-instruct-v0.1")
    );
    assert_eq!(r.score("p").await.unwrap(), 0.75);
    assert_eq!(*sc.1.lock().unwrap(), [0.5f32, 0.25]);
    let bad_emb = MfRouter::new(MfConfig {
        embedder: Some(Arc::new(Stub(Err(())))),
        scorer: Some(sc),
        strong_model: String::new(),
        weak_model: String::new(),
    })
    .unwrap();
    assert!(bad_emb
        .score("p")
        .await
        .unwrap_err()
        .to_string()
        .contains("mf: embed prompt"));
    let bad_sc = MfRouter::new(MfConfig {
        embedder: Some(Arc::new(Stub(Ok(vec![1.0])))),
        scorer: Some(Arc::new(ScorerStub(Err(()), Mutex::default()))),
        strong_model: String::new(),
        weak_model: String::new(),
    })
    .unwrap();
    assert!(bad_sc
        .score("p")
        .await
        .unwrap_err()
        .to_string()
        .contains("mf: scorer"));
}

struct Toks(Result<Vec<i32>, ()>);
impl Tokenizer for Toks {
    fn tokenize(&self, _: &str) -> Result<Vec<i32>, LlmError> {
        self.0.clone().map_err(|()| LlmError::invalid("no vocab"))
    }
}

#[tokio::test]
async fn bert_router() {
    let sc = Arc::new(ScorerStub(Ok(0.6), Mutex::default()));
    let cfg = |t: Option<Arc<dyn Tokenizer>>, s: Option<Arc<dyn Scorer>>, n| BertConfig {
        tokenizer: t,
        scorer: s,
        num_labels: n,
    };
    assert!(BertRouter::new(cfg(None, Some(sc.clone()), 0))
        .err()
        .unwrap()
        .to_string()
        .contains("tokenizer is required"));
    assert!(
        BertRouter::new(cfg(Some(Arc::new(Toks(Ok(vec![])))), None, 0))
            .err()
            .unwrap()
            .to_string()
            .contains("scorer is required")
    );
    let r = BertRouter::new(cfg(
        Some(Arc::new(Toks(Ok(vec![101, 7, 102])))),
        Some(sc.clone()),
        0,
    ))
    .unwrap();
    assert_eq!(r.num_labels(), 3);
    assert_eq!(r.score("p").await.unwrap(), 0.6);
    assert_eq!(*sc.1.lock().unwrap(), [101.0f32, 7.0, 102.0]);
    let r = BertRouter::new(cfg(Some(Arc::new(Toks(Err(())))), Some(sc), 5)).unwrap();
    assert_eq!(r.num_labels(), 5);
    assert!(r
        .score("p")
        .await
        .unwrap_err()
        .to_string()
        .contains("bert: tokenize"));
    let r = BertRouter::new(cfg(
        Some(Arc::new(Toks(Ok(vec![1])))),
        Some(Arc::new(ScorerStub(Err(()), Mutex::default()))),
        0,
    ))
    .unwrap();
    assert!(r
        .score("p")
        .await
        .unwrap_err()
        .to_string()
        .contains("bert: scorer"));
}

#[test]
fn softmax_and_logits() {
    let sm = softmax(&[2.0, 1.0, 0.1]);
    assert!((sm.iter().sum::<f64>() - 1.0).abs() < 1e-4);
    assert!(sm[0] > sm[1] && sm[1] > sm[2]);
    assert!(softmax(&[]).is_empty());
    let big = softmax(&[1000.0, 1001.0, 1002.0]);
    assert!(big.iter().all(|v| v.is_finite()) && (big.iter().sum::<f64>() - 1.0).abs() < 1e-4);
    assert!((score_from_logits(&[2.0, 1.0, 0.1]) - sm[0]).abs() < 1e-4);
    assert_eq!(score_from_logits(&[]), 0.5);
    assert!(score_from_logits(&[5.0]).abs() < 1e-4);
}

fn battles() -> (Vec<BattleRecord>, Vec<Vec<f64>>) {
    let b = |a: &str, c: &str, w: &str| BattleRecord {
        model_a: a.into(),
        model_b: c.into(),
        winner: w.into(),
    };
    (
        vec![
            b("strong", "weak", "model_a"),
            b("strong", "weak", "model_a"),
            b("weak", "strong", "model_b"),
        ],
        vec![
            vec![1.0, 0.0, 0.0],
            vec![0.9, 0.1, 0.0],
            vec![0.8, 0.2, 0.0],
        ],
    )
}

fn sw_cfg(embedder: Option<Arc<dyn Embedder>>) -> SwConfig {
    let (battles, battle_embeddings) = battles();
    SwConfig {
        embedder,
        strong_model: "strong".into(),
        weak_model: "weak".into(),
        num_tiers: 2,
        battles,
        battle_embeddings,
    }
}

#[tokio::test]
async fn sw_router() {
    let emb: Arc<dyn Embedder> = Arc::new(Stub(Ok(vec![1.0, 0.0, 0.0])));
    let e = |c: SwConfig| SwRankingRouter::new(c).err().unwrap().to_string();
    assert!(e(sw_cfg(None)).contains("embedder is required"));
    let mut c = sw_cfg(Some(emb.clone()));
    c.strong_model.clear();
    assert!(e(c).contains("model names required"));
    let mut c = sw_cfg(Some(emb.clone()));
    c.battles.clear();
    assert!(e(c).contains("battles data is required"));
    let mut c = sw_cfg(Some(emb.clone()));
    c.battle_embeddings.clear();
    assert!(e(c).contains("length mismatch"));
    let r = SwRankingRouter::new(sw_cfg(Some(emb))).unwrap();
    let s = r.score("test prompt").await.unwrap();
    assert!((0.0..=1.0).contains(&s));
    assert!(s > 0.5, "the strong model wins most battles: {s}");
    let bad = SwRankingRouter::new(sw_cfg(Some(Arc::new(Stub(Err(())))))).unwrap();
    assert!(bad
        .score("t")
        .await
        .unwrap_err()
        .to_string()
        .contains("embed prompt"));
}

#[test]
fn elo_tier_and_weight_helpers() {
    use super::routers::{compute_elo_mle, compute_tiers, compute_weightings, dot, vec_norm};
    assert!((dot(&[1.0, 2.0, 3.0], &[4.0, 5.0, 6.0]) - 32.0).abs() < 1e-3);
    assert!((vec_norm(&[3.0, 4.0]) - 5.0).abs() < 1e-3);
    let w = compute_weightings(&[0.5, 1.0, 0.2]);
    assert!((w[1] - 100.0).abs() < 1e-3);
    assert_eq!(compute_weightings(&[-1.0, 0.0]), [10.0, 10.0]);
    let b = |a: &str, c: &str, w: &str| BattleRecord {
        model_a: a.into(),
        model_b: c.into(),
        winner: w.into(),
    };
    let r = compute_elo_mle(
        &[
            b("a", "b", "model_a"),
            b("a", "b", "model_a"),
            b("b", "a", "model_b"),
        ],
        None,
    );
    assert!(r["a"] > r["b"]);
    let ratings = BTreeMap::from([
        ("a".to_string(), 1200.0),
        ("b".into(), 1100.0),
        ("c".into(), 1000.0),
        ("d".into(), 900.0),
    ]);
    let t = compute_tiers(&ratings, 2);
    assert_eq!(t.len(), 4);
    assert!(t["a"] < t["d"]);
    assert_eq!((t["a"], t["b"], t["c"], t["d"]), (0, 0, 1, 1));
    assert!(compute_tiers(&BTreeMap::new(), 5).is_empty());
    assert_eq!(
        compute_tiers(
            &BTreeMap::from([("a".to_string(), 1000.0), ("b".into(), 900.0)]),
            10
        )
        .len(),
        2
    );
}

#[test]
fn cosine() {
    assert!(cosine_similarity(&[1.0, 0.0], &[0.0, 1.0]).abs() < 1e-3);
    assert!((cosine_similarity(&[1.0, 1.0], &[1.0, 1.0]) - 1.0).abs() < 1e-3);
    assert_eq!(cosine_similarity(&[0.0, 0.0], &[1.0, 1.0]), 0.0);
}

fn table(entries: &[(&str, [f64; 3])], fallback: [f64; 3]) -> Arc<Table> {
    Arc::new(Table(
        entries
            .iter()
            .map(|(k, v)| (k.to_string(), v.to_vec()))
            .collect(),
        fallback.to_vec(),
    ))
}

async fn detector() -> Arc<IntentDetector> {
    let emb = table(
        &[
            ("write code", [1.0, 0.0, 0.0]),
            ("fix bug", [0.9, 0.1, 0.0]),
            ("write essay", [0.0, 1.0, 0.0]),
            ("write poem", [0.0, 0.9, 0.1]),
            ("code request", [0.95, 0.05, 0.0]),
        ],
        [0.5, 0.5, 0.0],
    );
    let det = Arc::new(IntentDetector::new(emb));
    det.add_examples("coding", &["write code".into(), "fix bug".into()])
        .await
        .unwrap();
    det.add_examples("creative", &["write essay".into(), "write poem".into()])
        .await
        .unwrap();
    det
}

#[tokio::test]
async fn intent_detection_and_confidence() {
    let none = IntentDetector::new(Arc::new(Stub(Ok(vec![1.0]))));
    assert_eq!(none.detect("t").await.unwrap(), "general");
    assert!(none.confidence("t").await.unwrap().is_empty());
    let det = detector().await;
    assert_eq!(det.detect("code request").await.unwrap(), "coding");
    let c = det.confidence("code request").await.unwrap();
    assert!((c.values().sum::<f64>() - 1.0).abs() < 1e-9);
    assert!(c["coding"] > c["creative"]);
    // Zero similarity everywhere: uniform.
    let flat = IntentDetector::new(Arc::new(Stub(Ok(vec![0.0, 0.0]))));
    flat.add_examples("a", &["x".into()]).await.unwrap();
    flat.add_examples("b", &["y".into()]).await.unwrap();
    let c = flat.confidence("t").await.unwrap();
    assert_eq!((c["a"], c["b"]), (0.5, 0.5));
    // An embedding failure while adding adds nothing.
    let bad = IntentDetector::new(Arc::new(Stub(Err(()))));
    assert!(bad
        .add_examples("a", &["x".into()])
        .await
        .unwrap_err()
        .to_string()
        .contains("embed example 0"));
    assert_eq!(bad.detect("t").await.unwrap(), "general");
}

#[tokio::test]
async fn intent_selector_middleware() {
    let det = detector().await;
    let default = pair();
    let code = ModelPair {
        strong: "code-strong".into(),
        weak: "code-weak".into(),
    };
    let sel = IntentModelSelector::new(
        det,
        default.clone(),
        vec![IntentMapping {
            intent: "coding".into(),
            description: String::new(),
            pair: code.clone(),
        }],
    );
    assert_eq!(sel.model_pair("code request").await.unwrap(), Some(code));
    assert_eq!(
        sel.model_pair("write essay").await.unwrap(),
        Some(default.clone()),
        "unmapped intent"
    );
    let broken = IntentModelSelector::new(
        Arc::new(IntentDetector::new(Arc::new(Stub(Err(()))))),
        default.clone(),
        vec![],
    );
    assert_eq!(broken.model_pair("x").await.unwrap(), Some(default));
}

#[tokio::test]
async fn score_classifier() {
    let score = |s: f64, c: fn(&mut ScoreClassifier)| {
        let mut sc = ScoreClassifier::new(Arc::new(Fixed(s)));
        c(&mut sc);
        async move { sc.classify("p").await.unwrap() }
    };
    let v = score(0.9, |_| {}).await;
    assert_eq!((v.label.as_str(), v.confidence), (LABEL_STRONG, 0.9));
    let v = score(0.2, |_| {}).await;
    assert_eq!(v.label, LABEL_WEAK);
    assert!((v.confidence - 0.8).abs() < 1e-9);
    assert_eq!(
        score(0.5, |_| {}).await.label,
        LABEL_STRONG,
        "at the default threshold"
    );
    let v = score(0.6, |c| c.threshold = 0.7).await;
    assert_eq!(v.label, LABEL_WEAK);
    assert!((v.confidence - 0.4).abs() < 1e-9);
    let v = score(0.9, |c| {
        c.strong_label = "premium".into();
        c.weak_label = "cheap".into();
    })
    .await;
    assert_eq!(v.label, "premium");
    let e = ScoreClassifier::new(Arc::new(Failing))
        .classify("p")
        .await
        .unwrap_err()
        .to_string();
    assert!(e.contains("router: score prompt"), "{e}");
}

#[tokio::test]
async fn intent_classifier() {
    let c = IntentClassifier {
        detector: detector().await,
    };
    let v = c.classify("code request").await.unwrap();
    assert_eq!(v.label, "coding");
    assert!(v.confidence > 0.5);
    let none = IntentClassifier {
        detector: Arc::new(IntentDetector::new(Arc::new(Stub(Ok(vec![1.0]))))),
    };
    assert_eq!(none.classify("x").await.unwrap(), Classification::default());
    // Equal scores: the alphabetically first label.
    let tie = Arc::new(IntentDetector::new(Arc::new(Stub(Ok(vec![1.0, 0.0])))));
    tie.add_examples("zeta", &["a".into()]).await.unwrap();
    tie.add_examples("alpha", &["b".into()]).await.unwrap();
    assert_eq!(
        IntentClassifier { detector: tie }
            .classify("x")
            .await
            .unwrap()
            .label,
        "alpha"
    );
}
