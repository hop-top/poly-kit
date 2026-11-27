//! A [`SchemaDriver`] for a SQLite database file. Go
//! `upgrade/driver/sqlite`.
//!
//! Two modes. Path only ([`SqliteDriver::new`]): `backup` opens a
//! short-lived connection of its own and `restore` swaps the file. With
//! [`with_connection`](SqliteDriver::with_connection): `backup` runs on
//! the caller's connection, which the driver never closes, and `restore`
//! returns [`BackupUnsupported`] (the file cannot be swapped under a live
//! connection). Both lock `<data dir>/<name>/.lock`.

use std::error::Error;
use std::ffi::OsString;
use std::fmt;
use std::fs;
use std::io;
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex, MutexGuard};
use std::time::Duration;

use rusqlite::{Connection, OpenFlags};

use crate::bus::Context;

use super::{
    filelock, fs_util, read_version_file_in, BackupUnsupported, BoxError, Locker, SchemaDriver,
    Unlock,
};

/// How long the driver's own connection waits on a lock.
const BUSY_TIMEOUT: Duration = Duration::from_secs(5);

/// Files SQLite keeps beside a database. A `-wal` or hot `-journal` left
/// from the replaced database would be replayed onto the restored one; the
/// `-shm` indexes that WAL.
const SIDECARS: [&str; 3] = ["-wal", "-shm", "-journal"];

/// The connection a caller shares with the driver. `rusqlite::Connection`
/// is `Send` but not `Sync`, so it travels in a mutex (a
/// [`sqldb::open`](crate::sqldb::open) result fits).
pub type SharedConnection = Arc<Mutex<Connection>>;

/// Backs up and restores one SQLite database file; the Migrator keeps the
/// version in its version file.
#[derive(Debug, Clone)]
pub struct SqliteDriver {
    name: String,
    tool: String,
    data_dir: Option<PathBuf>,
    db_path: PathBuf,
    conn: Option<SharedConnection>,
}

impl SqliteDriver {
    /// A driver named `sqlite` for the database file at `db_path`. Go `New`.
    pub fn new(db_path: impl Into<PathBuf>) -> SqliteDriver {
        SqliteDriver {
            name: "sqlite".to_string(),
            tool: String::new(),
            data_dir: None,
            db_path: db_path.into(),
            conn: None,
        }
    }

    /// Overrides the schema name (default `sqlite`). Go `WithName`.
    pub fn with_name(mut self, name: impl Into<String>) -> SqliteDriver {
        self.name = name.into();
        self
    }

    /// The tool whose [`default_data_dir`](super::default_data_dir) holds
    /// the version file and the lock. Go `WithTool`.
    pub fn with_tool(mut self, tool: impl Into<String>) -> SqliteDriver {
        self.tool = tool.into();
        self
    }

    /// Reads the version file and locks under `dir`; pass the migrator's
    /// [`data_dir`](super::Migrator::data_dir) when it sets one. Go
    /// `WithDataDir`.
    pub fn with_data_dir(mut self, dir: impl Into<PathBuf>) -> SqliteDriver {
        self.data_dir = Some(dir.into());
        self
    }

    /// Runs `backup` on the caller's connection instead of one of the
    /// driver's own. The driver never closes it, and `restore` returns
    /// [`BackupUnsupported`]; the backup file stays for manual recovery.
    /// `db_path` then only names the backup file (it may be empty: the
    /// backup is `<name>.db`). Go `WithDB`.
    pub fn with_connection(mut self, conn: SharedConnection) -> SqliteDriver {
        self.conn = Some(conn);
        self
    }

    /// The backup file's name inside a backup directory.
    fn backup_name(&self) -> OsString {
        match self.db_path.file_name() {
            Some(n) => n.to_os_string(),
            None => OsString::from(format!("{}.db", self.name)),
        }
    }

    /// `VACUUM INTO` `file`, which must not exist. The name is a bound
    /// parameter, so no path needs quoting.
    fn vacuum_into(&self, file: &Path) -> Result<(), BoxError> {
        let target = file
            .to_str()
            .ok_or("backup path is not valid UTF-8")?
            .to_string();
        let run = |c: &Connection| c.execute("VACUUM INTO ?1", [target.as_str()]).map(|_| ());
        match &self.conn {
            Some(shared) => run(&lock_conn(shared))?,
            None => {
                // Plain file name (no URI flag), read-write, never create.
                let own = Connection::open_with_flags(
                    &self.db_path,
                    OpenFlags::SQLITE_OPEN_READ_WRITE | OpenFlags::SQLITE_OPEN_NO_MUTEX,
                )?;
                own.busy_timeout(BUSY_TIMEOUT)?;
                run(&own)?;
            }
        }
        Ok(())
    }
}

fn lock_conn(shared: &SharedConnection) -> MutexGuard<'_, Connection> {
    shared
        .lock()
        .unwrap_or_else(|poisoned| poisoned.into_inner())
}

/// `restore` under a caller-owned connection; its source is
/// [`BackupUnsupported`].
#[derive(Debug)]
struct RestoreUnderConnection(PathBuf);

impl fmt::Display for RestoreUnderConnection {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(
            f,
            "restore {} under a caller-owned connection: {BackupUnsupported}",
            self.0.display()
        )
    }
}

impl Error for RestoreUnderConnection {
    fn source(&self) -> Option<&(dyn Error + 'static)> {
        Some(&BackupUnsupported)
    }
}

impl SchemaDriver for SqliteDriver {
    fn name(&self) -> &str {
        &self.name
    }

    fn version(&self) -> Result<String, BoxError> {
        let dir = self
            .data_dir
            .clone()
            .unwrap_or_else(|| super::default_data_dir(&self.tool));
        Ok(read_version_file_in(&dir, &self.name)?)
    }

    /// The runner has written the version file already.
    fn set_version(&mut self, _version: &str) -> Result<(), BoxError> {
        Ok(())
    }

    fn as_locker(&self) -> Option<&dyn Locker> {
        Some(self)
    }

    /// Writes a consistent snapshot to `<dest>/<database file name>` with
    /// `VACUUM INTO`, which reads through the WAL: rows committed but not
    /// yet checkpointed are in it. The snapshot is built beside the target
    /// and renamed. Path only: a missing database file is "nothing to back
    /// up" (`Ok`, no file). With a connection it must not be inside a
    /// transaction.
    fn backup(&self, dest: &Path) -> Result<(), BoxError> {
        if self.conn.is_none() {
            match fs::metadata(&self.db_path) {
                Ok(_) => {}
                Err(e) if e.kind() == io::ErrorKind::NotFound => return Ok(()),
                Err(e) => return Err(format!("stat db: {e}").into()),
            }
        }
        fs_util::create_dir_all(dest).map_err(|e| format!("create backup dir: {e}"))?;
        let target = dest.join(self.backup_name());
        let mut tmp = target.clone().into_os_string();
        tmp.push(".tmp");
        let tmp = PathBuf::from(tmp);
        // VACUUM INTO refuses an existing file; a leftover is stale.
        for p in [&tmp, &target] {
            match fs::remove_file(p) {
                Ok(()) => {}
                Err(e) if e.kind() == io::ErrorKind::NotFound => {}
                Err(e) => return Err(format!("clear backup file: {e}").into()),
            }
        }
        let made = self
            .vacuum_into(&tmp)
            .and_then(|()| fs::rename(&tmp, &target).map_err(Into::into));
        if let Err(e) = made {
            let _ = fs::remove_file(&tmp);
            return Err(format!("backup db: {e}").into());
        }
        Ok(())
    }

    /// Replaces the database file with the backup in `src`, then removes
    /// the replaced database's `-wal`, `-shm` and `-journal` files.
    ///
    /// Precondition: no open connection on the file, in this process or
    /// another; one left open keeps using the replaced file. The backup is
    /// copied to a temporary file beside the database, synced and renamed
    /// over it, so a failure before the rename leaves the database and its
    /// sidecars untouched. An error after the rename (it says "replaced")
    /// means the database already holds the backup.
    ///
    /// With a connection it restores nothing and returns an error whose
    /// source is [`BackupUnsupported`]; the backup stays.
    fn restore(&mut self, src: &Path) -> Result<(), BoxError> {
        if self.conn.is_some() {
            return Err(Box::new(RestoreUnderConnection(self.db_path.clone())));
        }
        let mut input = fs::File::open(src.join(self.backup_name()))
            .map_err(|e| format!("open backup: {e}"))?;
        let dir = match self.db_path.parent() {
            Some(d) if !d.as_os_str().is_empty() => d.to_path_buf(),
            _ => PathBuf::from("."),
        };
        fs_util::create_dir_all(&dir).map_err(|e| format!("create db dir: {e}"))?;

        let tmp = fill_temp(&self.db_path, &dir, &mut input)?;
        if let Err(e) = fs::rename(&tmp, &self.db_path) {
            let _ = fs::remove_file(&tmp);
            return Err(format!("restore db: {e}").into());
        }

        let mut errs: Vec<String> = Vec::new();
        for suffix in SIDECARS {
            let mut p = self.db_path.clone().into_os_string();
            p.push(suffix);
            match fs::remove_file(&p) {
                Ok(()) => {}
                Err(e) if e.kind() == io::ErrorKind::NotFound => {}
                Err(e) => errs.push(format!("remove stale {}: {e}", PathBuf::from(&p).display())),
            }
        }
        #[cfg(unix)]
        if let Err(e) = fs::File::open(&dir).and_then(|d| d.sync_all()) {
            errs.push(format!("fsync dir {}: {e}", dir.display()));
        }
        if errs.is_empty() {
            Ok(())
        } else {
            Err(format!(
                "restore: {} replaced: {}",
                self.db_path.display(),
                errs.join("; ")
            )
            .into())
        }
    }
}

/// Copies `input` into a new file `<db name>.restore-<n>` in `dir`, with
/// the database's mode (0600 when it does not exist), synced.
fn fill_temp(db: &Path, dir: &Path, input: &mut fs::File) -> Result<PathBuf, BoxError> {
    let base = db.file_name().unwrap_or_default().to_string_lossy();
    let (path, mut out) = (0..100u32)
        .find_map(|n| {
            let p = dir.join(format!("{base}.restore-{}-{n}", std::process::id()));
            let mut o = fs::OpenOptions::new();
            o.write(true).create_new(true);
            #[cfg(unix)]
            std::os::unix::fs::OpenOptionsExt::mode(&mut o, 0o600);
            o.open(&p).ok().map(|f| (p, f))
        })
        .ok_or("create db: no free temporary name")?;
    let copied = io::copy(input, &mut out)
        .and_then(|_| match fs::metadata(db) {
            Ok(m) => out.set_permissions(m.permissions()),
            Err(_) => Ok(()),
        })
        .and_then(|()| out.sync_all());
    if let Err(e) = copied {
        let _ = fs::remove_file(&path);
        return Err(format!("restore db: {e}").into());
    }
    Ok(path)
}

impl Locker for SqliteDriver {
    /// Exclusive flock on `<data dir>/<name>/.lock`; see the module docs.
    fn lock(&self, ctx: &Context) -> Result<Unlock, BoxError> {
        let dir = filelock::state_dir(self.data_dir.as_deref(), &self.tool, &self.name);
        filelock::lock_in(&dir, ctx)
    }
}
