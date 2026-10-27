//! Key resolution and config merge (Go `ResolveAPIKey`, `ApplyAPIKey`,
//! `ProviderKeyFor`, `LoadConfig`): one precedence for every surface.
//!
//! Key, highest first: the URI's `api_key`; llm.yaml `api_key`; the
//! variable `api_key_env` names, then the scheme's key names, each from
//! the store then the environment; `LLM_API_KEY` for a required key
//! only. A blank value is unset at every source.

use std::fmt;

use super::config::{ProviderConfig, ResolvedConfig, Sources};
use super::error::{LlmError, MissingKeyError};
use super::registry::{ProviderKey, Registry};
use super::uri::{is_blank, parse_uri, strip_param, Uri};

/// The universal key variable, consulted last and for required keys only.
pub const FALLBACK_ENV_KEY: &str = "LLM_API_KEY";

/// Where a resolved key came from.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub enum KeySourceKind {
    /// No key was found, or the scheme takes none.
    #[default]
    None,
    Uri,
    Config,
    Store,
    Env,
    Fallback,
}

/// A key's source: its kind and the name that held it (store key or
/// variable, or `providers.<block>.api_key`). Never the key.
#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct KeySource {
    pub kind: KeySourceKind,
    pub name: String,
}

/// The outcome of resolving a URI's key. `Debug` leaves the key out.
#[derive(Clone, PartialEq, Eq, Default)]
pub struct KeyResolution {
    pub scheme: String,
    /// False for a scheme no adapter serves: it gets only what its own
    /// llm.yaml block names.
    pub known: bool,
    pub key: ProviderKey,
    value: String,
    pub source: KeySource,
    /// Secret-store failures met on the way, found or not.
    pub store_err: Option<String>,
}

impl KeyResolution {
    /// The key; empty when none was found. Never log it.
    pub fn value(&self) -> &str {
        &self.value
    }

    pub fn found(&self) -> bool {
        self.source.kind != KeySourceKind::None
    }
}

impl fmt::Debug for KeyResolution {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("KeyResolution")
            .field("scheme", &self.scheme)
            .field("known", &self.known)
            .field("source", &self.source)
            .field("found", &self.found())
            .finish()
    }
}

struct Plan {
    known: bool,
    literal: String,
    block: String,
    key: ProviderKey,
}

/// `<SCHEME>_API_KEY`, non-alphanumerics as `_`.
fn convention_key(scheme: &str) -> String {
    let mut s: String = scheme
        .to_ascii_uppercase()
        .chars()
        .map(|c| if c.is_ascii_alphanumeric() { c } else { '_' })
        .collect();
    s.push_str("_API_KEY");
    s
}

impl Registry {
    fn plan(&self, src: &Sources<'_>, scheme: &str) -> Plan {
        let decl = self.declaration(scheme);
        let mut plan = Plan {
            known: decl.is_some(),
            literal: String::new(),
            block: String::new(),
            key: match decl {
                Some(d) => d.key.unwrap_or_else(|| ProviderKey {
                    env_vars: vec![convention_key(scheme)],
                    optional: false,
                }),
                None => ProviderKey::default(),
            },
        };
        if let Some((name, b)) = src.file.block(&[scheme]) {
            plan.literal = b.api_key.clone();
            plan.block = name.to_string();
            if !is_blank(&b.api_key_env) {
                let mut vars = vec![b.api_key_env.clone()];
                vars.extend(
                    plan.key
                        .env_vars
                        .iter()
                        .filter(|v| **v != b.api_key_env)
                        .cloned(),
                );
                plan.key.env_vars = vars;
            }
        }
        plan
    }

    fn resolve_key(&self, src: &Sources<'_>, uri: &Uri) -> Result<KeyResolution, LlmError> {
        let plan = self.plan(src, &uri.scheme);
        let mut res = KeyResolution {
            scheme: uri.scheme.clone(),
            known: plan.known,
            key: plan.key.clone(),
            ..KeyResolution::default()
        };
        if let Some(v) = uri.key() {
            res.value = v.to_string();
            res.source.kind = KeySourceKind::Uri;
            return Ok(res);
        }
        if !is_blank(&plan.literal) {
            res.value = plan.literal;
            res.source = KeySource {
                kind: KeySourceKind::Config,
                name: format!("providers.{}.api_key", plan.block),
            };
            return Ok(res);
        }
        let names = &plan.key.env_vars;
        if names.is_empty() {
            return Ok(res);
        }
        let required = plan.known && !plan.key.optional;
        let mut failed = Vec::new();
        let found = lookup(src, names, required, &mut failed);
        if !failed.is_empty() {
            res.store_err = Some(format!("secret store: {}", failed.join("; ")));
        }
        match found {
            Some((value, source)) => {
                res.value = value;
                res.source = source;
                Ok(res)
            }
            None if required => {
                let mut env_vars = names.clone();
                env_vars.push(FALLBACK_ENV_KEY.to_string());
                Err(MissingKeyError {
                    scheme: uri.scheme.clone(),
                    model: uri.model.clone(),
                    env_vars,
                    store_err: res.store_err,
                }
                .into())
            }
            None => Ok(res),
        }
    }

    /// The key a request to `uri` would carry and where it came from.
    /// A required key found nowhere is a [`LlmError::MissingKey`].
    pub fn resolve_api_key(&self, src: &Sources<'_>, uri: &str) -> Result<KeyResolution, LlmError> {
        let parsed = parse_uri(uri).map_err(|_| {
            LlmError::invalid("llm: resolve API key: provider URI must be scheme://model")
        })?;
        self.resolve_key(src, &parsed)
    }

    /// `uri` with its key set as the `api_key` param, where
    /// [`Registry::resolve`] reads it. A non-blank `api_key` already
    /// there wins; a blank one is removed first. The result holds the
    /// key: never log it.
    pub fn apply_api_key(&self, src: &Sources<'_>, uri: &str) -> Result<String, LlmError> {
        let parsed = parse_uri(uri).map_err(|_| {
            LlmError::invalid("llm: apply API key: provider URI must be scheme://model")
        })?;
        let mut out = uri.to_string();
        if parsed.params.get("api_key").is_some_and(|v| is_blank(v)) {
            out = strip_param(&out, "api_key");
        }
        let res = self.resolve_key(src, &parsed)?;
        self.guard_key_endpoint(src, &parsed, res.source.kind, &res.value)?;
        if res.source.kind == KeySourceKind::Uri || res.value.is_empty() {
            return Ok(out);
        }
        if res.value.contains(['&', '?', '#']) {
            return Err(LlmError::invalid(format!(
                "llm: {} API key contains a character a provider URI cannot carry (& ? #)",
                parsed.scheme
            )));
        }
        let sep = if out.contains('?') { '&' } else { '?' };
        Ok(format!("{out}{sep}api_key={}", res.value))
    }

    /// The credential `uri`'s scheme takes, and whether an adapter
    /// serves it. `uri` may be a bare scheme.
    pub fn provider_key_for(&self, src: &Sources<'_>, uri: &str) -> (ProviderKey, bool) {
        let plan = self.plan(src, super::uri::scheme_of(uri));
        (plan.key, plan.known)
    }

    /// Merge llm.yaml, the environment and `uri`, the URI highest.
    ///
    /// URI: `uri`, else `LLM_PROVIDER`, else llm.yaml `default`. Key: as
    /// [`Registry::resolve_api_key`]; none found leaves it empty. Base
    /// URL: `base_url` param, the URI's host, `LLM_BASE_URL`, the block's.
    /// Model: the URI's, else the block's. Fallbacks: `LLM_FALLBACK`
    /// (comma-separated), else llm.yaml `fallback`.
    pub fn load_config(&self, src: &Sources<'_>, uri: &str) -> Result<ResolvedConfig, LlmError> {
        let effective = if !uri.is_empty() {
            uri.to_string()
        } else if let Some(p) = src.env("LLM_PROVIDER") {
            p
        } else if !src.file.default.is_empty() {
            src.file.default.clone()
        } else {
            return Err(LlmError::invalid(
                "no URI provided and no default configured",
            ));
        };
        let mut parsed = parse_uri(&effective)?;
        parsed.drop_blank_key();

        let mut pc = ProviderConfig::default();
        if let Some((_, b)) = src.file.block(&[parsed.scheme.as_str()]) {
            pc.base_url = b.base_url.clone();
            pc.model = b.model.clone();
            pc.extras = b.extras.clone();
        }
        if let Ok(key) = self.resolve_key(src, &parsed) {
            self.guard_key_endpoint(src, &parsed, key.source.kind, &key.value)?;
            pc.api_key = key.value;
        }
        if !parsed.model.is_empty() {
            pc.model = parsed.model.clone();
        }
        if let Some(b) = src.env("LLM_BASE_URL") {
            pc.base_url = b;
        }
        if !parsed.host.is_empty() {
            pc.base_url = format!("http://{}", parsed.host);
        }
        if let Some(b) = parsed.params.get("base_url") {
            pc.base_url = b.clone();
        }
        pc.params = parsed.params.clone();
        super::endpoint::check_key_endpoint(&parsed.scheme, &pc.api_key, &pc.base_url)?;

        let fallbacks = match src.env("LLM_FALLBACK") {
            Some(list) => list
                .split(',')
                .map(str::trim)
                .filter(|s| !s.is_empty())
                .map(String::from)
                .collect(),
            None => src.file.fallback.clone(),
        };
        Ok(ResolvedConfig {
            uri: parsed,
            provider: pc,
            fallbacks,
            net_policy: self.policy(),
        })
    }
}

/// Store then environment for each name, then `LLM_API_KEY` when
/// `universal`. A store failure counts as the name being absent; it is
/// pushed onto `failed`.
fn lookup(
    src: &Sources<'_>,
    names: &[String],
    universal: bool,
    failed: &mut Vec<String>,
) -> Option<(String, KeySource)> {
    let source = |kind, name: &str| KeySource {
        kind,
        name: name.to_string(),
    };
    if let Some(store) = src.store {
        for name in names {
            match store.get(name) {
                Ok(Some(v)) if !is_blank(&v) => {
                    return Some((v, source(KeySourceKind::Store, name)));
                }
                Ok(_) => {}
                Err(e) => failed.push(format!("{name}: {e}")),
            }
        }
    }
    for name in names {
        if let Some(v) = (src.env)(name).filter(|v| !is_blank(v)) {
            return Some((v, source(KeySourceKind::Env, name)));
        }
    }
    if universal {
        if let Some(v) = (src.env)(FALLBACK_ENV_KEY).filter(|v| !is_blank(v)) {
            return Some((v, source(KeySourceKind::Fallback, FALLBACK_ENV_KEY)));
        }
    }
    None
}
