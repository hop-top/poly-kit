//! The per-tool grants file, with the legacy telemetry records.

use std::fs;
use std::io;
use std::path::{Path, PathBuf};
use std::sync::{Mutex, MutexGuard};

use crate::config::yaml::Node;

use super::error::{Error, GrantError};
use super::grant::{Grant, Key, CAP_TELEMETRY};
use super::paths::{state_file, write_file_atomic, GRANTS_FILE};
use super::store::GrantStore;
use super::telemetry::DENIED;
use super::telemetry::{deny_wins, TelemetryDecision};
use super::yaml_doc::{
    consent_block, consent_node, decode_consent, decode_grants, edit_or_render, encode_grants,
    only_consent_path, parse_doc, remove_consent, render, replace_legacy_consent, splice_remove,
    splice_set, upsert_consent,
};

/// The [`GrantStore`] over `<XDG_STATE_HOME>/<tool>/consent/grants.yaml`.
/// config.yaml is never read for other capabilities, so a typo there
/// cannot refuse one, and a grant write cannot clobber it.
///
/// Telemetry compat: the telemetry record is the grants file entry,
/// `kit.telemetry.consent` in `<XDG_CONFIG_HOME>/kit/config.yaml` and
/// bare `telemetry.consent` in `kit/telemetry.yaml`, combined
/// deny-wins; any source that cannot be read fails the check closed. A
/// telemetry write lands in the grants file and is mirrored into
/// config.yaml (and telemetry.yaml when it holds a record); revoking
/// telemetry clears all three. Edits of config.yaml and telemetry.yaml
/// keep every other key and the comments, as Go's yaml.v3 Node
/// round-trip does: only the consent block is spliced, and a file the
/// splicer cannot edit in place is re-emitted whole.
pub struct FileStore {
    grants_path: PathBuf,
    config_path: PathBuf,
    legacy_path: PathBuf,
    /// Where kit-rs telemetry read before it resolved through `xdg`
    /// (`~/.config/kit/{config,telemetry}.yaml`, macOS and Windows):
    /// deny-only, cleared by every telemetry write and revoke.
    old: Option<(PathBuf, PathBuf)>,
    mu: Mutex<()>,
}

fn io_err(what: &str, path: &Path, e: io::Error) -> Error {
    Error::Io(format!("consent: {what} {}: {e}", path.display()))
}

/// Why a legacy file could not be used: it does not parse (left alone
/// on a write), or anything else.
enum DocError {
    Parse(String),
    Other(Error),
}

impl From<DocError> for Error {
    fn from(e: DocError) -> Self {
        match e {
            DocError::Parse(m) => Error::Io(m),
            DocError::Other(e) => e,
        }
    }
}

impl From<Error> for DocError {
    fn from(e: Error) -> Self {
        DocError::Other(e)
    }
}

/// Reads a legacy file: `Ok(None)` when it is absent or empty.
fn read_doc(path: &Path) -> Result<Option<Node>, DocError> {
    Ok(read_text_doc(path)?.map(|(_, n)| n))
}

/// A legacy file's text and document.
fn read_text_doc(path: &Path) -> Result<Option<(String, Node)>, DocError> {
    match fs::read_to_string(path) {
        Ok(s) => Ok(parse_doc(&s)
            .map_err(|e| DocError::Parse(format!("consent: parse {}: {e}", path.display())))?
            .map(|n| (s, n))),
        Err(e) if e.kind() == io::ErrorKind::NotFound => Ok(None),
        Err(e) => Err(DocError::Other(io_err("read", path, e))),
    }
}

fn as_store_error(e: Error) -> Error {
    match e {
        Error::Grant(_) => e,
        other => Error::Grant(GrantError::malformed(format!("telemetry consent: {other}"))),
    }
}

impl FileStore {
    /// The store for `tool`: grants at
    /// `<XDG_STATE_HOME>/<tool>/consent/grants.yaml`, the legacy
    /// telemetry records under `<XDG_CONFIG_HOME>/kit/`.
    pub fn for_tool(tool: &str) -> Result<Self, Error> {
        let kit = crate::xdg::raw_config_dir("kit");
        let mut s = Self::at(
            state_file(tool, GRANTS_FILE)?,
            kit.join("config.yaml"),
            kit.join("telemetry.yaml"),
        );
        s.old = crate::xdg::legacy_config_home()
            .map(|h| h.join("kit"))
            .map(|k| (k.join("config.yaml"), k.join("telemetry.yaml")));
        Ok(s)
    }

    /// The store over explicit paths: the grants file, config.yaml and
    /// the legacy telemetry.yaml.
    pub fn at(
        grants: impl Into<PathBuf>,
        config: impl Into<PathBuf>,
        legacy: impl Into<PathBuf>,
    ) -> Self {
        FileStore {
            grants_path: grants.into(),
            config_path: config.into(),
            legacy_path: legacy.into(),
            old: None,
            mu: Mutex::new(()),
        }
    }

    pub fn grants_path(&self) -> &Path {
        &self.grants_path
    }

    pub fn config_path(&self) -> &Path {
        &self.config_path
    }

    pub fn legacy_path(&self) -> &Path {
        &self.legacy_path
    }

    fn lock(&self) -> MutexGuard<'_, ()> {
        self.mu.lock().unwrap_or_else(|e| e.into_inner())
    }

    /// Decodes the grants file; absent holds none, any read or parse
    /// failure is `malformed_store`.
    fn read_grants(&self) -> Result<Vec<Grant>, Error> {
        let src = match fs::read_to_string(&self.grants_path) {
            Ok(s) => s,
            Err(e)
                if matches!(
                    e.kind(),
                    io::ErrorKind::NotFound | io::ErrorKind::NotADirectory
                ) =>
            {
                return Ok(Vec::new())
            }
            Err(e) => return Err(GrantError::malformed(e.to_string()).into()),
        };
        let doc = parse_doc(&src)
            .map_err(|e| GrantError::malformed(format!("{}: {e}", self.grants_path.display())))?;
        Ok(decode_grants(doc.as_ref())?)
    }

    fn write_grants(&self, gs: Vec<Grant>) -> Result<(), Error> {
        write_file_atomic(&self.grants_path, render(&encode_grants(gs)).as_bytes())
    }

    fn put_locked(&self, g: &Grant) -> Result<(), Error> {
        let gs = self.read_grants()?;
        let mut kept = remove_key(gs, &g.key());
        kept.push(g.clone());
        self.write_grants(kept)
    }

    fn read_legacy(&self, path: &Path, canonical: bool) -> Result<TelemetryDecision, Error> {
        match read_doc(path)? {
            None => Ok(TelemetryDecision::unknown()),
            Some(doc) => match consent_block(&doc, canonical) {
                None => Ok(TelemetryDecision::unknown()),
                Some(c) => decode_consent(c).map_err(Error::Io),
            },
        }
    }

    /// The telemetry decision: grants file and both legacy records,
    /// deny-wins. Any unreadable source is an error.
    fn telemetry_locked(&self) -> Result<TelemetryDecision, Error> {
        let own = self
            .read_grants()?
            .iter()
            .rev()
            .find(|g| g.capability == CAP_TELEMETRY)
            .map(TelemetryDecision::from_grant)
            .unwrap_or_else(TelemetryDecision::unknown);
        let cfg = self.read_legacy(&self.config_path, true)?;
        let old = self.read_legacy(&self.legacy_path, false)?;
        let mut all = vec![own, deny_wins(&[cfg, old])];
        for (path, canonical) in self.old_paths() {
            let d = self.read_legacy(&path, canonical)?;
            if d.state == DENIED {
                all.push(d);
            }
        }
        Ok(deny_wins(&all))
    }

    fn old_paths(&self) -> Vec<(PathBuf, bool)> {
        self.old
            .iter()
            .flat_map(|(c, l)| [(c.clone(), true), (l.clone(), false)])
            .collect()
    }

    /// Removes the old-location records: a file that held nothing else
    /// is deleted, any other is spliced like the current records.
    fn clear_old_locked(&self) -> Result<(), Error> {
        for (path, canonical) in self.old_paths() {
            let Some((src, mut doc)) = read_text_doc(&path)? else {
                continue;
            };
            if consent_block(&doc, canonical).is_none() {
                continue;
            }
            remove_consent(&mut doc, canonical);
            if only_consent_path(&doc, canonical) {
                fs::remove_file(&path).map_err(|e| io_err("remove", &path, e))?;
                continue;
            }
            let (key, parent) = if canonical {
                ("kit.telemetry.consent", "kit.telemetry")
            } else {
                ("telemetry.consent", "telemetry")
            };
            let text = edit_or_render(Some(&src), &doc, &|s| splice_remove(s, key, parent));
            write_file_atomic(&path, text.as_bytes())?;
        }
        Ok(())
    }

    /// The combined telemetry decision (Go `FileStore.Get`).
    pub fn telemetry_decision(&self) -> Result<TelemetryDecision, Error> {
        let _g = self.lock();
        self.telemetry_locked()
    }

    /// The grants file, then a mirror into the legacy records. A legacy
    /// file that does not parse is left alone.
    fn set_telemetry_locked(&self, g: &Grant) -> Result<(), Error> {
        self.put_locked(g)?;
        match self.mirror_legacy(&TelemetryDecision::from_grant(g)) {
            Err(DocError::Other(e)) => Err(e),
            _ => self.clear_old_locked(),
        }
    }

    fn mirror_legacy(&self, d: &TelemetryDecision) -> Result<(), DocError> {
        let (src, mut doc) = match read_text_doc(&self.config_path)? {
            Some((s, n)) => (Some(s), n),
            None => (None, Node::map(Vec::new())),
        };
        upsert_consent(&mut doc, d).map_err(Error::Io)?;
        let node = consent_node(d);
        let text = edit_or_render(src.as_deref(), &doc, &|s| {
            splice_set(s, "kit.telemetry.consent", &node)
        });
        write_file_atomic(&self.config_path, text.as_bytes())?;
        // A record left in telemetry.yaml would deny forever under
        // deny-wins; rewrite it so a re-grant converges.
        let Some((src, mut legacy)) = read_text_doc(&self.legacy_path)? else {
            return Ok(());
        };
        if consent_block(&legacy, false).is_none() {
            return Ok(());
        }
        replace_legacy_consent(&mut legacy, d);
        let text = edit_or_render(Some(&src), &legacy, &|s| {
            splice_set(s, "telemetry.consent", &node)
        });
        Ok(write_file_atomic(&self.legacy_path, text.as_bytes())?)
    }

    /// Removes telemetry from the grants file and both legacy files. A
    /// malformed grants file fails before anything is touched.
    fn clear_telemetry_locked(&self) -> Result<(), Error> {
        let gs = self.read_grants()?;
        if gs.iter().any(|g| g.capability == CAP_TELEMETRY) {
            self.write_grants(remove_key(gs, &(CAP_TELEMETRY.to_string(), String::new())))?;
        }
        if let Some((src, mut doc)) = read_text_doc(&self.config_path)? {
            remove_consent(&mut doc, true);
            let text = edit_or_render(Some(&src), &doc, &|s| {
                splice_remove(s, "kit.telemetry.consent", "kit.telemetry")
            });
            write_file_atomic(&self.config_path, text.as_bytes())?;
        }
        self.clear_old_locked()?;
        let Some((src, mut legacy)) = read_text_doc(&self.legacy_path)? else {
            return Ok(());
        };
        if consent_block(&legacy, false).is_none() {
            return Ok(());
        }
        remove_consent(&mut legacy, false);
        let text = edit_or_render(Some(&src), &legacy, &|s| {
            splice_remove(s, "telemetry.consent", "telemetry")
        });
        write_file_atomic(&self.legacy_path, text.as_bytes())
    }
}

fn remove_key(gs: Vec<Grant>, k: &Key) -> Vec<Grant> {
    gs.into_iter().filter(|g| &g.key() != k).collect()
}

impl GrantStore for FileStore {
    /// Every grant in the grants file, the telemetry grant being the
    /// deny-wins combination with the legacy records. An unreadable
    /// legacy record leaves telemetry out here; a telemetry check
    /// reports it.
    fn grants(&self) -> Result<Vec<Grant>, Error> {
        let _g = self.lock();
        let tele = (CAP_TELEMETRY.to_string(), String::new());
        let mut gs = remove_key(self.read_grants()?, &tele);
        if let Some(tg) = self.telemetry_locked().ok().and_then(|d| d.grant()) {
            gs.push(tg);
        }
        super::grant::sort_grants(&mut gs);
        Ok(gs)
    }

    fn put_grant(&self, g: &Grant) -> Result<(), Error> {
        g.check_persistable()?;
        let _l = self.lock();
        if g.capability == CAP_TELEMETRY {
            return self.set_telemetry_locked(g);
        }
        self.put_locked(g)
    }

    /// Deleting telemetry clears the grants file entry and both legacy
    /// records.
    fn delete_grant(&self, capability: &str, subject: &str) -> Result<bool, Error> {
        let _l = self.lock();
        if capability == CAP_TELEMETRY {
            let existed = self
                .telemetry_locked()
                .map_err(as_store_error)?
                .grant()
                .is_some();
            self.clear_telemetry_locked()?;
            return Ok(existed);
        }
        let gs = self.read_grants()?;
        let n = gs.len();
        let kept = remove_key(gs, &(capability.to_string(), subject.to_string()));
        if kept.len() == n {
            return Ok(false);
        }
        self.write_grants(kept)?;
        Ok(true)
    }

    fn telemetry(&self) -> Option<Result<TelemetryDecision, Error>> {
        Some(self.telemetry_decision().map_err(as_store_error))
    }
}
