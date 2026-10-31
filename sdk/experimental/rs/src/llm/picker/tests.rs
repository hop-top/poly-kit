//! Ported from Go `picker_test.go`, `picker_pool_test.go`,
//! `pool_filter_test.go` and `picker_classifier_test.go`.

#![cfg(not(target_arch = "wasm32"))]

use std::collections::BTreeMap;
use std::sync::Mutex;

use super::*;
use crate::llm::provider::BoxFuture;

fn model(provider: &str, id: &str) -> Model {
    Model {
        id: id.into(),
        name: id.into(),
        provider: provider.into(),
        ..Model::default()
    }
}

fn priced(mut m: Model, input: f64, output: f64) -> Model {
    m.cost = Some(Cost {
        input,
        output,
        ..Cost::default()
    });
    m
}

fn ctx(mut m: Model, n: u64) -> Model {
    m.limit.context = n;
    m
}

fn out(mut m: Model, n: u64) -> Model {
    m.limit.output = n;
    m
}

fn tools(mut m: Model, b: bool) -> Model {
    m.tool_call = b;
    m
}

fn picker(models: Vec<Model>) -> Picker {
    Picker::new(Arc::new(StaticCatalog::new(models)))
}

fn ids(m: &Model) -> (&str, &str) {
    (&m.provider, &m.id)
}

#[tokio::test]
async fn capability_filter() {
    let p = picker(vec![
        tools(model("p1", "no-tools-a"), false),
        tools(model("p1", "yes-tools"), true),
        tools(model("p2", "no-tools-b"), false),
    ]);
    let prof = RequestProfile {
        filter: Filter {
            tool_call: Some(true),
            ..Filter::default()
        },
        ..RequestProfile::default()
    };
    assert_eq!(
        p.pick(&prof, BudgetTier::Balanced).await.unwrap().id,
        "yes-tools"
    );
}

#[tokio::test]
async fn token_window_filters() {
    let p = picker(vec![
        ctx(model("p1", "small"), 8192),
        ctx(model("p1", "large"), 200_000),
    ]);
    let prof = RequestProfile {
        max_input_tokens: 100_000,
        ..RequestProfile::default()
    };
    assert_eq!(
        p.pick(&prof, BudgetTier::Balanced).await.unwrap().id,
        "large"
    );
    let p = picker(vec![
        out(model("p1", "tiny-output"), 4096),
        out(model("p1", "big-output"), 32768),
    ]);
    let prof = RequestProfile {
        max_output_tokens: 16384,
        ..RequestProfile::default()
    };
    assert_eq!(
        p.pick(&prof, BudgetTier::Balanced).await.unwrap().id,
        "big-output"
    );
}

#[tokio::test]
async fn unknown_limits_pass() {
    let p = picker(vec![model("p1", "unknown")]);
    let prof = RequestProfile {
        max_input_tokens: 1_000_000,
        max_output_tokens: 1_000_000,
        ..RequestProfile::default()
    };
    assert_eq!(
        p.pick(&prof, BudgetTier::Cheap).await.unwrap().id,
        "unknown"
    );
}

#[tokio::test]
async fn budget_tiers() {
    let none = RequestProfile::default();
    let p = picker(vec![
        priced(model("p1", "expensive"), 10.0, 10.0),
        priced(model("p1", "cheap"), 1.0, 1.0),
        priced(model("p1", "mid"), 5.0, 5.0),
    ]);
    assert_eq!(p.pick(&none, BudgetTier::Cheap).await.unwrap().id, "cheap");
    // weighted = input*0.75 + output*0.25; prices [1, 5, 10] → median 5.
    assert_eq!(p.pick(&none, BudgetTier::Balanced).await.unwrap().id, "mid");
    let p = picker(vec![
        priced(ctx(model("p1", "small"), 4096), 1.0, 1.0),
        priced(ctx(model("p1", "huge"), 200_000), 1.0, 1.0),
        priced(ctx(model("p1", "medium"), 128_000), 1.0, 1.0),
    ]);
    assert_eq!(p.pick(&none, BudgetTier::Premium).await.unwrap().id, "huge");
}

#[tokio::test]
async fn ties_break_by_provider_then_id() {
    let none = RequestProfile::default();
    for budget in [BudgetTier::Cheap, BudgetTier::Premium] {
        let p = picker(vec![
            priced(ctx(model("zeta", "model-z"), 100_000), 2.0, 2.0),
            priced(ctx(model("alpha", "model-a"), 100_000), 2.0, 2.0),
        ]);
        for _ in 0..10 {
            assert_eq!(
                ids(&p.pick(&none, budget).await.unwrap()),
                ("alpha", "model-a")
            );
        }
    }
    let p = picker(vec![
        priced(model("zeta", "model-z"), 2.0, 2.0),
        priced(model("alpha", "model-a"), 2.0, 2.0),
        priced(model("mu", "model-m"), 2.0, 2.0),
    ]);
    for _ in 0..10 {
        assert_eq!(
            ids(&p.pick(&none, BudgetTier::Balanced).await.unwrap()),
            ("mu", "model-m")
        );
    }
}

#[tokio::test]
async fn missing_cost_prefers_cheap_and_loses_premium_tiebreak() {
    let none = RequestProfile::default();
    let p = picker(vec![
        priced(model("p1", "priced"), 5.0, 5.0),
        model("p1", "local"),
    ]);
    assert_eq!(p.pick(&none, BudgetTier::Cheap).await.unwrap().id, "local");
    let p = picker(vec![
        priced(ctx(model("p1", "priced"), 100_000), 5.0, 5.0),
        ctx(model("p1", "local"), 100_000),
    ]);
    assert_eq!(
        p.pick(&none, BudgetTier::Premium).await.unwrap().id,
        "priced"
    );
}

#[tokio::test]
async fn no_match_carries_detail() {
    let p = picker(vec![tools(model("p1", "a"), false)]);
    let prof = RequestProfile {
        filter: Filter {
            tool_call: Some(true),
            ..Filter::default()
        },
        ..RequestProfile::default()
    };
    let e = p.pick(&prof, BudgetTier::Cheap).await.unwrap_err();
    let PickError::NoMatch(n) = &e else {
        panic!("{e}")
    };
    assert!(e.is_no_match());
    assert_eq!((n.candidate_count, n.eliminated.len()), (0, 0));
    assert_eq!(
        e.to_string(),
        "llm: no provider matches: 0 candidates, 0 eliminated"
    );

    let p = picker(vec![
        ctx(model("p1", "small"), 1000),
        ctx(model("p1", "tiny"), 500),
    ]);
    let prof = RequestProfile {
        max_input_tokens: 100_000,
        ..RequestProfile::default()
    };
    let PickError::NoMatch(n) = p.pick(&prof, BudgetTier::Cheap).await.unwrap_err() else {
        panic!()
    };
    assert_eq!(n.candidate_count, 2);
    assert_eq!(n.eliminated.len(), 2);
    assert!(n.eliminated.iter().all(|e| e.stage == ELIM_CONTEXT_WINDOW));
    assert_eq!(
        n.eliminated[0].detail,
        "context_window 1000 < required 100000"
    );
}

struct Failing;
impl ModelCatalog for Failing {
    fn models<'a>(&'a self, _: &'a Filter) -> BoxFuture<'a, Result<Vec<Model>, LlmError>> {
        Box::pin(async { Err(LlmError::invalid("boom")) })
    }
}

#[tokio::test]
async fn catalog_error_is_not_a_no_match() {
    let p = Picker::new(Arc::new(Failing));
    let e = p
        .pick(&RequestProfile::default(), BudgetTier::Cheap)
        .await
        .unwrap_err();
    assert!(!e.is_no_match());
    assert_eq!(e.to_string(), "llm: query registry: boom");
}

#[test]
fn budget_tier_parse_and_labels() {
    for (s, t) in [
        ("cheap", BudgetTier::Cheap),
        (" Balanced ", BudgetTier::Balanced),
        ("PREMIUM", BudgetTier::Premium),
    ] {
        assert_eq!(BudgetTier::parse(s).unwrap(), t);
    }
    assert_eq!(
        BudgetTier::parse("bogus").unwrap_err().to_string(),
        r#"llm: invalid budget tier "bogus""#
    );
    assert_eq!(BudgetTier::Premium.as_str(), "premium");
}

fn entry(scheme: &str, model: &str, enabled: bool) -> PoolEntry {
    PoolEntry {
        enabled,
        ..PoolEntry::new(scheme, model)
    }
}

#[test]
fn filter_by_pool_semantics() {
    let c = vec![model("openai", "a"), model("anthropic", "b")];
    let (s, e) = filter_by_pool(c.clone(), &[]);
    assert_eq!((s.len(), e.len()), (2, 0), "empty pool accepts everything");
    let (s, e) = filter_by_pool(
        c.clone(),
        &[entry("openai", "a", true), entry("anthropic", "b", false)],
    );
    assert_eq!(s.len(), 1);
    assert_eq!(ids(&s[0]), ("openai", "a"));
    assert_eq!(e.len(), 1);
    assert_eq!(
        (e[0].stage, e[0].detail.as_str()),
        (ELIM_POOL_DISABLED, "not in pool or disabled")
    );
    assert_eq!(ids(&e[0].model), ("anthropic", "b"));
}

#[tokio::test]
async fn pick_in_pool() {
    let p = picker(vec![
        priced(model("openai", "gpt-3.5-turbo"), 1.0, 1.0),
        priced(model("anthropic", "claude-sonnet-4-5"), 3.0, 15.0),
    ]);
    let none = RequestProfile::default();
    // Narrowed to the pool.
    let pool = [entry("anthropic", "claude-sonnet-4-5", true)];
    assert_eq!(
        p.pick_in_pool(&none, BudgetTier::Cheap, &pool)
            .await
            .unwrap()
            .id,
        "claude-sonnet-4-5"
    );
    // Empty pool equals plain pick.
    assert_eq!(
        p.pick_in_pool(&none, BudgetTier::Cheap, &[]).await.unwrap(),
        p.pick(&none, BudgetTier::Cheap).await.unwrap()
    );
    // Disabled entry is eliminated; pool and window eliminations merge.
    let pool = [entry("openai", "gpt-3.5-turbo", false)];
    let PickError::NoMatch(n) = p
        .pick_in_pool(&none, BudgetTier::Cheap, &pool)
        .await
        .unwrap_err()
    else {
        panic!()
    };
    assert_eq!((n.candidate_count, n.eliminated.len()), (2, 2));
    assert!(n.eliminated.iter().all(|e| e.stage == ELIM_POOL_DISABLED));
    let big = RequestProfile {
        max_input_tokens: 10,
        ..RequestProfile::default()
    };
    let p = picker(vec![ctx(model("a", "x"), 5), model("a", "y")]);
    let pool = [entry("a", "x", true)];
    let PickError::NoMatch(n) = p
        .pick_in_pool(&big, BudgetTier::Cheap, &pool)
        .await
        .unwrap_err()
    else {
        panic!()
    };
    assert_eq!(
        n.eliminated.iter().map(|e| e.stage).collect::<Vec<_>>(),
        [ELIM_POOL_DISABLED, ELIM_CONTEXT_WINDOW]
    );
}

struct Static(Result<Classification, ()>);
impl PromptClassifier for Static {
    fn classify<'a>(&'a self, _: &'a str) -> BoxFuture<'a, Result<Classification, LlmError>> {
        Box::pin(async move {
            self.0
                .clone()
                .map_err(|()| LlmError::invalid("scorer unreachable"))
        })
    }
}

fn routing(c: Option<Result<Classification, ()>>) -> ClassifierRouting {
    ClassifierRouting {
        classifier: c.map(|c| Arc::new(Static(c)) as Arc<dyn PromptClassifier>),
        routes: BTreeMap::from([
            (
                "code".into(),
                ClassifiedRoute {
                    pool: vec![PoolEntry::new("anthropic", "claude-sonnet-4-5")],
                    budget: None,
                },
            ),
            (
                "hard".into(),
                ClassifiedRoute {
                    pool: vec![],
                    budget: Some(BudgetTier::Premium),
                },
            ),
            ("empty".into(), ClassifiedRoute::default()),
        ]),
        min_confidence: 0.6,
    }
}

fn verdict(label: &str, confidence: f64) -> Option<Result<Classification, ()>> {
    Some(Ok(Classification {
        label: label.into(),
        confidence,
    }))
}

#[tokio::test]
async fn pick_classified_routes_and_falls_back() {
    let p = picker(vec![
        priced(ctx(model("openai", "gpt-3.5-turbo"), 16_000), 0.5, 1.5),
        priced(
            ctx(model("anthropic", "claude-sonnet-4-5"), 200_000),
            3.0,
            15.0,
        ),
    ]);
    let none = RequestProfile::default();
    let pick = |r: ClassifierRouting| {
        let p = p.clone();
        async move {
            p.pick_classified(
                &r,
                "prompt",
                &RequestProfile::default(),
                BudgetTier::Cheap,
                &[],
            )
            .await
            .unwrap()
        }
    };
    let _ = none;
    assert_eq!(
        pick(routing(verdict("code", 0.9))).await.id,
        "claude-sonnet-4-5",
        "label steers the pool"
    );
    assert_eq!(
        pick(routing(verdict("hard", 0.9))).await.id,
        "claude-sonnet-4-5",
        "label steers the budget"
    );
    assert_eq!(
        pick(routing(verdict("empty", 0.9))).await.id,
        "gpt-3.5-turbo",
        "an empty route inherits"
    );
    for (name, r) in [
        ("classifier error", routing(Some(Err(())))),
        ("low confidence", routing(verdict("code", 0.3))),
        ("unknown label", routing(verdict("poetry", 0.99))),
        ("empty label", routing(verdict("", 0.99))),
        ("nil classifier", routing(None)),
    ] {
        assert_eq!(pick(r).await.id, "gpt-3.5-turbo", "{name}");
    }
}

#[tokio::test]
async fn a_classified_route_with_no_match_does_not_fall_back() {
    let p = picker(vec![model("openai", "gpt-3.5-turbo")]);
    let r = routing(verdict("code", 0.9));
    let e = p
        .pick_classified(&r, "x", &RequestProfile::default(), BudgetTier::Cheap, &[])
        .await
        .unwrap_err();
    assert!(e.is_no_match());
}

type Events = Arc<Mutex<Vec<TraceEvent>>>;

fn traced(models: Vec<Model>, flag: Option<&'static str>) -> (Picker, Events) {
    let events: Events = Arc::default();
    let sink = events.clone();
    let trace = Trace::from_env(
        move |k| {
            (k == "LLM_PICKER_TRACE")
                .then(|| flag.map(str::to_string))
                .flatten()
        },
        move |e| sink.lock().unwrap().push(e.clone()),
    );
    (picker(models).with_trace(trace), events)
}

#[tokio::test]
async fn trace_matched_no_match_off_and_accepted_values() {
    let none = RequestProfile::default();
    let (p, ev) = traced(
        vec![
            priced(model("p1", "expensive"), 10.0, 10.0),
            priced(model("p1", "cheap"), 1.0, 1.0),
        ],
        Some("1"),
    );
    p.pick(&none, BudgetTier::Cheap).await.unwrap();
    let got = ev.lock().unwrap()[0].to_string();
    for want in [
        "msg=llm.pick",
        "picker.budget=cheap",
        "picker.outcome=matched",
        "picker.chosen.provider=p1",
        "picker.chosen.model=cheap",
        "picker.candidate_count=2",
        "picker.eliminated_count=0",
        "picker.filter.tool_call=<nil>",
    ] {
        assert!(got.contains(want), "{want} missing in {got}");
    }
    let (p, ev) = traced(vec![ctx(model("p1", "a"), 10)], Some("on"));
    let prof = RequestProfile {
        max_input_tokens: 100,
        ..RequestProfile::default()
    };
    p.pick(&prof, BudgetTier::Cheap).await.unwrap_err();
    let got = ev.lock().unwrap()[0].to_string();
    assert!(
        got.contains("picker.outcome=no_match")
            && got.contains("picker.profile.max_input_tokens=100"),
        "{got}"
    );
    assert!(!got.contains("picker.chosen"), "{got}");
    for (flag, on) in [
        (None, false),
        (Some("0"), false),
        (Some("maybe"), false),
        (Some("TRUE"), true),
        (Some("Yes"), true),
        (Some(" on "), true),
    ] {
        let (p, ev) = traced(vec![model("p1", "a")], flag);
        p.pick(&none, BudgetTier::Cheap).await.unwrap();
        assert_eq!(!ev.lock().unwrap().is_empty(), on, "{flag:?}");
    }
}

#[tokio::test]
async fn trace_classify_events() {
    let (p, ev) = traced(vec![model("anthropic", "claude-sonnet-4-5")], Some("1"));
    let r = routing(verdict("code", 0.9));
    p.pick_classified(&r, "x", &RequestProfile::default(), BudgetTier::Cheap, &[])
        .await
        .unwrap();
    let r = routing(verdict("code", 0.1));
    p.pick_classified(&r, "x", &RequestProfile::default(), BudgetTier::Cheap, &[])
        .await
        .unwrap();
    let e = ev.lock().unwrap();
    let texts: Vec<String> = e.iter().map(ToString::to_string).collect();
    assert!(texts[0].starts_with("msg=llm.classify classifier.outcome=classified classifier.label=code classifier.confidence=0.9"), "{}", texts[0]);
    assert!(texts[1].starts_with("msg=llm.pick"), "{}", texts[1]);
    assert!(
        texts[2].contains("classifier.outcome=fallback classifier.fallback_reason=low_confidence"),
        "{}",
        texts[2]
    );
}

#[test]
fn filter_matching() {
    let mut m = model("p1", "GPT-Mini");
    m.family = "gpt".into();
    m.modalities.input = vec!["text".into(), "image".into()];
    m.reasoning = true;
    let f = |f: Filter| f.matches(&m);
    assert!(f(Filter::default()));
    assert!(f(Filter {
        input: vec!["image".into()],
        family: "gpt".into(),
        reasoning: Some(true),
        query: "mini".into(),
        ..Filter::default()
    }));
    assert!(!f(Filter {
        input: vec!["audio".into()],
        ..Filter::default()
    }));
    assert!(
        !f(Filter {
            provider: "P1".into(),
            ..Filter::default()
        }),
        "case-sensitive"
    );
    assert!(!f(Filter {
        open_weights: Some(true),
        ..Filter::default()
    }));
}
