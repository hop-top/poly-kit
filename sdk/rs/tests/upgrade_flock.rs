//! The lock the SQLite and directory drivers take is flock(2): another
//! process taking flock on the same `.lock` file excludes them, and they
//! exclude it. That is the lock Go's `gofrs/flock` takes, so a Go and a
//! Rust process migrating one store exclude each other. The other process
//! here is this test binary re-executed, calling flock(2) directly.

#![cfg(all(any(feature = "upgrade-sqlite", feature = "upgrade-fsdir"), unix))]

use std::fs::OpenOptions;
use std::io::{BufRead, BufReader, Write};
use std::os::fd::AsRawFd;
use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};
use std::time::Duration;

use hop_top_kit::bus::{Context, ContextError};
use hop_top_kit::upgrade::SchemaDriver;

extern "C" {
    fn flock(fd: i32, operation: i32) -> i32;
}

// Identical on Linux and macOS.
const LOCK_EX: i32 = 2;
const LOCK_NB: i32 = 4;

const MODE_ENV: &str = "KIT_FLOCK_HELPER";
const FILE_ENV: &str = "KIT_FLOCK_FILE";

/// Helper process: `try` makes one non-blocking flock(2) attempt and exits
/// 0 when it got the lock, 3 when it would block; `hold` takes it, prints
/// `held` and keeps it until stdin closes.
#[test]
fn flock_helper() {
    let (Ok(mode), Ok(file)) = (std::env::var(MODE_ENV), std::env::var(FILE_ENV)) else {
        return;
    };
    let f = OpenOptions::new()
        .read(true)
        .write(true)
        .open(file)
        .unwrap();
    let fd = f.as_raw_fd();
    let call = |op| unsafe { flock(fd, op) };
    match mode.as_str() {
        "try" => {
            if call(LOCK_EX | LOCK_NB) == 0 {
                std::process::exit(0);
            }
            let e = std::io::Error::last_os_error();
            assert_eq!(e.kind(), std::io::ErrorKind::WouldBlock, "{e}");
            std::process::exit(3);
        }
        "hold" => {
            assert_eq!(call(LOCK_EX), 0);
            println!("held");
            std::io::stdout().flush().unwrap();
            let mut s = String::new();
            let _ = std::io::stdin().read_line(&mut s); // returns when the parent closes it
            std::process::exit(0);
        }
        other => panic!("unknown mode {other}"),
    }
}

fn helper(mode: &str, file: &Path) -> Command {
    let mut c = Command::new(std::env::current_exe().unwrap());
    c.args(["--exact", "flock_helper", "--test-threads=1", "--nocapture"])
        .env(MODE_ENV, mode)
        .env(FILE_ENV, file);
    c
}

/// Exit status of a `try` helper: 0 acquired, 3 would block.
fn try_flock(file: &Path) -> i32 {
    helper("try", file)
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .status()
        .unwrap()
        .code()
        .unwrap()
}

/// While a driver holds its lock, another process's flock(2) on the file
/// would block; after the unlock it succeeds.
#[test]
fn driver_lock_excludes_another_process_flock() {
    for (name, mk) in drivers() {
        let dir = tempfile::tempdir().unwrap();
        let d = mk(dir.path());
        let lock_file = dir.path().join("s/.lock");
        let unlock = d.as_locker().unwrap().lock(&Context::background()).unwrap();
        assert_eq!(
            try_flock(&lock_file),
            3,
            "{name}: flock must block while held"
        );
        unlock().unwrap();
        assert_eq!(try_flock(&lock_file), 0, "{name}: flock free after unlock");
    }
}

/// While another process holds flock(2) on the file, the driver's lock
/// waits, honours the deadline and holds nothing; when the process lets go
/// the driver acquires.
#[test]
fn another_process_flock_excludes_the_driver_lock() {
    for (name, mk) in drivers() {
        let dir = tempfile::tempdir().unwrap();
        let d = mk(dir.path());
        let lock_file: PathBuf = dir.path().join("s/.lock");
        d.as_locker().unwrap().lock(&Context::background()).unwrap()().unwrap(); // creates the file
        let mut child = helper("hold", &lock_file)
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::null())
            .spawn()
            .unwrap();
        let mut out = BufReader::new(child.stdout.take().unwrap());
        let mut line = String::new();
        while !line.trim().ends_with("held") {
            line.clear();
            assert!(out.read_line(&mut line).unwrap() > 0, "{name}: helper died");
        }

        let (ctx, _h) = Context::background().with_timeout(Duration::from_millis(200));
        let err = d
            .as_locker()
            .unwrap()
            .lock(&ctx)
            .err()
            .expect("held elsewhere");
        assert_eq!(
            err.downcast_ref::<ContextError>(),
            Some(&ContextError::DeadlineExceeded),
            "{name}"
        );

        drop(child.stdin.take()); // the helper exits and its flock goes with it
        child.wait().unwrap();
        let (ctx, _h) = Context::background().with_timeout(Duration::from_secs(3));
        d.as_locker()
            .unwrap()
            .lock(&ctx)
            .expect("free after the holder exited")()
        .unwrap();
    }
}

type MakeDriver = fn(&Path) -> Box<dyn SchemaDriver>;

/// Each enabled driver, built over a data dir; schema `s`.
fn drivers() -> Vec<(&'static str, MakeDriver)> {
    #[allow(unused_mut)]
    let mut v: Vec<(&'static str, MakeDriver)> = Vec::new();
    #[cfg(feature = "upgrade-sqlite")]
    v.push(("sqlite", |dir| {
        Box::new(
            hop_top_kit::upgrade::sqlite::SqliteDriver::new(dir.join("x.db"))
                .with_name("s")
                .with_data_dir(dir),
        )
    }));
    #[cfg(feature = "upgrade-fsdir")]
    v.push(("fsdir", |dir| {
        Box::new(
            hop_top_kit::upgrade::fsdir::FsDirDriver::new("s", dir.join("d")).with_data_dir(dir),
        )
    }));
    v
}
