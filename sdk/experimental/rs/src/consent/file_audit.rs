//! The per-tool audit log: JSON lines, the durable outbox a `Relay`
//! delivers from.

use std::fs;
use std::io::{self, Write};
use std::path::{Path, PathBuf};
use std::sync::{Mutex, MutexGuard};

use super::audit::{AuditEvent, Auditor};
use super::error::Error;
use super::paths::{ensure_dir, open_options, state_file, AUDIT_FILE};

/// TypeID prefix of audit log ids.
const LOG_PREFIX: &str = "clog";

#[derive(Default)]
struct Pos {
    loaded: bool,
    log: String,
    seq: u64,
}

/// Appends audit events to `<XDG_STATE_HOME>/<tool>/consent/audit.jsonl`
/// (0600 in a 0700 directory), fsynced before `record` returns. Each
/// record gets the log id (minted when the log is created) and the next
/// sequence number. One process writes a log at a time.
pub struct FileAuditor {
    path: PathBuf,
    pos: Mutex<Pos>,
}

impl FileAuditor {
    /// The audit log of `tool`.
    pub fn for_tool(tool: &str) -> Result<Self, Error> {
        Ok(Self::at(state_file(tool, AUDIT_FILE)?))
    }

    /// The audit log at `path`.
    pub fn at(path: impl Into<PathBuf>) -> Self {
        FileAuditor {
            path: path.into(),
            pos: Mutex::new(Pos::default()),
        }
    }

    pub fn path(&self) -> &Path {
        &self.path
    }

    fn lock(&self) -> MutexGuard<'_, Pos> {
        self.pos.lock().unwrap_or_else(|e| e.into_inner())
    }

    fn load(&self, p: &mut Pos) -> Result<(), String> {
        if p.loaded {
            return Ok(());
        }
        let all = self.read_all()?;
        match all.last() {
            Some(last) => (p.log, p.seq) = (last.log.clone(), last.seq),
            None => {
                p.log = crate::id::new(LOG_PREFIX)
                    .map_err(|e| format!("consent: mint audit log id: {e}"))?
            }
        }
        p.loaded = true;
        Ok(())
    }

    /// The log id, minting one for a log not written yet.
    pub fn log_id(&self) -> Result<String, String> {
        let mut p = self.lock();
        self.load(&mut p)?;
        Ok(p.log.clone())
    }

    /// The records with `seq > after`, in order.
    pub fn read_after(&self, after: u64) -> Result<Vec<AuditEvent>, String> {
        let _p = self.lock();
        Ok(self
            .read_all()?
            .into_iter()
            .filter(|e| e.seq > after)
            .collect())
    }

    /// Parses the whole log. A record that does not parse, or breaks the
    /// log id or the sequence, makes the log unreadable: the next use is
    /// refused rather than audited into a broken trail.
    fn read_all(&self) -> Result<Vec<AuditEvent>, String> {
        let src = match fs::read_to_string(&self.path) {
            Ok(s) => s,
            Err(e) if e.kind() == io::ErrorKind::NotFound => return Ok(Vec::new()),
            Err(e) => return Err(format!("consent: audit read: {e}")),
        };
        let mut out: Vec<AuditEvent> = Vec::new();
        for (i, line) in src.lines().enumerate() {
            let n = i + 1;
            let where_ = || format!("consent: audit log {} line {n}", self.path.display());
            let e = AuditEvent::from_json(line).map_err(|err| format!("{}: {err}", where_()))?;
            match out.first() {
                None if e.log.is_empty() || e.seq != 1 => {
                    return Err(format!(
                        "{}: first record must be seq 1 with a log id",
                        where_()
                    ))
                }
                Some(first)
                    if e.log != first.log || Some(e.seq) != out.last().map(|l| l.seq + 1) =>
                {
                    return Err(format!("{}: log id or sequence out of order", where_()))
                }
                _ => {}
            }
            out.push(e);
        }
        Ok(out)
    }

    fn append(&self, line: &str) -> io::Result<()> {
        let mut f = open_options().append(true).create(true).open(&self.path)?;
        f.write_all(line.as_bytes())?;
        f.write_all(b"\n")?;
        f.sync_all()
    }
}

impl Auditor for FileAuditor {
    fn record(&self, event: &AuditEvent) -> Result<(), String> {
        let mut p = self.lock();
        self.load(&mut p)?;
        let mut e = event.clone();
        e.log.clone_from(&p.log);
        e.seq = p.seq + 1;
        if let Some(dir) = self.path.parent() {
            ensure_dir(dir).map_err(|err| err.to_string())?;
        }
        self.append(&e.to_json())
            .map_err(|err| format!("consent: audit write: {err}"))?;
        p.seq = e.seq;
        Ok(())
    }
}
