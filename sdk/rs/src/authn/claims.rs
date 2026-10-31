use serde::{Deserialize, Serialize};
use serde_json::Value;

/// The caller a verified credential names: principal, tenant and the
/// scopes it carries. The value an [`AuthFunc`](super::AuthFunc) hands
/// the transports; field names follow JWT registered-claim spelling
/// where one exists.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct Claims {
    /// The principal (`sub`).
    #[serde(rename = "sub")]
    pub subject: String,
    /// The tenant, empty for single-tenant tools.
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub tenant: String,
    /// The entitlements the credential carries.
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub scopes: Vec<String>,
}

/// Attributes a call to a principal and tenant without naming the
/// claims type.
pub trait Identity {
    /// The stable identifier of the authenticated caller: a user id, a
    /// service account, a client id.
    fn principal(&self) -> &str;
    /// The tenant the principal acts within, empty for single-tenant
    /// tools.
    fn tenant_id(&self) -> &str;
}

impl Identity for Claims {
    fn principal(&self) -> &str {
        &self.subject
    }
    fn tenant_id(&self) -> &str {
        &self.tenant
    }
}

/// The principal and tenant a decoded claims object carries: its `sub`
/// and `tenant` strings. Anything but an object yields two empty
/// strings.
pub fn identity_of(claims: &Value) -> (String, String) {
    (
        string_entry(claims, "sub").to_string(),
        string_entry(claims, "tenant").to_string(),
    )
}

fn string_entry<'a>(claims: &'a Value, key: &str) -> &'a str {
    claims.get(key).and_then(Value::as_str).unwrap_or("")
}

/// The scopes a decoded claims object carries: the first of these
/// entries present.
///
/// - `scopes`: a list of strings (kit's own tokens), or a single
///   string, one scope;
/// - `scope`: a space-delimited string (RFC 8693, RFC 9068, what OAuth
///   2.0 issuers mint);
/// - `scp`: a list of strings, or a space-delimited string (Azure AD
///   and Okta spell it so).
///
/// Empty when there are none. Non-string list entries are skipped.
pub fn scopes_of(claims: &Value) -> Vec<String> {
    if let Some(v) = claims.get("scopes") {
        return scope_list(v, false);
    }
    for key in ["scope", "scp"] {
        if let Some(v) = claims.get(key) {
            return scope_list(v, true);
        }
    }
    Vec::new()
}

fn scope_list(v: &Value, delimited: bool) -> Vec<String> {
    match v {
        Value::Array(items) => items
            .iter()
            .filter_map(|e| e.as_str().map(str::to_string))
            .collect(),
        Value::String(s) if delimited => s.split_whitespace().map(str::to_string).collect(),
        Value::String(s) => vec![s.clone()],
        _ => Vec::new(),
    }
}
