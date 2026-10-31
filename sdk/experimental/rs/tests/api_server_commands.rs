//! The command-tree projection against the `projection` table of
//! `contracts/parity/api.json`. Go is the authority; this projects the
//! equivalent tree of a kit CLI root, so the routes, the discovery
//! listing, the invocation decoding, the exit-class to status mapping
//! and the OpenAPI operations all come from the model the root defines.
#![cfg(all(feature = "api-server", feature = "cli"))]
#![allow(clippy::result_large_err)]

use std::collections::BTreeMap;
use std::path::PathBuf;
use std::sync::{Arc, Mutex};

use axum::body::Body;
use axum::http::Request;
use hop_top_kit::api_server::commands::{
    exit_status_table, status_for_exit_code, CommandProjection,
};
use hop_top_kit::cli::{ArgSpec, CommandSpec, Config, FlagSpec, Root};
use hop_top_kit::output::CliError;
use serde_json::Value;
use tower::ServiceExt;

type Seen = Arc<Mutex<Option<(String, BTreeMap<String, String>, Vec<String>)>>>;

fn corpus() -> Value {
    let path = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../../contracts/parity/api.json");
    let v: Value = serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap();
    v["projection"].clone()
}

/// The corpus tree as a kit root; `seen` records what a command saw.
fn build(c: &Value, seen: &Seen) -> Root {
    let mut cfg = Config::new("fix", "1.0.0", "Fixture");
    cfg.disable_validate = true;
    let mut root = Root::with_config(cfg);
    let mut top: Vec<(String, Vec<&Value>)> = Vec::new();
    for n in c["tree"].as_array().unwrap() {
        let first = n["path"][0].as_str().unwrap().to_string();
        match top.iter_mut().find(|(k, _)| *k == first) {
            Some((_, v)) => v.push(n),
            None => top.push((first, vec![n])),
        }
    }
    for (name, nodes) in &top {
        let cmd = group(&mut root, name, nodes, 1, seen);
        root.add_command(cmd);
    }
    root
}

fn group(root: &mut Root, name: &str, nodes: &[&Value], depth: usize, seen: &Seen) -> CommandSpec {
    let leaf = nodes
        .iter()
        .find(|n| n["path"].as_array().unwrap().len() == depth);
    if let Some(n) = leaf {
        return leaf_command(root, name, n, seen);
    }
    let mut cmd = CommandSpec::new(name.to_string());
    let mut subs: Vec<(String, Vec<&Value>)> = Vec::new();
    for n in nodes {
        let next = n["path"][depth].as_str().unwrap().to_string();
        match subs.iter_mut().find(|(k, _)| *k == next) {
            Some((_, v)) => v.push(n),
            None => subs.push((next, vec![n])),
        }
    }
    for (sub, ns) in &subs {
        cmd = cmd.subcommand(group(root, sub, ns, depth + 1, seen));
    }
    cmd
}

fn leaf_command(root: &mut Root, name: &str, n: &Value, seen: &Seen) -> CommandSpec {
    let words: Vec<String> = n["path"]
        .as_array()
        .unwrap()
        .iter()
        .map(|w| w.as_str().unwrap().to_string())
        .collect();
    let path = words.join(" ");
    let mut cmd =
        CommandSpec::new(name.to_string()).about(n["summary"].as_str().unwrap().to_string());
    for f in n["flags"].as_array().unwrap() {
        let fname = f["name"].as_str().unwrap().to_string();
        cmd = cmd.flag(match f["type"].as_str().unwrap() {
            "bool" => FlagSpec::bool(fname),
            "int" => FlagSpec::int(fname),
            _ => FlagSpec::string(fname),
        });
    }
    let arg_names: Vec<String> = n["args"]
        .as_array()
        .unwrap()
        .iter()
        .map(|a| a["name"].as_str().unwrap().to_string())
        .collect();
    for a in n["args"].as_array().unwrap() {
        let an = a["name"].as_str().unwrap().to_string();
        let mut arg = ArgSpec::new(an.clone());
        if a["required"].as_bool().unwrap_or(false) {
            arg = arg.required();
        }
        if an == "item" {
            arg = arg.variadic();
        }
        cmd = cmd.arg(arg);
    }
    if n["reason"] == "hidden-internal" {
        cmd = cmd.hidden();
    }
    if n["source"] == "declared" {
        cmd = cmd.side_effect(n["side_effect"].as_str().unwrap());
    }
    let _ = &root;
    if n["invocable"].as_bool().unwrap() {
        let seen = seen.clone();
        let p = path.clone();
        cmd = cmd.handler(move |inv, out, _| {
            *seen.lock().unwrap() = Some((
                p.clone(),
                inv.flags
                    .iter()
                    .map(|(k, v)| (k.clone(), v.last().cloned().unwrap_or_default()))
                    .collect(),
                inv.args.clone(),
            ));
            let _ = &arg_names;
            write!(out, "ran {p}").unwrap();
            match inv.flag("exit").and_then(|v| v.parse::<i32>().ok()) {
                Some(code) if code != 0 => {
                    let mut e = CliError::generic("boom");
                    e.exit_code = code;
                    Err(e)
                }
                _ => Ok(()),
            }
        });
    }
    cmd
}

async fn send(app: axum::Router, method: &str, path: &str, body: Option<&Value>) -> (u16, Value) {
    let mut req = Request::builder().method(method).uri(path);
    let b = match body {
        Some(v) => {
            req = req.header("content-type", "application/json");
            Body::from(v.to_string())
        }
        None => Body::empty(),
    };
    let resp = app.oneshot(req.body(b).unwrap()).await.unwrap();
    let status = resp.status().as_u16();
    let bytes = axum::body::to_bytes(resp.into_body(), 1 << 20)
        .await
        .unwrap();
    (
        status,
        serde_json::from_slice(&bytes).unwrap_or(Value::Null),
    )
}

#[test]
fn exit_status_matches_the_corpus() {
    let c = corpus();
    let want: Vec<(i32, u16)> = c["exit_status"]
        .as_array()
        .unwrap()
        .iter()
        .map(|r| {
            (
                r["exit_code"].as_i64().unwrap() as i32,
                r["status"].as_u64().unwrap() as u16,
            )
        })
        .collect();
    assert_eq!(exit_status_table(), want);
    for (code, status) in want {
        assert_eq!(status_for_exit_code(code), status);
    }
}

#[tokio::test(flavor = "multi_thread")]
async fn cases_match_the_corpus() {
    let c = corpus();
    for case in c["cases"].as_array().unwrap() {
        let seen: Seen = Arc::default();
        let root = build(&c, &seen);
        let proj = CommandProjection::new(Arc::new(root), "fix", "1.0.0");
        let name = case["name"].as_str().unwrap();
        let (status, out) = send(
            proj.router(),
            case["method"].as_str().unwrap(),
            case["path"].as_str().unwrap(),
            case.get("body"),
        )
        .await;
        assert_eq!(
            status as u64,
            case["status"].as_u64().unwrap(),
            "{name}: {out}"
        );
        if let Some(code) = case.get("error_code") {
            assert_eq!(&out["code"], code, "{name}");
        }
        if let Some(code) = case.get("exit_code") {
            assert_eq!(&out["exit_code"], code, "{name}");
        }
        if let Some(call) = case.get("call") {
            let got = seen
                .lock()
                .unwrap()
                .clone()
                .unwrap_or_else(|| panic!("{name}: command did not run"));
            let want_path = call["path"]
                .as_array()
                .unwrap()
                .iter()
                .map(|w| w.as_str().unwrap())
                .collect::<Vec<_>>()
                .join(" ");
            assert_eq!(got.0, want_path, "{name}");
            let want_flags: BTreeMap<String, String> = call["flags"]
                .as_object()
                .unwrap()
                .iter()
                .map(|(k, v)| (k.clone(), v.as_str().unwrap().to_string()))
                .collect();
            assert_eq!(got.1, want_flags, "{name}");
            let want_args: Vec<String> = call["args"]
                .as_array()
                .unwrap()
                .iter()
                .map(|a| a.as_str().unwrap().to_string())
                .collect();
            assert_eq!(got.2, want_args, "{name}");
        } else if case.get("exit_code").is_none() && matches!(status, 400 | 404 | 405) {
            assert!(
                seen.lock().unwrap().is_none(),
                "{name}: a refused request must not run the command"
            );
        }
    }
}

#[test]
fn discovery_matches_the_corpus() {
    let c = corpus();
    let proj = CommandProjection::new(Arc::new(build(&c, &Arc::default())), "fix", "1.0.0");
    let doc = proj.discovery();
    let cmds = doc["commands"].as_array().unwrap();
    let invocable: Vec<&str> = cmds
        .iter()
        .filter(|e| e["invocable"] == true)
        .map(|e| e["name"].as_str().unwrap())
        .collect();
    let want: Vec<&str> = c["discovery"]["invocable_first"]
        .as_array()
        .unwrap()
        .iter()
        .map(|v| v.as_str().unwrap())
        .collect();
    assert_eq!(invocable, want);
    // invocable entries come before withheld ones
    let first_withheld = cmds.iter().position(|e| e["invocable"] == false).unwrap();
    assert!(cmds[first_withheld..]
        .iter()
        .all(|e| e["invocable"] == false));
    for w in c["discovery"]["withheld"].as_array().unwrap() {
        let e = cmds
            .iter()
            .find(|e| e["name"] == w["name"])
            .expect("listed");
        assert_eq!(e["invocable"], false);
        assert_eq!(e["reason"], w["reason"]);
        assert!(e.get("route").is_none() && e.get("method").is_none());
    }
    assert_eq!(doc["reasons"], c["discovery"]["reasons"]);
    for (name, want) in c["discovery"]["entries"].as_object().unwrap() {
        let e = cmds
            .iter()
            .find(|e| e["name"] == name.as_str())
            .expect("listed");
        for k in [
            "method",
            "route",
            "stream_route",
            "side_effect",
            "side_effect_source",
        ] {
            assert_eq!(e[k], want[k], "{name}.{k}");
        }
    }
    assert_eq!(doc["prefix"], "/v1/commands");
}

#[test]
fn openapi_matches_the_corpus() {
    let c = corpus();
    let proj = CommandProjection::new(Arc::new(build(&c, &Arc::default())), "fix", "1.0.0");
    let doc = proj.openapi("Fixture", "1.0.0").to_value();
    let paths = doc["paths"].as_object().unwrap();
    for want in c["openapi"].as_array().unwrap() {
        let path = want["path"].as_str().unwrap();
        let op = &paths[path][want["method"].as_str().unwrap()];
        assert_eq!(op["operationId"], want["operation_id"], "{path}");
        if let Some(q) = want.get("query") {
            let mut names: Vec<&str> = op["parameters"]
                .as_array()
                .unwrap()
                .iter()
                .map(|p| p["name"].as_str().unwrap())
                .collect();
            names.sort();
            let w: Vec<&str> = q
                .as_array()
                .unwrap()
                .iter()
                .map(|v| v.as_str().unwrap())
                .collect();
            assert_eq!(names, w, "{path}");
        }
        if let Some(b) = want.get("body_flags") {
            let props = &op["requestBody"]["content"]["application/json"]["schema"]["properties"]
                ["flags"]["properties"];
            let mut names: Vec<&str> = props
                .as_object()
                .map(|m| m.keys().map(String::as_str).collect())
                .unwrap_or_default();
            names.sort();
            let w: Vec<&str> = b
                .as_array()
                .unwrap()
                .iter()
                .map(|v| v.as_str().unwrap())
                .collect();
            assert_eq!(names, w, "{path}");
        }
        if let Some(se) = want.get("side_effect") {
            assert_eq!(&op["x-kit-side-effect"], se, "{path}");
            assert_eq!(
                op["x-kit-side-effect-source"], want["side_effect_source"],
                "{path}"
            );
        }
        if let Some(r) = want.get("responses") {
            let mut codes: Vec<&str> = op["responses"]
                .as_object()
                .unwrap()
                .keys()
                .map(String::as_str)
                .collect();
            codes.sort();
            let w: Vec<&str> = r
                .as_array()
                .unwrap()
                .iter()
                .map(|v| v.as_str().unwrap())
                .collect();
            assert_eq!(codes, w, "{path}");
        }
    }
    for p in c["openapi_absent"].as_array().unwrap() {
        assert!(!paths.contains_key(p.as_str().unwrap()));
    }
}

#[tokio::test(flavor = "multi_thread")]
async fn stream_twin_sends_events_then_the_result() {
    let c = corpus();
    let proj = CommandProjection::new(Arc::new(build(&c, &Arc::default())), "fix", "1.0.0");
    let resp = proj
        .router()
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1/commands/tidy/stream")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(resp.status(), 200);
    assert_eq!(resp.headers()["content-type"], "text/event-stream");
    let text = String::from_utf8(
        axum::body::to_bytes(resp.into_body(), 1 << 20)
            .await
            .unwrap()
            .to_vec(),
    )
    .unwrap();
    assert!(text.contains("event: event\n"), "{text}");
    assert!(text.contains("\"kind\":\"stdout\""), "{text}");
    assert!(text.contains("event: result\n"), "{text}");
    assert!(text.contains("\"status\":200"), "{text}");
}

#[tokio::test(flavor = "multi_thread")]
async fn gate_refuses_before_the_command_runs() {
    let c = corpus();
    let seen: Seen = Arc::default();
    let proj =
        CommandProjection::new(Arc::new(build(&c, &seen)), "fix", "1.0.0").with_gate(|d, _| {
            if d.side_effect == hop_top_kit::api_server::commands::SideEffectClass::Destructive {
                Err(hop_top_kit::api_server::commands::refusal(
                    "destructive_blocked",
                    "blocked here",
                ))
            } else {
                Ok(())
            }
        });
    let (status, out) = send(
        proj.router(),
        "POST",
        "/v1/commands/widget/delete",
        Some(&serde_json::json!({})),
    )
    .await;
    assert_eq!(
        (status, out["code"].as_str()),
        (403, Some("destructive_blocked"))
    );
    assert!(seen.lock().unwrap().is_none());
}
