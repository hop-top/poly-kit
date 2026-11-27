//! Backup and restore a driver cannot do: `BackupUnsupported` from a
//! driver, `NotRestored` from the migrator, matched by the `is_*`
//! helpers (Go `errors.Is`). The cross-language texts are pinned in
//! `upgrade_conformance.rs`.

#![cfg(all(feature = "upgrade", unix))]

use std::path::Path;
use std::sync::{Arc, Mutex};

use hop_top_kit::bus::Context;
use hop_top_kit::upgrade::{
    is_backup_unsupported, is_not_restored, BackupUnsupported, BoxError, MigrateError, Migration,
    Migrator, SchemaDriver,
};

/// A driver whose backup or restore is unsupported, or whose restore fails.
struct Driver {
    version: Arc<Mutex<String>>,
    backup_unsupported: bool,
    restore: Restore,
    restores: Arc<Mutex<u32>>,
}

#[derive(Clone, Copy)]
enum Restore {
    Works,
    Unsupported,
    Fails,
}

impl SchemaDriver for Driver {
    fn name(&self) -> &str {
        "db"
    }
    fn version(&self) -> Result<String, BoxError> {
        Ok(self.version.lock().unwrap().clone())
    }
    fn set_version(&mut self, v: &str) -> Result<(), BoxError> {
        *self.version.lock().unwrap() = v.into();
        Ok(())
    }
    fn backup(&self, _dest: &Path) -> Result<(), BoxError> {
        if self.backup_unsupported {
            return Err(Box::new(BackupUnsupported));
        }
        Ok(())
    }
    fn restore(&mut self, _src: &Path) -> Result<(), BoxError> {
        *self.restores.lock().unwrap() += 1;
        match self.restore {
            Restore::Works => Ok(()),
            Restore::Unsupported => Err(Box::new(BackupUnsupported)),
            Restore::Fails => Err("disk gone".into()),
        }
    }
}

struct Setup {
    migrator: Migrator,
    version: Arc<Mutex<String>>,
    restores: Arc<Mutex<u32>>,
    dir: tempfile::TempDir,
}

/// Targets 1.3.0 over 1.1.0 (with a down), 1.2.0 (none) and 1.3.0
/// (fails): a failure needs a restore.
fn setup(backup_unsupported: bool, restore: Restore, manual: bool) -> Setup {
    let dir = tempfile::tempdir().unwrap();
    let version = Arc::new(Mutex::new("1.0.0".to_string()));
    let restores = Arc::new(Mutex::new(0));
    let mut m = Migrator::new("unsup", "1.3.0").data_dir(dir.path());
    if manual {
        m = m.manual_rollback();
    }
    m.register(Migration::new("1.1.0", "db", |_: &Context| Ok(())).with_down(|_: &Context| Ok(())));
    m.register(Migration::new("1.2.0", "db", |_: &Context| Ok(())));
    m.register(Migration::new("1.3.0", "db", |_: &Context| {
        Err("boom".into())
    }));
    m.add_driver(Driver {
        version: version.clone(),
        backup_unsupported,
        restore,
        restores: restores.clone(),
    });
    Setup {
        migrator: m,
        version,
        restores,
        dir,
    }
}

fn backup_names(dir: &Path) -> Vec<String> {
    let Ok(entries) = std::fs::read_dir(dir.join("backups").join("db")) else {
        return Vec::new();
    };
    let mut names: Vec<String> = entries
        .map(|e| e.unwrap().file_name().into_string().unwrap())
        .collect();
    names.sort();
    names
}

#[test]
fn not_restored_with_no_backup_matches_both_helpers() {
    let mut s = setup(true, Restore::Works, false);
    let err = s.migrator.run(&Context::background()).unwrap_err();
    assert!(err.is_not_restored(), "{err}");
    assert!(err.is_backup_unsupported(), "{err}");
    assert!(!err.is_verify() && !err.is_canceled());
    assert_eq!(err.step_kind(), Some(hop_top_kit::upgrade::StepKind::Up));
    assert!(err.to_string().contains("up 1.3.0 failed: boom"), "{err}");
    assert_eq!(*s.restores.lock().unwrap(), 0, "no backup: no restore call");
    assert_eq!(*s.version.lock().unwrap(), "1.2.0");
    assert!(backup_names(s.dir.path()).is_empty());
}

#[test]
fn unsupported_restore_is_not_restored() {
    let mut s = setup(false, Restore::Unsupported, false);
    let err = s.migrator.run(&Context::background()).unwrap_err();
    assert!(
        err.is_not_restored() && err.is_backup_unsupported(),
        "{err}"
    );
    assert_eq!(*s.restores.lock().unwrap(), 1);
}

#[test]
fn a_plain_restore_failure_is_not_not_restored() {
    let mut s = setup(false, Restore::Fails, false);
    let err = s.migrator.run(&Context::background()).unwrap_err();
    assert!(
        !err.is_not_restored() && !err.is_backup_unsupported(),
        "{err}"
    );
    assert!(err.to_string().contains("disk gone"), "{err}");
}

#[test]
fn unsupported_backup_keeps_an_existing_directory_and_skips_prune() {
    let mut s = setup(true, Restore::Works, true);
    let old = s.dir.path().join("backups/db/pre-1.1.0");
    std::fs::create_dir_all(&old).unwrap();
    let _ = s.migrator.run(&Context::background());
    assert!(
        old.exists(),
        "a directory that existed is not ours to remove"
    );
}

#[test]
fn rollback_latest_without_a_backup_or_restore_is_not_restored() {
    let mut s = setup(true, Restore::Works, true);
    let _ = s.migrator.run(&Context::background());
    let err = s.migrator.rollback_latest().unwrap_err();
    assert!(matches!(err, MigrateError::Rollback { .. }));
    assert!(
        err.is_not_restored() && err.is_backup_unsupported(),
        "{err}"
    );
    assert_eq!(*s.version.lock().unwrap(), "1.2.0");

    let mut s = setup(false, Restore::Unsupported, true);
    let _ = s.migrator.run(&Context::background());
    let err = s.migrator.rollback_latest().unwrap_err();
    assert!(
        err.is_not_restored() && err.is_backup_unsupported(),
        "{err}"
    );
    assert_eq!(*s.version.lock().unwrap(), "1.2.0");
}

#[test]
fn helpers_tell_the_errors_apart() {
    assert!(is_backup_unsupported(&BackupUnsupported));
    assert!(!is_not_restored(&BackupUnsupported));
    let other: BoxError = "x".into();
    assert!(!is_backup_unsupported(&*other));
}
