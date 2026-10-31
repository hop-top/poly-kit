//! The wasm-safe core over the memory store: no files, an injected
//! clock and env.
#![cfg(feature = "consent")]

use std::sync::{Arc, Mutex};
use std::time::{Duration as StdDuration, SystemTime, UNIX_EPOCH};

use hop_top_kit::consent::{
    Answer, DecisionSource, Duration, Effect, Error, Grant, Grants, MemoryGrantStore, Prompt,
    Reason, CAP_TELEMETRY,
};

fn session(env: &'static [(&'static str, &'static str)]) -> (Grants, Arc<Mutex<SystemTime>>) {
    let now = Arc::new(Mutex::new(
        UNIX_EPOCH + StdDuration::from_secs(1_772_323_200),
    ));
    let n = now.clone();
    let g = Grants::builder(Arc::new(MemoryGrantStore::new()))
        .clock(move || *n.lock().unwrap())
        .env(move |k| {
            env.iter()
                .find(|(e, _)| *e == k)
                .map(|(_, v)| v.to_string())
                .unwrap_or_default()
        })
        .build();
    (g, now)
}

fn refusal(r: Result<impl std::fmt::Debug, Error>) -> (Reason, &'static str, i32, &'static str) {
    let e = r.unwrap_err();
    let re = e
        .refusal()
        .unwrap_or_else(|| panic!("want refusal, got {e}"))
        .clone();
    (re.reason, re.code(), re.exit_code(), re.transience())
}

#[test]
fn allow_once_is_consumed_by_one_use() {
    let (g, _) = session(&[]);
    let ask = |_: &Prompt| -> Result<Answer, String> {
        Ok(Answer {
            decision: Effect::Allow,
            duration: Duration::Once,
            expires_at: None,
        })
    };
    g.request("io.microphone.capture", "app:dek", Some(&ask))
        .unwrap();
    g.use_grant("io.microphone.capture", "app:dek").unwrap();
    assert_eq!(
        refusal(g.use_grant("io.microphone.capture", "app:dek")),
        (Reason::Unset, "CONSENT_REFUSED", 7, "transient")
    );
}

#[test]
fn a_standing_deny_is_permanent_and_never_prompted() {
    let (g, _) = session(&[]);
    g.decide(Grant::new(
        "io.files.read",
        "",
        Effect::Deny,
        Duration::Always,
        DecisionSource::Flag,
    ))
    .unwrap();
    let ask = |_: &Prompt| -> Result<Answer, String> { panic!("prompted") };
    assert_eq!(
        refusal(g.request("io.files.read", "app:dek", Some(&ask))),
        (Reason::Denied, "UNAUTHORIZED", 5, "permanent")
    );
}

#[test]
fn an_until_grant_expires() {
    let (g, now) = session(&[]);
    let t0 = *now.lock().unwrap();
    let mut gr = Grant::new(
        "io.screen.capture",
        "",
        Effect::Allow,
        Duration::Until,
        DecisionSource::Prompt,
    );
    gr.expires_at = Some(t0 + StdDuration::from_secs(60));
    g.decide(gr).unwrap();
    assert!(g.check("io.screen.capture", "app:dek").unwrap().allowed);
    *now.lock().unwrap() = t0 + StdDuration::from_secs(60);
    assert_eq!(
        g.check("io.screen.capture", "app:dek").unwrap().reason,
        Reason::Expired
    );
}

#[test]
fn telemetry_through_the_memory_store_honours_do_not_track() {
    let (g, _) = session(&[("DO_NOT_TRACK", "1")]);
    g.decide(Grant::new(
        CAP_TELEMETRY,
        "",
        Effect::Allow,
        Duration::Always,
        DecisionSource::Prompt,
    ))
    .unwrap();
    let v = g.check(CAP_TELEMETRY, "").unwrap();
    assert_eq!((v.allowed, v.reason), (false, Reason::Denied));
    let (g, _) = session(&[]);
    assert_eq!(g.check(CAP_TELEMETRY, "").unwrap().reason, Reason::Unset);
}
