//! [`resolve`]: evaluate a PKL module with answers, via the `pkl`
//! binary. Ports Go `pkl/eval.go` (which drives the same binary through
//! pkl-go).

use std::io::Write;
use std::path::Path;
use std::process::Command;

use super::fmt_num;
use crate::config::goquote::quote;
use crate::config::{Error, ErrorKind, Map, Value};

fn pkl_err(msg: String) -> Error {
    Error::new(ErrorKind::Invalid, msg)
}

/// Evaluates the module at `pkl_path` amended with `answers` and
/// returns the full config, computed fields included.
///
/// Requires `pkl` on `PATH`; the module must end with
/// `output { renderer = new JsonRenderer {} }`.
pub fn resolve(pkl_path: impl AsRef<Path>, answers: &Map) -> Result<Map, Error> {
    let abs = std::path::absolute(pkl_path.as_ref())
        .map_err(|e| pkl_err(format!("pkl: resolve path: {e}")))?;
    let amendment = generate_amendment(&abs.display().to_string(), answers)
        .map_err(|e| pkl_err(format!("pkl: generate amendment: {e}")))?;
    let dir = abs.parent().unwrap_or(Path::new("."));
    let tmp = TempFile::create(dir).map_err(|e| pkl_err(format!("pkl: create temp: {e}")))?;
    std::fs::File::options()
        .write(true)
        .open(&tmp.0)
        .and_then(|mut f| f.write_all(amendment.as_bytes()))
        .map_err(|e| pkl_err(format!("pkl: write amendment: {e}")))?;
    let out = Command::new("pkl")
        .arg("eval")
        .arg(&tmp.0)
        .output()
        .map_err(|e| pkl_err(format!("pkl: create evaluator: {e}")))?;
    if !out.status.success() {
        return Err(pkl_err(format!(
            "pkl: evaluate: {}",
            String::from_utf8_lossy(&out.stderr).trim()
        )));
    }
    let json: serde_json::Value =
        serde_json::from_slice(&out.stdout).map_err(|e| pkl_err(format!("pkl: unmarshal: {e}")))?;
    match from_json(json) {
        Value::Map(m) => Ok(m),
        other => Err(pkl_err(format!(
            "pkl: unmarshal: json: cannot unmarshal {} into Go value of type map[string]interface {{}}",
            crate::config::overrides::go_type_name(&other)
        ))),
    }
}

fn from_json(v: serde_json::Value) -> Value {
    match v {
        serde_json::Value::Null => Value::Null,
        serde_json::Value::Bool(b) => Value::Bool(b),
        serde_json::Value::Number(n) => n
            .as_i64()
            .map(Value::Int)
            .or_else(|| n.as_u64().map(Value::Uint))
            .unwrap_or_else(|| Value::Float(n.as_f64().unwrap_or(f64::NAN))),
        serde_json::Value::String(s) => Value::String(s),
        serde_json::Value::Array(a) => Value::Seq(a.into_iter().map(from_json).collect()),
        serde_json::Value::Object(o) => {
            Value::Map(o.into_iter().map(|(k, v)| (k, from_json(v))).collect())
        }
    }
}

/// `pkl-amend-*.pkl` next to the module, removed on drop.
struct TempFile(std::path::PathBuf);

impl TempFile {
    fn create(dir: &Path) -> std::io::Result<TempFile> {
        let nanos = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .map_or(0, |d| d.as_nanos());
        for n in 0..1000u32 {
            let p = dir.join(format!("pkl-amend-{}-{nanos}-{n}.pkl", std::process::id()));
            match std::fs::OpenOptions::new()
                .write(true)
                .create_new(true)
                .open(&p)
            {
                Ok(_) => return Ok(TempFile(p)),
                Err(e) if e.kind() == std::io::ErrorKind::AlreadyExists => continue,
                Err(e) => return Err(e),
            }
        }
        Err(std::io::Error::other("no free temp name"))
    }
}

impl Drop for TempFile {
    fn drop(&mut self) {
        let _ = std::fs::remove_file(&self.0);
    }
}

/// A value as PKL source; `None` for unsupported values (null, maps,
/// lists of non-scalars).
pub(crate) fn to_pkl_literal(v: &Value) -> Option<String> {
    Some(match v {
        Value::String(s) => quote(s),
        Value::Bool(b) => b.to_string(),
        Value::Int(i) => i.to_string(),
        Value::Uint(u) => u.to_string(),
        Value::Float(f) => fmt_num(*f),
        Value::Seq(items) if items.is_empty() => "new Listing {}".to_string(),
        Value::Seq(items) => {
            let lits: Option<Vec<String>> = items
                .iter()
                .map(|i| to_pkl_literal(i).map(|l| format!("  {l}")))
                .collect();
            format!("new Listing {{\n{}\n}}", lits?.join("\n"))
        }
        Value::Null | Value::Map(_) => return None,
    })
}

/// The amendment module overriding answered fields of `pkl_path`.
pub(crate) fn generate_amendment(pkl_path: &str, answers: &Map) -> Result<String, String> {
    let mut out = format!("amends \"{pkl_path}\"\n\n");
    for (k, v) in answers {
        let lit =
            to_pkl_literal(v).ok_or_else(|| format!("unsupported value type for {}", quote(k)))?;
        out.push_str(&format!("{k} = {lit}\n"));
    }
    Ok(out)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn pkl_literals_and_amendment() {
        assert_eq!(to_pkl_literal(&"hello".into()).unwrap(), "\"hello\"");
        assert_eq!(to_pkl_literal(&Value::Bool(false)).unwrap(), "false");
        assert_eq!(to_pkl_literal(&Value::Int(42)).unwrap(), "42");
        assert_eq!(to_pkl_literal(&Value::Float(2.5)).unwrap(), "2.5");
        let list = to_pkl_literal(&Value::Seq(vec!["a".into(), "b".into()])).unwrap();
        assert_eq!(list, "new Listing {\n  \"a\"\n  \"b\"\n}");
        assert_eq!(
            to_pkl_literal(&Value::Seq(vec![])).unwrap(),
            "new Listing {}"
        );
        assert_eq!(to_pkl_literal(&Value::Map(Map::new())), None);
        let mut answers = Map::new();
        answers.insert("name".into(), "app".into());
        answers.insert("port".into(), Value::Int(8080));
        let got = generate_amendment("/x/config.pkl", &answers).unwrap();
        assert!(got.starts_with("amends \"/x/config.pkl\""));
        assert!(got.contains("name = \"app\"") && got.contains("port = 8080"));
    }
}
