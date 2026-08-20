//! MRTR confirmation and the tasks extension declaration.
//!
//! # MRTR confirmation
//!
//! The modern `tools/call` path is structured as: resolve leaf → auth
//! gate → **confirmation gate** → invoke → render. This module fills the
//! confirmation slot.
//!
//! Without key material the gate is the `X-Confirm-Token` header check,
//! identical to the legacy era. Given a key ([`crate::mcp::MountOptions::confirmation_key`])
//! *and* a client that declared form-mode elicitation, the gate instead
//! runs the spec-native MRTR round trip: the first call returns
//! `resultType: "input_required"` carrying an `elicitation/create` form
//! request and an integrity-protected `requestState`; the client
//! retries with `inputResponses.confirm.action` and the echoed state.
//!
//! Everything a retry needs lives inside `requestState` — there is no
//! server-side pending-request storage — so any instance holding the
//! same key verifies state minted by any other. That is why the key is
//! adopter-supplied with no generated-at-mount default: a per-instance
//! key would silently break exactly that guarantee.
//!
//! Two verification failures are deliberately distinct: an
//! **expired but authentic** state is a routine re-prompt, while a state
//! that **fails HMAC verification** is never honored — it is rejected,
//! then a fresh prompt is issued with newly minted state. Tampering thus
//! causes nothing worse than request failure.
//!
//! MRTR never relaxes the destructive ceiling: the policy gate runs
//! inside `Bridge::invoke`, after this gate, on every path.

use std::time::{SystemTime, UNIX_EPOCH};

use serde_json::{Map, Value};

use super::bridge::Leaf;
use super::legacy::{error_result_block, Headers};
use super::modern::{RequestMeta, RESULT_TYPE_INPUT_REQUIRED};
use super::wire::Request;
use super::HandlerConfig;

/// The reserved `inputRequests` key this flow uses; the retry's answer
/// is read from the same key in `inputResponses`.
const CONFIRM_KEY: &str = "confirm";

/// Lifetime of a minted `requestState`. Long enough for a human to
/// answer, short enough to bound the replay window the stateless design
/// cannot otherwise close.
const STATE_TTL_SECS: u64 = 300;

/// Tags the `requestState` wire format so a future change invalidates
/// (rather than misparses) old state.
const STATE_VERSION: &str = "v1";

/// The official tasks-extension identifier (SEP-2663), matching
/// `rmcp::model::task::TASKS_EXTENSION_ID`.
pub const TASKS_EXTENSION_ID: &str = "io.modelcontextprotocol/tasks";

/// Declares the tasks extension on a `server/discover` result.
///
/// Off by default: kit's surface executes every leaf synchronously, so
/// advertising task augmentation without an execution model behind it
/// would invite clients to poll `tasks/get` for tasks that never exist.
/// Adopters running long jobs opt in.
pub(super) fn declare_extension(cfg: &HandlerConfig, result: &mut Map<String, Value>) {
    if !cfg.tasks_enabled {
        return;
    }
    let mut tasks = Map::new();
    // Advertise the extension with no per-capability augmentation: the
    // surface accepts task-augmented requests but promises no
    // long-running execution semantics beyond immediate completion.
    tasks.insert("requests".into(), Value::Object(Map::new()));

    let mut extensions = Map::new();
    extensions.insert(TASKS_EXTENSION_ID.into(), Value::Object(tasks));
    result.insert("extensions".into(), Value::Object(extensions));
}

/// The confirmation gate.
///
/// Returns `None` to proceed, or the refusal/interim result plus its
/// HTTP status. The caller stamps the modern envelope onto whatever is
/// returned.
pub(super) fn confirmation_gate(
    cfg: &HandlerConfig,
    leaf: &Leaf,
    req: &Request,
    headers: &Headers,
    meta: &RequestMeta,
) -> Option<(Map<String, Value>, u16)> {
    if !leaf.class.requires_confirmation {
        return None;
    }

    let Some(key) = cfg.confirmation_key.as_deref().filter(|k| !k.is_empty()) else {
        return header_gate(headers);
    };
    // The spec forbids sending inputRequests for capabilities the client
    // has not declared; the capability stays optional precisely because
    // this fallback exists (never -32021 for confirmation).
    if !supports_form_elicitation(meta) {
        return header_gate(headers);
    }

    let binding = Binding {
        tool: leaf.path.join(" "),
        args_digest: args_digest(req),
        principal: principal(headers),
    };
    let retry = parse_retry(req);

    let Some(state) = retry.state.filter(|s| !s.is_empty()) else {
        // First call — or a retry without the state it was required to
        // echo, which is indistinguishable and equally unverifiable.
        return Some((input_required(key, leaf, &binding), 200));
    };

    match verify_state(key, &state, &binding, now_secs()) {
        // Tampered, malformed, or minted for a different request: never
        // honored. Re-prompt with fresh state.
        StateStatus::Invalid |
        // Authentic but past its TTL: a routine re-prompt, per spec
        // (re-request missing information rather than error).
        StateStatus::Expired => {
            return Some((input_required(key, leaf, &binding), 200));
        }
        StateStatus::Valid => {}
    }

    match retry.action.as_deref() {
        Some("accept") => None,
        Some("decline" | "cancel") => Some((
            error_result_block("confirmation declined")
                .as_object()
                .cloned()
                .unwrap_or_default(),
            200,
        )),
        // Missing or unusable answer: re-request rather than error.
        _ => Some((input_required(key, leaf, &binding), 200)),
    }
}

/// The default gate: a `RequiresConfirmation` leaf needs the
/// `X-Confirm-Token` header, exactly as on the legacy path.
fn header_gate(headers: &Headers) -> Option<(Map<String, Value>, u16)> {
    if headers.get("X-Confirm-Token").is_some() {
        return None;
    }
    Some((
        error_result_block("confirmation required")
            .as_object()
            .cloned()
            .unwrap_or_default(),
        428,
    ))
}

/// The request context a `requestState` is bound to.
///
/// The MAC covers every field plus the expiry, so state presented for a
/// different leaf, different arguments, or by a different principal
/// fails verification outright.
struct Binding {
    /// Space-joined leaf path, e.g. `widget purge`.
    tool: String,
    /// Hex SHA-256 of the canonically serialized `params.arguments`.
    args_digest: String,
    /// Hex SHA-256 of the `Authorization` value, or empty when absent.
    /// Hashed so credential material never enters the MAC input.
    principal: String,
}

/// The tolerant read of a request's MRTR retry members. Absent or
/// wrongly-typed members stay `None`, so malformed retries converge on a
/// fresh prompt rather than a decode error.
struct Retry {
    state: Option<String>,
    action: Option<String>,
}

/// Extracts `params.requestState` and the `inputResponses.confirm`
/// action.
fn parse_retry(req: &Request) -> Retry {
    let params = req.params.as_ref();
    Retry {
        state: params
            .and_then(|p| p.get("requestState"))
            .and_then(Value::as_str)
            .map(ToOwned::to_owned),
        action: params
            .and_then(|p| p.get("inputResponses"))
            .and_then(|r| r.get(CONFIRM_KEY))
            .and_then(|c| c.get("action"))
            .and_then(Value::as_str)
            .map(ToOwned::to_owned),
    }
}

/// Reports whether the client declared form-mode elicitation.
///
/// An empty `elicitation` object declares form-only support; a non-empty
/// one must name `form` among its modes. Anything that is not a
/// conforming object declaration counts as undeclared, failing toward
/// the header fallback rather than toward sending a request the client
/// never said it could handle.
fn supports_form_elicitation(meta: &RequestMeta) -> bool {
    let Some(modes) = meta
        .capabilities
        .get("elicitation")
        .and_then(Value::as_object)
    else {
        return false;
    };
    modes.is_empty() || modes.contains_key("form")
}

/// Builds the `input_required` result for one confirmation prompt.
///
/// Carries both `inputRequests` and `requestState` (the spec requires at
/// least one). Interim results are never cacheable: no `ttlMs` or
/// `cacheScope`, ever.
fn input_required(key: &[u8], leaf: &Leaf, binding: &Binding) -> Map<String, Value> {
    // No form fields: approval rides the elicit action itself, so the
    // requested schema is the empty object.
    let mut schema = Map::new();
    schema.insert("type".into(), Value::String("object".into()));
    schema.insert("properties".into(), Value::Object(Map::new()));

    let mut params = Map::new();
    params.insert("mode".into(), Value::String("form".into()));
    params.insert(
        "message".into(),
        Value::String(format!(
            "Approve execution of {:?}?",
            leaf.tool_name()
        )),
    );
    params.insert("requestedSchema".into(), Value::Object(schema));

    let mut create = Map::new();
    create.insert("method".into(), Value::String("elicitation/create".into()));
    create.insert("params".into(), Value::Object(params));

    let mut requests = Map::new();
    requests.insert(CONFIRM_KEY.into(), Value::Object(create));

    let mut out = Map::new();
    out.insert(
        "resultType".into(),
        Value::String(RESULT_TYPE_INPUT_REQUIRED.into()),
    );
    out.insert("inputRequests".into(), Value::Object(requests));
    out.insert(
        "requestState".into(),
        Value::String(mint_state(key, binding, now_secs() + STATE_TTL_SECS)),
    );
    out
}

/// Hex SHA-256 of the canonically serialized `params.arguments`.
///
/// Canonical form sorts object keys at every depth, so equal argument
/// sets digest identically regardless of the client's key order; absent
/// arguments canonicalize to `null`. Only the arguments participate:
/// `_meta`, `inputResponses`, and `requestState` all legitimately differ
/// between the first call and its retry, and the tool name is bound
/// separately.
fn args_digest(req: &Request) -> String {
    let arguments = req
        .params
        .as_ref()
        .and_then(|p| p.get("arguments"))
        .cloned()
        .unwrap_or(Value::Null);
    let canonical = super::wire::to_wire_bytes(&arguments);
    hex(&sha256(&canonical))
}

/// Hex SHA-256 of the `Authorization` value, or empty when absent.
fn principal(headers: &Headers) -> String {
    match headers.get("Authorization") {
        None => String::new(),
        Some(auth) => hex(&sha256(auth.as_bytes())),
    }
}

/// Computes the HMAC-SHA-256 tag binding a state to its expiry and
/// request context.
///
/// Components are written length-prefixed so the concatenation is
/// unambiguous whatever they contain (no delimiter-injection), behind a
/// domain-separation constant so the tag can never be confused with
/// another HMAC minted under a shared key.
fn state_mac(key: &[u8], binding: &Binding, exp: u64) -> [u8; 32] {
    let mut input = Vec::new();
    let domain = format!("cmdsurface-mcp-confirm-{STATE_VERSION}");
    for part in [
        domain.as_str(),
        &exp.to_string(),
        &binding.tool,
        &binding.args_digest,
        &binding.principal,
    ] {
        input.extend_from_slice(format!("{}:{}", part.len(), part).as_bytes());
    }
    hmac_sha256(key, &input)
}

/// Renders an opaque-to-clients `requestState`:
/// `v1.<expiry-unix>.<base64url(mac)>`.
///
/// Only the version and expiry travel in the clear; the binding is
/// reconstructed from the retry request itself at verification time,
/// which is what keeps the state small and the flow stateless.
fn mint_state(key: &[u8], binding: &Binding, exp: u64) -> String {
    format!(
        "{STATE_VERSION}.{exp}.{}",
        base64url_encode(&state_mac(key, binding, exp))
    )
}

/// The outcome of verifying a presented `requestState`.
#[derive(Debug, PartialEq, Eq)]
enum StateStatus {
    Valid,
    Expired,
    Invalid,
}

/// Checks a presented state against the current request's binding.
///
/// Authenticity is decided **before** expiry, so `Expired` is only ever
/// reported for a state that verifiably came from this key and this
/// exact binding — a tampered expiry fails the MAC and lands in
/// `Invalid`, never in `Expired`. Any structural defect is a
/// verification failure too: a state that cannot be verified is never
/// honored.
fn verify_state(key: &[u8], state: &str, binding: &Binding, now: u64) -> StateStatus {
    let parts: Vec<&str> = state.split('.').collect();
    if parts.len() != 3 || parts[0] != STATE_VERSION {
        return StateStatus::Invalid;
    }
    let Ok(exp) = parts[1].parse::<u64>() else {
        return StateStatus::Invalid;
    };
    let Some(tag) = base64url_decode(parts[2]) else {
        return StateStatus::Invalid;
    };
    if !constant_time_eq(&tag, &state_mac(key, binding, exp)) {
        return StateStatus::Invalid;
    }
    if exp < now {
        return StateStatus::Expired;
    }
    StateStatus::Valid
}

/// Seconds since the Unix epoch.
fn now_secs() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or_default()
}

/// Length-independent comparison, so a tag mismatch leaks no timing
/// information about how many bytes matched.
fn constant_time_eq(a: &[u8], b: &[u8]) -> bool {
    if a.len() != b.len() {
        return false;
    }
    a.iter().zip(b).fold(0u8, |acc, (x, y)| acc | (x ^ y)) == 0
}

/// Lowercase hex.
fn hex(bytes: &[u8]) -> String {
    bytes.iter().map(|b| format!("{b:02x}")).collect()
}

/// Unpadded base64url, matching Go's `base64.RawURLEncoding`.
fn base64url_encode(input: &[u8]) -> String {
    const TABLE: &[u8; 64] = b"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_";
    let mut out = String::with_capacity(input.len().div_ceil(3) * 4);
    for chunk in input.chunks(3) {
        let b0 = u32::from(chunk[0]);
        let b1 = chunk.get(1).copied().map_or(0, u32::from);
        let b2 = chunk.get(2).copied().map_or(0, u32::from);
        let acc = (b0 << 16) | (b1 << 8) | b2;
        out.push(TABLE[(acc >> 18) as usize & 63] as char);
        out.push(TABLE[(acc >> 12) as usize & 63] as char);
        if chunk.len() > 1 {
            out.push(TABLE[(acc >> 6) as usize & 63] as char);
        }
        if chunk.len() > 2 {
            out.push(TABLE[acc as usize & 63] as char);
        }
    }
    out
}

/// Unpadded base64url decode. `None` on any malformed input.
fn base64url_decode(input: &str) -> Option<Vec<u8>> {
    const TABLE: &[u8; 64] = b"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_";
    let bytes = input.as_bytes();
    if bytes.len() % 4 == 1 {
        return None;
    }
    let mut out = Vec::with_capacity(bytes.len() / 4 * 3);
    for chunk in bytes.chunks(4) {
        let mut acc: u32 = 0;
        for &b in chunk {
            acc = (acc << 6) | TABLE.iter().position(|&t| t == b)? as u32;
        }
        // Left-align the partial group so the high bytes land first.
        acc <<= 6 * (4 - chunk.len());
        let group = acc.to_be_bytes();
        out.push(group[1]);
        if chunk.len() > 2 {
            out.push(group[2]);
        }
        if chunk.len() > 3 {
            out.push(group[3]);
        }
    }
    Some(out)
}

// --- SHA-256 / HMAC ----------------------------------------------------
//
// Implemented here rather than pulled from `sha2` so the `mcp` feature
// stays free of the RustCrypto tree: the surface needs exactly one hash
// and one MAC, both on short inputs, and the crate already gates other
// optional dependencies narrowly.

/// SHA-256 over `input`, per FIPS 180-4.
fn sha256(input: &[u8]) -> [u8; 32] {
    const K: [u32; 64] = [
        0x428a_2f98, 0x7137_4491, 0xb5c0_fbcf, 0xe9b5_dba5, 0x3956_c25b, 0x59f1_11f1, 0x923f_82a4,
        0xab1c_5ed5, 0xd807_aa98, 0x1283_5b01, 0x2431_85be, 0x550c_7dc3, 0x72be_5d74, 0x80de_b1fe,
        0x9bdc_06a7, 0xc19b_f174, 0xe49b_69c1, 0xefbe_4786, 0x0fc1_9dc6, 0x240c_a1cc, 0x2de9_2c6f,
        0x4a74_84aa, 0x5cb0_a9dc, 0x76f9_88da, 0x983e_5152, 0xa831_c66d, 0xb003_27c8, 0xbf59_7fc7,
        0xc6e0_0bf3, 0xd5a7_9147, 0x06ca_6351, 0x1429_2967, 0x27b7_0a85, 0x2e1b_2138, 0x4d2c_6dfc,
        0x5338_0d13, 0x650a_7354, 0x766a_0abb, 0x81c2_c92e, 0x9272_2c85, 0xa2bf_e8a1, 0xa81a_664b,
        0xc24b_8b70, 0xc76c_51a3, 0xd192_e819, 0xd699_0624, 0xf40e_3585, 0x106a_a070, 0x19a4_c116,
        0x1e37_6c08, 0x2748_774c, 0x34b0_bcb5, 0x391c_0cb3, 0x4ed8_aa4a, 0x5b9c_ca4f, 0x682e_6ff3,
        0x748f_82ee, 0x78a5_636f, 0x84c8_7814, 0x8cc7_0208, 0x90be_fffa, 0xa450_6ceb, 0xbef9_a3f7,
        0xc671_78f2,
    ];
    let mut h: [u32; 8] = [
        0x6a09_e667,
        0xbb67_ae85,
        0x3c6e_f372,
        0xa54f_f53a,
        0x510e_527f,
        0x9b05_688c,
        0x1f83_d9ab,
        0x5be0_cd19,
    ];

    let mut msg = input.to_vec();
    let bit_len = (input.len() as u64) * 8;
    msg.push(0x80);
    while msg.len() % 64 != 56 {
        msg.push(0);
    }
    msg.extend_from_slice(&bit_len.to_be_bytes());

    for block in msg.chunks(64) {
        let mut w = [0u32; 64];
        for (i, word) in block.chunks(4).enumerate() {
            w[i] = u32::from_be_bytes([word[0], word[1], word[2], word[3]]);
        }
        for i in 16..64 {
            let s0 = w[i - 15].rotate_right(7) ^ w[i - 15].rotate_right(18) ^ (w[i - 15] >> 3);
            let s1 = w[i - 2].rotate_right(17) ^ w[i - 2].rotate_right(19) ^ (w[i - 2] >> 10);
            w[i] = w[i - 16]
                .wrapping_add(s0)
                .wrapping_add(w[i - 7])
                .wrapping_add(s1);
        }

        let [mut a, mut b, mut c, mut d, mut e, mut f, mut g, mut hh] = h;
        for i in 0..64 {
            let s1 = e.rotate_right(6) ^ e.rotate_right(11) ^ e.rotate_right(25);
            let ch = (e & f) ^ ((!e) & g);
            let t1 = hh
                .wrapping_add(s1)
                .wrapping_add(ch)
                .wrapping_add(K[i])
                .wrapping_add(w[i]);
            let s0 = a.rotate_right(2) ^ a.rotate_right(13) ^ a.rotate_right(22);
            let maj = (a & b) ^ (a & c) ^ (b & c);
            let t2 = s0.wrapping_add(maj);

            hh = g;
            g = f;
            f = e;
            e = d.wrapping_add(t1);
            d = c;
            c = b;
            b = a;
            a = t1.wrapping_add(t2);
        }
        for (slot, value) in h.iter_mut().zip([a, b, c, d, e, f, g, hh]) {
            *slot = slot.wrapping_add(value);
        }
    }

    let mut out = [0u8; 32];
    for (i, word) in h.iter().enumerate() {
        out[i * 4..i * 4 + 4].copy_from_slice(&word.to_be_bytes());
    }
    out
}

/// HMAC-SHA-256, per RFC 2104.
fn hmac_sha256(key: &[u8], message: &[u8]) -> [u8; 32] {
    const BLOCK: usize = 64;
    let mut padded = [0u8; BLOCK];
    if key.len() > BLOCK {
        padded[..32].copy_from_slice(&sha256(key));
    } else {
        padded[..key.len()].copy_from_slice(key);
    }

    let mut inner = Vec::with_capacity(BLOCK + message.len());
    inner.extend(padded.iter().map(|b| b ^ 0x36));
    inner.extend_from_slice(message);
    let inner_hash = sha256(&inner);

    let mut outer = Vec::with_capacity(BLOCK + 32);
    outer.extend(padded.iter().map(|b| b ^ 0x5c));
    outer.extend_from_slice(&inner_hash);
    sha256(&outer)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn sha256_matches_known_vectors() {
        assert_eq!(
            hex(&sha256(b"")),
            "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
        );
        assert_eq!(
            hex(&sha256(b"abc")),
            "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
        );
        // Multi-block input, exercising the padding boundary.
        assert_eq!(
            hex(&sha256(
                b"abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq"
            )),
            "248d6a61d20638b8e5c026930c3e6039a33ce45964ff2167f6ecedd419db06c1"
        );
    }

    #[test]
    fn hmac_sha256_matches_rfc4231_vectors() {
        // RFC 4231 test case 1.
        assert_eq!(
            hex(&hmac_sha256(&[0x0b; 20], b"Hi There")),
            "b0344c61d8db38535ca8afceaf0bf12b881dc200c9833da726e9376c2e32cff7"
        );
        // RFC 4231 test case 2.
        assert_eq!(
            hex(&hmac_sha256(b"Jefe", b"what do ya want for nothing?")),
            "5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843"
        );
        // Case 6: key longer than the block size, exercising the
        // hash-the-key branch.
        assert_eq!(
            hex(&hmac_sha256(
                &[0xaa; 131],
                b"Test Using Larger Than Block-Size Key - Hash Key First"
            )),
            "60e431591ee0b67f0d8a26aacbf5b77f8e0bc6213728c5140546040f0ee37f54"
        );
    }

    #[test]
    fn base64url_round_trips_unpadded() {
        for input in [
            &b""[..],
            b"a",
            b"ab",
            b"abc",
            b"abcd",
            b"hello world, longer input",
        ] {
            let encoded = base64url_encode(input);
            assert!(!encoded.contains('='), "must be unpadded: {encoded}");
            assert_eq!(base64url_decode(&encoded).as_deref(), Some(input));
        }
    }

    fn binding() -> Binding {
        Binding {
            tool: "widget purge".into(),
            args_digest: "digest".into(),
            principal: String::new(),
        }
    }

    #[test]
    fn minted_state_verifies_against_its_own_binding() {
        let key = b"secret";
        let state = mint_state(key, &binding(), now_secs() + 60);
        assert_eq!(
            verify_state(key, &state, &binding(), now_secs()),
            StateStatus::Valid
        );
    }

    #[test]
    fn state_is_rejected_for_a_different_binding() {
        let key = b"secret";
        let state = mint_state(key, &binding(), now_secs() + 60);
        let other = Binding {
            tool: "widget delete".into(),
            args_digest: "digest".into(),
            principal: String::new(),
        };
        assert_eq!(
            verify_state(key, &state, &other, now_secs()),
            StateStatus::Invalid,
            "state minted for one leaf must not verify for another"
        );
    }

    #[test]
    fn state_is_rejected_under_a_different_key() {
        let state = mint_state(b"secret", &binding(), now_secs() + 60);
        assert_eq!(
            verify_state(b"other", &state, &binding(), now_secs()),
            StateStatus::Invalid
        );
    }

    #[test]
    fn expired_state_is_distinguished_from_tampered_state() {
        let key = b"secret";
        // Authentic but past its TTL: Expired, a routine re-prompt.
        let expired = mint_state(key, &binding(), 1_000);
        assert_eq!(
            verify_state(key, &expired, &binding(), 2_000),
            StateStatus::Expired
        );

        // A tampered expiry fails the MAC and lands in Invalid, never
        // Expired — that distinction is what keeps forgery from being
        // silently treated as a re-prompt.
        let parts: Vec<&str> = expired.split('.').collect();
        let tampered = format!("{}.{}.{}", parts[0], 9_999_999_999u64, parts[2]);
        assert_eq!(
            verify_state(key, &tampered, &binding(), 2_000),
            StateStatus::Invalid
        );
    }

    #[test]
    fn structurally_malformed_state_is_invalid() {
        let key = b"secret";
        for bad in ["", "v1", "v1.abc.xx", "v2.100.xx", "v1.100.!!!"] {
            assert_eq!(
                verify_state(key, bad, &binding(), 0),
                StateStatus::Invalid,
                "{bad:?} must not verify"
            );
        }
    }

    #[test]
    fn constant_time_eq_rejects_length_and_content_mismatch() {
        assert!(constant_time_eq(b"abc", b"abc"));
        assert!(!constant_time_eq(b"abc", b"abd"));
        assert!(!constant_time_eq(b"abc", b"ab"));
    }
}
