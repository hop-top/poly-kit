//! YAML documents for the grants file and the legacy telemetry records,
//! over the config module's yaml.v3-shaped node tree: scalars keep their
//! source text, so decoding reads what Go's `yaml.Node.Value` reads.

use std::time::SystemTime;

use crate::config::yaml::resolve::{INT_TAG, STR_TAG};
use crate::config::yaml::{emit, parse, Kind, Node};

use super::error::GrantError;
use super::grant::{sort_grants, DecisionSource, Duration, Grant, Key};
use super::telemetry::TelemetryDecision;
use super::time::{format_rfc3339, parse_rfc3339};

const GRANTS_VERSION: &str = "1";
const GRANT_FIELDS: [&str; 8] = [
    "capability",
    "subject",
    "decision",
    "duration",
    "decided_at",
    "expires_at",
    "decision_source",
    "prompt_version",
];

/// Parses a document; `Ok(None)` for an empty or comment-only one.
pub(crate) fn parse_doc(src: &str) -> Result<Option<Node>, String> {
    if src.is_empty() {
        return Ok(None);
    }
    parse(src).map_err(|e| e.to_string())
}

/// The new text for a legacy file whose values must become `expected`
/// (Go's tree edit): `src` itself when nothing changes, else `edit(src)`
/// when it keeps comments and reads back as `expected`, else `expected`
/// re-emitted whole. yaml.v3 keeps comments on its Node round-trip;
/// the splice is how this port does too.
pub(crate) fn edit_or_render(
    src: Option<&str>,
    expected: &Node,
    edit: &dyn Fn(&str) -> Option<String>,
) -> String {
    use crate::config::getter::node_to_value;
    if let Some(src) = src.filter(|s| !s.trim().is_empty()) {
        let want = node_to_value(expected);
        let same =
            |t: &str| parse_doc(t).ok().flatten().map(|n| node_to_value(&n)) == Some(want.clone());
        if same(src) {
            return src.to_string();
        }
        if let Some(t) = edit(src).filter(|t| same(t)) {
            return t;
        }
    }
    render(expected)
}

/// Splices `key` out of `src`; when that prunes `parent` (Go leaves the
/// emptied mapping), puts `parent: {}` back.
pub(crate) fn splice_remove(src: &str, key: &str, parent: &str) -> Option<String> {
    use crate::config::edit;
    let t = edit::unset(src, key).ok()?;
    let has_parent = parse_doc(&t)
        .ok()
        .flatten()
        .is_some_and(|n| parent.split('.').try_fold(&n, |m, k| child(m, k)).is_some());
    if has_parent {
        Some(t)
    } else {
        edit::set(&t, parent, &Node::map(Vec::new())).ok()
    }
}

/// Splices `key: value` into `src`.
pub(crate) fn splice_set(src: &str, key: &str, value: &Node) -> Option<String> {
    crate::config::edit::set(src, key, value).ok()
}

/// Renders a document as yaml.v3's `Marshal` lays it out (4 spaces).
pub(crate) fn render(doc: &Node) -> String {
    emit::document(doc, 4)
}

fn entries(m: &Node) -> Option<&Vec<(Node, Node)>> {
    match &m.target().kind {
        Kind::Map(e) => Some(e),
        _ => None,
    }
}

fn key_text(k: &Node) -> &str {
    k.scalar_value().unwrap_or("")
}

/// The value under `key` in a mapping (first match), as Go's
/// `mappingChild`.
pub(crate) fn child<'a>(m: &'a Node, key: &str) -> Option<&'a Node> {
    entries(m)?
        .iter()
        .find(|(k, _)| matches!(k.kind, Kind::Scalar(_)) && key_text(k) == key)
        .map(|(_, v)| v.target())
}

/// A mapping child, only when it is a mapping.
pub(crate) fn child_map<'a>(m: &'a Node, key: &str) -> Option<&'a Node> {
    child(m, key).filter(|n| matches!(n.kind, Kind::Map(_)))
}

fn child_mut<'a>(m: &'a mut Node, key: &str) -> Option<&'a mut Node> {
    match &mut m.kind {
        Kind::Map(e) => e
            .iter_mut()
            .find(|(k, _)| matches!(k.kind, Kind::Scalar(_)) && key_text(k) == key)
            .map(|(_, v)| v),
        _ => None,
    }
}

/// Replaces or appends `key: value` in a mapping.
pub(crate) fn set_child(m: &mut Node, key: &str, value: Node) {
    if let Some(v) = child_mut(m, key) {
        *v = value;
    } else if let Kind::Map(e) = &mut m.kind {
        e.push((Node::str(key), value));
    }
}

/// Removes `key` from a mapping, if present.
pub(crate) fn delete_child(m: &mut Node, key: &str) {
    if let Kind::Map(e) = &mut m.kind {
        if let Some(i) = e
            .iter()
            .position(|(k, _)| matches!(k.kind, Kind::Scalar(_)) && key_text(k) == key)
        {
            e.remove(i);
        }
    }
}

/// The mapping child under `key`, replacing a missing or non-mapping
/// value with an empty mapping.
pub(crate) fn ensure_map<'a>(m: &'a mut Node, key: &str) -> &'a mut Node {
    if !matches!(child_mut(m, key).map(|n| &n.kind), Some(Kind::Map(_))) {
        set_child(m, key, Node::map(Vec::new()));
    }
    child_mut(m, key).expect("just set")
}

fn scalar(n: &Node) -> &str {
    n.scalar_value().unwrap_or("")
}

fn int_node(v: i64) -> Node {
    Node::scalar(INT_TAG, v.to_string())
}

fn str_node(v: &str) -> Node {
    Node::scalar(STR_TAG, v)
}

fn time_text(t: Option<SystemTime>) -> String {
    t.map(format_rfc3339)
        .unwrap_or_else(|| "0001-01-01T00:00:00Z".to_string())
}

/// Reads a grants file document: strict, one bad record makes the whole
/// file malformed. `None` holds no grants.
pub(crate) fn decode_grants(doc: Option<&Node>) -> Result<Vec<Grant>, GrantError> {
    let Some(doc) = doc else {
        return Ok(Vec::new());
    };
    let root = doc.target();
    let Some(top) = entries(root).filter(|_| matches!(root.kind, Kind::Map(_))) else {
        return Err(GrantError::malformed("document root is not a mapping"));
    };
    if let Some((k, _)) = top
        .iter()
        .find(|(k, _)| !matches!(key_text(k), "version" | "grants"))
    {
        return Err(GrantError::malformed(format!(
            "unknown key {:?}",
            key_text(k)
        )));
    }
    let Some(v) = child(root, "version") else {
        return Err(GrantError::malformed("version is missing"));
    };
    if scalar(v) != GRANTS_VERSION {
        return Err(GrantError::malformed(format!(
            "version {:?} is not supported (want {GRANTS_VERSION})",
            scalar(v)
        )));
    }
    let Some(list) = child(root, "grants") else {
        return Ok(Vec::new());
    };
    let Kind::Seq(items) = &list.kind else {
        return Err(GrantError::malformed("grants is not a list"));
    };
    let mut out = Vec::with_capacity(items.len());
    let mut seen = std::collections::HashSet::<Key>::new();
    for (i, n) in items.iter().enumerate() {
        let g = decode_grant(n.target())
            .map_err(|e| GrantError::malformed(format!("grants[{i}]: {e}")))?;
        if !seen.insert(g.key()) {
            return Err(GrantError::malformed(format!(
                "grants[{i}]: duplicate {} for {:?}",
                g.capability, g.subject
            )));
        }
        out.push(g);
    }
    sort_grants(&mut out);
    Ok(out)
}

fn decode_grant(n: &Node) -> Result<Grant, String> {
    let Some(fields) = entries(n).filter(|_| matches!(n.kind, Kind::Map(_))) else {
        return Err("not a mapping".into());
    };
    if let Some((k, _)) = fields
        .iter()
        .find(|(k, _)| !GRANT_FIELDS.contains(&key_text(k)))
    {
        return Err(format!("unknown key {:?}", key_text(k)));
    }
    let field = |key: &str, required: bool| -> Result<String, String> {
        match child(n, key) {
            None if required => Err(format!("{key} is missing")),
            None => Ok(String::new()),
            Some(v) => match v.kind {
                Kind::Scalar(_) => Ok(scalar(v).to_string()),
                _ => Err(format!("{key} is not a scalar")),
            },
        }
    };
    let capability = field("capability", true)?;
    let subject = field("subject", true)?;
    let decision = field("decision", true)?;
    let duration = field("duration", true)?;
    let decided = field("decided_at", true)?;
    let expires = field("expires_at", false)?;
    let source = field("decision_source", true)?;
    let pv = field("prompt_version", true)?;
    let decided_at =
        Some(parse_rfc3339(&decided).ok_or_else(|| format!("decided_at: {decided:?}"))?);
    let expires_at = match expires.as_str() {
        "" => None,
        s => Some(parse_rfc3339(s).ok_or_else(|| format!("expires_at: {s:?}"))?),
    };
    let g = Grant {
        capability,
        subject,
        decision: decision.parse().map_err(|e: GrantError| e.to_string())?,
        duration: duration.parse::<Duration>().map_err(|e| e.to_string())?,
        decided_at,
        expires_at,
        decision_source: DecisionSource::from_wire(&source),
        prompt_version: pv.parse().map_err(|_| format!("prompt_version: {pv:?}"))?,
    };
    g.check_persistable().map_err(|e| e.to_string())?;
    Ok(g)
}

/// Renders the grants file, sorted.
pub(crate) fn encode_grants(mut gs: Vec<Grant>) -> Node {
    sort_grants(&mut gs);
    let list = gs
        .iter()
        .map(|g| {
            let mut m = Node::map(Vec::new());
            set_child(&mut m, "capability", str_node(&g.capability));
            set_child(&mut m, "subject", str_node(&g.subject));
            set_child(&mut m, "decision", str_node(g.decision.as_str()));
            set_child(&mut m, "duration", str_node(g.duration.as_str()));
            set_child(&mut m, "decided_at", str_node(&time_text(g.decided_at)));
            if g.duration == Duration::Until {
                set_child(&mut m, "expires_at", str_node(&time_text(g.expires_at)));
            }
            set_child(
                &mut m,
                "decision_source",
                str_node(g.decision_source.as_str()),
            );
            set_child(&mut m, "prompt_version", int_node(g.prompt_version));
            m
        })
        .collect();
    let mut doc = Node::map(Vec::new());
    set_child(&mut doc, "version", int_node(1));
    set_child(&mut doc, "grants", Node::seq(list));
    doc
}

/// `kit.telemetry.consent` (canonical) or bare `telemetry.consent`
/// (legacy): the consent mapping, if the path is all mappings.
pub(crate) fn consent_block(doc: &Node, canonical: bool) -> Option<&Node> {
    let root = doc.target();
    if !matches!(root.kind, Kind::Map(_)) {
        return None;
    }
    let base = if canonical {
        child_map(root, "kit")?
    } else {
        root
    };
    child_map(child_map(base, "telemetry")?, "consent")
}

/// Decodes a consent mapping as Go's `decodeConsentMapping`.
pub(crate) fn decode_consent(c: &Node) -> Result<TelemetryDecision, String> {
    let mut d = TelemetryDecision::unknown();
    d.state = child(c, "state")
        .map(|v| scalar(v).to_string())
        .unwrap_or_default();
    if let Some(v) = child(c, "decided_at").map(scalar).filter(|s| !s.is_empty()) {
        d.decided_at =
            Some(parse_rfc3339(v).ok_or_else(|| format!("consent: parse decided_at {v:?}"))?);
    }
    if let Some(v) = child(c, "prompt_version")
        .map(scalar)
        .filter(|s| !s.is_empty())
    {
        d.prompt_version =
            scan_int(v).ok_or_else(|| format!("consent: parse prompt_version {v:?}"))?;
    }
    if let Some(v) = child(c, "decision_source") {
        d.decision_source = DecisionSource::from_wire(scalar(v));
    }
    Ok(d)
}

/// Go's `fmt.Sscanf(v, "%d", &n)`: leading spaces, a sign, digits;
/// trailing text is ignored.
fn scan_int(v: &str) -> Option<i64> {
    let t = v.trim_start();
    let sign_len = usize::from(t.starts_with(['+', '-']));
    let digits = t[sign_len..].bytes().take_while(u8::is_ascii_digit).count();
    if digits == 0 {
        return None;
    }
    t[..sign_len + digits].parse().ok()
}

/// A consent mapping for `d`.
pub(crate) fn consent_node(d: &TelemetryDecision) -> Node {
    let mut m = Node::map(Vec::new());
    set_child(&mut m, "state", str_node(&d.state));
    set_child(&mut m, "decided_at", str_node(&time_text(d.decided_at)));
    set_child(&mut m, "prompt_version", int_node(d.prompt_version));
    set_child(
        &mut m,
        "decision_source",
        str_node(d.decision_source.as_str()),
    );
    m
}

/// Writes `d` at `kit.telemetry.consent`, creating the mappings on the
/// way and keeping every other key. The root must be a mapping.
pub(crate) fn upsert_consent(doc: &mut Node, d: &TelemetryDecision) -> Result<(), String> {
    if !matches!(doc.kind, Kind::Map(_)) {
        return Err("consent: document root is not a mapping".into());
    }
    let telemetry = ensure_map(ensure_map(doc, "kit"), "telemetry");
    set_child(telemetry, "consent", consent_node(d));
    Ok(())
}

/// Strips `kit.telemetry.consent` (canonical) or `telemetry.consent`
/// (legacy), leaving the emptied parents in place.
pub(crate) fn remove_consent(doc: &mut Node, canonical: bool) {
    if !matches!(doc.kind, Kind::Map(_)) {
        return;
    }
    let base = if canonical {
        match child_mut(doc, "kit").filter(|n| matches!(n.kind, Kind::Map(_))) {
            Some(k) => k,
            None => return,
        }
    } else {
        doc
    };
    if let Some(t) = child_mut(base, "telemetry").filter(|n| matches!(n.kind, Kind::Map(_))) {
        delete_child(t, "consent");
    }
}

/// Replaces bare `telemetry.consent` (the legacy record must exist).
pub(crate) fn replace_legacy_consent(doc: &mut Node, d: &TelemetryDecision) {
    if let Some(t) = child_mut(doc, "telemetry").filter(|n| matches!(n.kind, Kind::Map(_))) {
        set_child(t, "consent", consent_node(d));
    }
}

/// Whether `doc` is only the emptied consent path (`kit.telemetry` or
/// `telemetry` holding nothing): the file held the record and no more.
pub(crate) fn only_consent_path(doc: &Node, canonical: bool) -> bool {
    use crate::config::getter::node_to_value;
    let skeleton = if canonical {
        "kit:\n  telemetry: {}\n"
    } else {
        "telemetry: {}\n"
    };
    let want = parse_doc(skeleton)
        .ok()
        .flatten()
        .map(|n| node_to_value(&n));
    Some(node_to_value(doc)) == want
}
