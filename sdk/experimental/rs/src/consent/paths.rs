//! Per-tool state under `<XDG_STATE_HOME>/<tool>/consent/`, atomic
//! writes, and `open`.

use std::fs;
use std::io::Write;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Arc;

use super::audit::Publisher;
use super::builder::GrantsBuilder;
use super::error::Error;
use super::file_audit::FileAuditor;
use super::file_store::FileStore;
use super::grants::Grants;
use super::relay::Relay;

pub(crate) const STATE_DIR: &str = "consent";
pub(crate) const GRANTS_FILE: &str = "grants.yaml";
pub(crate) const AUDIT_FILE: &str = "audit.jsonl";
pub(crate) const CURSOR_FILE: &str = "audit.cursor";

#[cfg(unix)]
const FILE_MODE: u32 = 0o600;
#[cfg(unix)]
const DIR_MODE: u32 = 0o700;

/// The tool a bare [`open`] uses: `KIT_APP_PREFIX` (the telemetry app
/// prefix), lowercased, else `kit`.
pub fn default_tool() -> String {
    let p = std::env::var("KIT_APP_PREFIX")
        .unwrap_or_default()
        .trim()
        .to_lowercase();
    if validate_tool(&p).is_ok() {
        p
    } else {
        "kit".into()
    }
}

fn validate_tool(tool: &str) -> Result<(), Error> {
    if tool.is_empty()
        || tool == "."
        || tool == ".."
        || tool.contains(['/', '\\'])
        || tool.trim() != tool
    {
        return Err(Error::Io(format!("consent: invalid tool name {tool:?}")));
    }
    Ok(())
}

/// `<XDG_STATE_HOME>/<tool>/consent/<name>`. Nothing is created here;
/// writes create the 0700 directory on demand.
pub fn state_file(tool: &str, name: &str) -> Result<PathBuf, Error> {
    validate_tool(tool)?;
    Ok(crate::xdg::raw_state_dir(tool).join(STATE_DIR).join(name))
}

/// Creates `dir` (0700); the consent state dir is forced to 0700 even
/// when something else created it first.
pub(crate) fn ensure_dir(dir: &Path) -> Result<(), Error> {
    let mkdir_err = |e: std::io::Error| Error::Io(format!("consent: mkdir {}: {e}", dir.display()));
    let mut b = fs::DirBuilder::new();
    b.recursive(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::{DirBuilderExt, PermissionsExt};
        b.mode(DIR_MODE);
        b.create(dir).map_err(mkdir_err)?;
        if dir.file_name().is_some_and(|n| n == STATE_DIR) {
            fs::set_permissions(dir, fs::Permissions::from_mode(DIR_MODE)).map_err(mkdir_err)?;
        }
    }
    #[cfg(not(unix))]
    b.create(dir).map_err(mkdir_err)?;
    Ok(())
}

/// Opens `path` for writing a new file, 0600 on Unix.
pub(crate) fn open_options() -> fs::OpenOptions {
    let mut o = fs::OpenOptions::new();
    o.write(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        o.mode(FILE_MODE);
    }
    o
}

fn set_file_mode(path: &Path) -> std::io::Result<()> {
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(path, fs::Permissions::from_mode(FILE_MODE))
    }
    #[cfg(not(unix))]
    {
        let _ = path;
        Ok(())
    }
}

/// Writes `b` to a temp sibling, fsyncs, sets 0600 and renames it over
/// `path`, so readers never see a half-written file.
pub(crate) fn write_file_atomic(path: &Path, b: &[u8]) -> Result<(), Error> {
    static SEQ: AtomicU64 = AtomicU64::new(0);
    let dir = path.parent().unwrap_or(Path::new("."));
    ensure_dir(dir)?;
    let name = path
        .file_name()
        .map(|n| n.to_string_lossy())
        .unwrap_or_default();
    let tmp = dir.join(format!(
        ".{name}.tmp.{}.{}",
        std::process::id(),
        SEQ.fetch_add(1, Ordering::Relaxed)
    ));
    let fail = |what: &str, e: std::io::Error| {
        let _ = fs::remove_file(&tmp);
        Error::Io(format!("consent: {what} {}: {e}", path.display()))
    };
    let mut f = open_options()
        .create_new(true)
        .open(&tmp)
        .map_err(|e| fail("create tmp for", e))?;
    f.write_all(b).map_err(|e| fail("write tmp for", e))?;
    f.sync_all().map_err(|e| fail("fsync tmp for", e))?;
    drop(f);
    set_file_mode(&tmp).map_err(|e| fail("chmod tmp for", e))?;
    fs::rename(&tmp, path).map_err(|e| fail("rename", e))?;
    set_file_mode(path).map_err(|e| Error::Io(format!("consent: chmod {}: {e}", path.display())))
}

/// A session builder over the per-tool [`FileStore`] and
/// [`FileAuditor`] of `tool` ([`default_tool`] when `None`).
pub fn open(tool: Option<&str>) -> Result<GrantsBuilder, Error> {
    let tool = tool.map(str::to_string).unwrap_or_else(default_tool);
    let store = FileStore::for_tool(&tool)?;
    let audit = Arc::new(FileAuditor::for_tool(&tool)?);
    let mut b = Grants::builder(Arc::new(store)).auditor(audit.clone());
    b.file_auditor = Some(audit);
    Ok(b)
}

impl GrantsBuilder {
    /// After [`open`]: builds a [`Relay`] from the audit log onto
    /// `publisher` and wires it as the delivery. Ignored otherwise.
    pub fn deliver_to(mut self, publisher: Arc<dyn Publisher>) -> Self {
        if let Some(a) = self.file_auditor.clone() {
            let r = Arc::new(Relay::new(a, publisher));
            self.relay = Some(r.clone());
            self = self.delivery(r);
        }
        self
    }
}

impl Grants {
    /// The relay built by [`GrantsBuilder::deliver_to`].
    pub fn relay(&self) -> Option<Arc<Relay>> {
        self.relay.clone()
    }
}
