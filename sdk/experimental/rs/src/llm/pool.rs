//! The operator-approved model pool of llm.yaml (Go `PoolEntry`,
//! `LoadPool`, `ResolvePool`): which `scheme:model` pairs a picker may
//! choose from, with `LLM_POOL_DISABLE` and caller disables layered on.
//! Pure; wasm-safe apart from reading the file.

use super::config::ConfigFile;

/// A resolved pool member after defaults and disable overrides.
/// `scheme` and `model` identify the model in the catalog.
#[derive(Debug, Clone, PartialEq, Default)]
pub struct PoolEntry {
    pub alias: String,
    pub scheme: String,
    pub model: String,
    pub enabled: bool,
    pub weight: f64,
}

impl PoolEntry {
    /// An enabled entry of weight 1.
    pub fn new(scheme: impl Into<String>, model: impl Into<String>) -> Self {
        PoolEntry {
            alias: String::new(),
            scheme: scheme.into(),
            model: model.into(),
            enabled: true,
            weight: 1.0,
        }
    }
}

/// The pool of `file`'s `pool:` block with defaults (enabled unless
/// `enabled: false`, weight 1.0 when 0) and the `LLM_POOL_DISABLE`
/// override from `env`. An empty pool is `None` for "accept everything",
/// never "deny everything".
pub fn load_pool(
    file: &ConfigFile,
    env: &dyn Fn(&str) -> Option<String>,
) -> Option<Vec<PoolEntry>> {
    if file.pool.is_empty() {
        return None;
    }
    let entries = file.pool.clone();
    Some(match env("LLM_POOL_DISABLE").filter(|v| !v.is_empty()) {
        Some(v) => resolve_pool(&entries, &split_csv(&v)),
        None => entries,
    })
}

/// A copy of `entries` with `disable` applied: an entry goes when its
/// alias or its `<scheme>:<model>` form is listed (case-sensitive,
/// exact).
pub fn resolve_pool(entries: &[PoolEntry], disable: &[String]) -> Vec<PoolEntry> {
    let mut out = entries.to_vec();
    let disable: Vec<&str> = disable
        .iter()
        .map(|d| d.trim())
        .filter(|d| !d.is_empty())
        .collect();
    if disable.is_empty() {
        return out;
    }
    for e in &mut out {
        let key = format!("{}:{}", e.scheme, e.model);
        if (!e.alias.is_empty() && disable.contains(&e.alias.as_str()))
            || disable.contains(&key.as_str())
        {
            e.enabled = false;
        }
    }
    out
}

fn split_csv(s: &str) -> Vec<String> {
    s.split(',')
        .map(str::trim)
        .filter(|t| !t.is_empty())
        .map(str::to_string)
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn file(body: &str) -> ConfigFile {
        ConfigFile::parse(body).unwrap()
    }

    const POOL: &str = "pool:
  - alias: fast
    scheme: openai
    model: gpt-4o-mini
  - alias: smart
    scheme: anthropic
    model: claude-sonnet-4-5
    weight: 2.5
  - alias: off
    scheme: openai
    model: gpt-3.5-turbo
    enabled: false
";

    #[test]
    fn basic_fixture_applies_defaults_in_order() {
        let got = load_pool(&file(POOL), &|_| None).unwrap();
        assert_eq!(got.len(), 3);
        assert_eq!(
            (
                got[0].alias.as_str(),
                got[0].scheme.as_str(),
                got[0].model.as_str()
            ),
            ("fast", "openai", "gpt-4o-mini")
        );
        assert!(got[0].enabled && got[0].weight == 1.0);
        assert!(got[1].enabled && got[1].weight == 2.5);
        assert!(!got[2].enabled && got[2].weight == 1.0);
    }

    #[test]
    fn no_pool_block_is_none() {
        let f = file("default: openai://gpt-4o\nproviders:\n  openai:\n    api_key: x\n");
        assert_eq!(load_pool(&f, &|_| None), None);
    }

    #[test]
    fn env_disable_by_alias_or_scheme_model() {
        let env = |k: &str| {
            (k == "LLM_POOL_DISABLE").then(|| "fast, anthropic:claude-sonnet-4-5".to_string())
        };
        let got = load_pool(&file(POOL), &env).unwrap();
        assert_eq!(
            got.iter().map(|e| e.enabled).collect::<Vec<_>>(),
            [false, false, false]
        );
        let env = |k: &str| (k == "LLM_POOL_DISABLE").then(|| "smart".to_string());
        let got = load_pool(&file(POOL), &env).unwrap();
        assert_eq!(
            got.iter().map(|e| e.enabled).collect::<Vec<_>>(),
            [true, false, false]
        );
    }

    #[test]
    fn resolve_pool_is_a_copy_and_matches_exactly() {
        let e = vec![
            PoolEntry {
                alias: "fast".into(),
                ..PoolEntry::new("openai", "gpt-4o-mini")
            },
            PoolEntry::new("anthropic", "claude"),
        ];
        assert!(resolve_pool(&[], &["x".into()]).is_empty());
        assert_eq!(resolve_pool(&e, &[]), e);
        let got = resolve_pool(&e, &[" fast ".into(), "".into(), "Anthropic:claude".into()]);
        assert!(!got[0].enabled && got[1].enabled, "case-sensitive, trimmed");
        assert!(e[0].enabled, "the input is untouched");
    }
}
