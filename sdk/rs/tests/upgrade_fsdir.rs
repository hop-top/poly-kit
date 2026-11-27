//! Ports of the Go `upgrade/driver/fsdir` tests, plus what the Rust driver
//! adds: the traversal guard, a swap-in restore that leaves the original
//! intact on failure, the file lock, a Go-made archive (a fixture) and the
//! archive layout Go reads.

#![cfg(all(feature = "upgrade-fsdir", unix))]

use std::fs;
use std::io::Read;
use std::os::unix::fs::PermissionsExt;
use std::path::{Path, PathBuf};
use std::time::Duration;

use flate2::read::GzDecoder;
use flate2::write::GzEncoder;
use flate2::Compression;
use hop_top_kit::bus::{Context, ContextError};
use hop_top_kit::upgrade::fsdir::FsDirDriver;
use hop_top_kit::upgrade::{read_version_file_in, BoxError, Migration, Migrator, SchemaDriver};
use tar::{Archive, Builder, EntryType, Header};

fn bg() -> Context {
    Context::background()
}

fn mode(p: &Path) -> u32 {
    fs::symlink_metadata(p).unwrap().permissions().mode() & 0o7777
}

fn write(p: &Path, body: &str, m: u32) {
    fs::create_dir_all(p.parent().unwrap()).unwrap();
    fs::write(p, body).unwrap();
    fs::set_permissions(p, fs::Permissions::from_mode(m)).unwrap();
}

/// Every entry under `root` as `(relative path, kind and content)`.
fn tree(root: &Path) -> Vec<(String, String)> {
    fn walk(root: &Path, dir: &Path, out: &mut Vec<(String, String)>) {
        let mut ents: Vec<_> = fs::read_dir(dir)
            .unwrap()
            .map(|e| e.unwrap().path())
            .collect();
        ents.sort();
        for p in ents {
            let rel = p.strip_prefix(root).unwrap().to_string_lossy().into_owned();
            let m = fs::symlink_metadata(&p).unwrap();
            if m.is_dir() {
                out.push((rel, "dir".into()));
                walk(root, &p, out);
            } else if m.file_type().is_symlink() {
                out.push((
                    rel,
                    format!("link {}", fs::read_link(&p).unwrap().display()),
                ));
            } else {
                out.push((rel, format!("file {}", fs::read_to_string(&p).unwrap())));
            }
        }
    }
    let mut out = Vec::new();
    walk(root, root, &mut out);
    out
}

fn mkdir(p: &Path) {
    fs::create_dir_all(p).unwrap();
    fs::set_permissions(p, fs::Permissions::from_mode(0o750)).unwrap();
}

fn sample(dir: &Path) {
    mkdir(dir);
    mkdir(&dir.join("sub"));
    write(&dir.join("file.txt"), "hello", 0o600);
    write(&dir.join("sub/nested.txt"), "world", 0o600);
    write(&dir.join("run.sh"), "#!/bin/sh\n", 0o755);
    mkdir(&dir.join("empty"));
    std::os::unix::fs::symlink("sub/nested.txt", dir.join("alias")).unwrap();
}

#[test]
fn name_and_roundtrip() {
    let tmp = tempfile::tempdir().unwrap();
    let dir = tmp.path().join("mydata");
    sample(&dir);
    let mut d = FsDirDriver::new("mydata", &dir);
    assert_eq!(d.name(), "mydata");

    let bk = tmp.path().join("backup");
    d.backup(&bk).unwrap();
    assert!(bk.join("mydata.tar.gz").is_file());
    let before = tree(&dir);

    write(&dir.join("file.txt"), "changed", 0o600);
    fs::remove_dir_all(dir.join("sub")).unwrap();
    write(&dir.join("stray.txt"), "x", 0o600);

    d.restore(&bk).unwrap();
    assert_eq!(tree(&dir), before);
    assert_eq!(mode(&dir.join("run.sh")), 0o755);
    assert_eq!(mode(&dir.join("file.txt")), 0o600);
    assert_eq!(mode(&dir.join("sub")), 0o750);
    assert_eq!(mode(&dir), 0o750);
    let names: Vec<_> = fs::read_dir(tmp.path())
        .unwrap()
        .map(|e| e.unwrap().file_name())
        .collect();
    assert_eq!(names.len(), 2, "no staging or old tree left: {names:?}");
}

#[test]
fn backup_missing_dir_is_nothing_to_back_up() {
    let tmp = tempfile::tempdir().unwrap();
    let bk = tmp.path().join("bk");
    FsDirDriver::new("missing", "/nonexistent/dir")
        .backup(&bk)
        .unwrap();
    assert!(!bk.join("missing.tar.gz").exists());
}

#[test]
fn restore_missing_archive_fails() {
    let tmp = tempfile::tempdir().unwrap();
    let dir = tmp.path().join("d");
    write(&dir.join("keep"), "1", 0o600);
    let err = FsDirDriver::new("missing", &dir)
        .restore(tmp.path())
        .unwrap_err();
    assert!(err.to_string().contains("not found"), "{err}");
    assert_eq!(tree(&dir), vec![("keep".into(), "file 1".into())]);
}

// ---- layout shared with the Go driver ----

fn members(archive: &Path) -> Vec<(String, EntryType, u32)> {
    let mut ar = Archive::new(GzDecoder::new(fs::File::open(archive).unwrap()));
    ar.entries()
        .unwrap()
        .map(|e| {
            let e = e.unwrap();
            let name = e.path().unwrap().to_string_lossy().into_owned();
            (name, e.header().entry_type(), e.header().mode().unwrap())
        })
        .collect()
}

/// Go writes the directory itself as `.`, then every entry in name order
/// as a path relative to it: no `./` prefix, no trailing slash, directory
/// and regular-file members with their mode bits.
#[test]
fn archive_layout_is_the_one_go_writes() {
    let tmp = tempfile::tempdir().unwrap();
    let dir = tmp.path().join("mydata");
    sample(&dir);
    FsDirDriver::new("mydata", &dir).backup(tmp.path()).unwrap();
    let got = members(&tmp.path().join("mydata.tar.gz"));
    let want = vec![
        (".", EntryType::Directory, 0o750),
        ("alias", EntryType::Symlink, 0o777),
        ("empty", EntryType::Directory, 0o750),
        ("file.txt", EntryType::Regular, 0o600),
        ("run.sh", EntryType::Regular, 0o755),
        ("sub", EntryType::Directory, 0o750),
        ("sub/nested.txt", EntryType::Regular, 0o600),
    ];
    // A symlink's own mode is the platform's (0777 on Linux, 0755 on macOS).
    let got: Vec<_> = got
        .iter()
        .map(|(n, t, m)| {
            (
                n.trim_end_matches('/'),
                *t,
                if *t == EntryType::Symlink {
                    0o777
                } else {
                    m & 0o777
                },
            )
        })
        .collect();
    assert_eq!(got, want);
}

const GO_ARCHIVE: &[u8] = include_bytes!("upgrade_fsdir/go-backup.tar.gz");

/// A backup the Go driver made (USTAR, root `.`, a member name over 100
/// bytes split into prefix and name) restores here. Regenerate with the
/// Go driver's `Backup` over: `file.txt` "hello" 0600, `sub/nested.txt`
/// "world" 0600, `exec.sh` 0755, `deep/<100 y>/leaf.txt` "leaf", dirs 0750.
#[test]
fn go_made_backup_restores() {
    let tmp = tempfile::tempdir().unwrap();
    let bk = tmp.path().join("bk");
    fs::create_dir_all(&bk).unwrap();
    fs::write(bk.join("mydata.tar.gz"), GO_ARCHIVE).unwrap();
    let dir = tmp.path().join("mydata");
    write(&dir.join("stale"), "old", 0o600);

    FsDirDriver::new("mydata", &dir).restore(&bk).unwrap();

    let long = format!("deep/{}", "y".repeat(100));
    let want: Vec<(String, String)> = vec![
        ("deep".into(), "dir".into()),
        (long.clone(), "dir".into()),
        (format!("{long}/leaf.txt"), "file leaf".into()),
        ("exec.sh".into(), "file #!/bin/sh\n".into()),
        ("file.txt".into(), "file hello".into()),
        ("sub".into(), "dir".into()),
        ("sub/nested.txt".into(), "file world".into()),
    ];
    assert_eq!(tree(&dir), want);
    assert_eq!(mode(&dir.join("exec.sh")), 0o755);
    assert_eq!(mode(&dir.join("file.txt")), 0o600);
    assert_eq!(mode(&dir), 0o750);
}

// ---- traversal guard ----

/// A gzip tar of raw members: `(name bytes, type, link name, body)`. Names
/// are written verbatim, which `Header::set_path` would refuse.
type Member = (&'static str, EntryType, &'static str, &'static str);

fn evil_archive(dest: &Path, members: &[Member]) {
    let gz = GzEncoder::new(
        fs::File::create(dest.join("mydata.tar.gz")).unwrap(),
        Compression::default(),
    );
    let mut b = Builder::new(gz);
    for (name, ty, link, body) in members {
        let mut h = Header::new_gnu();
        h.as_old_mut().name[..name.len()].copy_from_slice(name.as_bytes());
        if !link.is_empty() {
            h.as_old_mut().linkname[..link.len()].copy_from_slice(link.as_bytes());
        }
        h.set_entry_type(*ty);
        h.set_mode(if *ty == EntryType::Directory {
            0o755
        } else {
            0o644
        });
        h.set_size(body.len() as u64);
        h.set_cksum();
        b.append(&h, body.as_bytes()).unwrap();
    }
    b.into_inner().unwrap().finish().unwrap();
}

/// Each archive must be refused; the original directory is untouched, no
/// staging directory is left and nothing lands outside it.
#[test]
fn traversal_is_rejected_and_the_original_survives() {
    use EntryType::{Directory, Fifo, Link, Regular, Symlink};
    let cases: Vec<(&str, Vec<Member>)> = vec![
        (
            "dotdot",
            vec![
                ("ok.txt", Regular, "", "a"),
                ("../evil.txt", Regular, "", "x"),
            ],
        ),
        ("deep dotdot", vec![("a/../../evil.txt", Regular, "", "x")]),
        ("absolute", vec![("/tmp/kit-evil.txt", Regular, "", "x")]),
        ("dir dotdot", vec![("../evildir", Directory, "", "")]),
        ("symlink up", vec![("l", Symlink, "../outside", "")]),
        ("symlink absolute", vec![("l", Symlink, "/etc", "")]),
        (
            "symlink deep up",
            vec![("a", Directory, "", ""), ("a/l", Symlink, "../../x", "")],
        ),
        (
            "write through a link",
            vec![("s", Symlink, ".", ""), ("s/l", Symlink, "../x", "")],
        ),
        (
            "file through a link",
            vec![("s", Symlink, "sub", ""), ("s/f", Regular, "", "x")],
        ),
        ("hardlink up", vec![("h", Link, "../outside", "")]),
        ("fifo", vec![("p", Fifo, "", "")]),
    ];
    for (label, members) in cases {
        let tmp = tempfile::tempdir().unwrap();
        let area = tmp.path().join("area");
        let dir = area.join("mydata");
        write(&dir.join("keep.txt"), "original", 0o600);
        let bk = tmp.path().join("bk");
        fs::create_dir_all(&bk).unwrap();
        evil_archive(&bk, &members);

        let err = FsDirDriver::new("mydata", &dir)
            .restore(&bk)
            .unwrap_err()
            .to_string();
        assert!(
            err.contains("blocked") || err.contains("escapes"),
            "{label}: unexpected error {err}"
        );
        assert_eq!(
            tree(&dir),
            vec![("keep.txt".into(), "file original".into())],
            "{label}"
        );
        let left: Vec<_> = fs::read_dir(&area)
            .unwrap()
            .map(|e| e.unwrap().file_name())
            .collect();
        assert_eq!(left.len(), 1, "{label}: staging left behind: {left:?}");
        assert!(
            !tmp.path().join("evil.txt").exists() && !area.join("evil.txt").exists(),
            "{label}"
        );
        assert!(!Path::new("/tmp/kit-evil.txt").exists(), "{label}");
    }
}

/// Hard-link members: the source must be a regular file reached through
/// real directories. `d -> .` and `s -> d/..` look inside the tree
/// lexically but `s` really is the restore target's parent, so the
/// hard link `leak` would pull `outside.txt` into the tree; the later
/// regular file `s` replaces the link the final scan would have caught.
#[test]
fn hard_link_source_outside_the_tree_is_refused() {
    use std::os::unix::fs::MetadataExt;
    use EntryType::{Link, Regular, Symlink};
    let tmp = tempfile::tempdir().unwrap();
    let area = tmp.path().join("area");
    let dir = area.join("mydata");
    write(&dir.join("keep.txt"), "original", 0o600);
    let outside = area.join("outside.txt");
    write(&outside, "secret", 0o600);

    // The path the archive relies on does reach `outside.txt`: from a
    // staging directory beside the target, `s -> d/..` is `area`.
    let probe = area.join("probe");
    mkdir(&probe);
    std::os::unix::fs::symlink(".", probe.join("d")).unwrap();
    std::os::unix::fs::symlink("d/..", probe.join("s")).unwrap();
    assert_eq!(
        fs::read_to_string(probe.join("s/outside.txt")).unwrap(),
        "secret"
    );
    fs::remove_dir_all(&probe).unwrap();

    let bk = tmp.path().join("bk");
    fs::create_dir_all(&bk).unwrap();
    evil_archive(
        &bk,
        &[
            ("d", Symlink, ".", ""),
            ("s", Symlink, "d/..", ""),
            ("leak", Link, "s/outside.txt", ""),
            ("s", Regular, "", "plain"),
        ],
    );

    let err = FsDirDriver::new("mydata", &dir)
        .restore(&bk)
        .unwrap_err()
        .to_string();
    assert!(err.contains("blocked") || err.contains("escapes"), "{err}");
    assert_eq!(
        tree(&dir),
        vec![("keep.txt".into(), "file original".into())]
    );
    let mut left: Vec<_> = fs::read_dir(&area)
        .unwrap()
        .map(|e| e.unwrap().file_name())
        .collect();
    left.sort();
    assert_eq!(left, ["mydata", "outside.txt"], "no staging left");
    assert_eq!(
        fs::metadata(&outside).unwrap().nlink(),
        1,
        "outside.txt was hard-linked"
    );
}

/// A hard link whose source is itself a symlink is refused, whatever it
/// points at.
#[test]
fn hard_link_to_a_symlink_is_refused() {
    use EntryType::{Link, Regular, Symlink};
    let tmp = tempfile::tempdir().unwrap();
    let dir = tmp.path().join("mydata");
    write(&dir.join("keep.txt"), "original", 0o600);
    let bk = tmp.path().join("bk");
    fs::create_dir_all(&bk).unwrap();
    evil_archive(
        &bk,
        &[
            ("a", Regular, "", "x"),
            ("l", Symlink, "a", ""),
            ("h", Link, "l", ""),
        ],
    );
    let err = FsDirDriver::new("mydata", &dir)
        .restore(&bk)
        .unwrap_err()
        .to_string();
    assert!(err.contains("blocked") || err.contains("escapes"), "{err}");
    assert_eq!(
        tree(&dir),
        vec![("keep.txt".into(), "file original".into())]
    );
}

/// A hard link to a regular file inside the tree (what tar makes of
/// hard-linked files) restores as a hard link.
#[test]
fn hard_link_to_a_file_in_the_tree_is_kept() {
    use std::os::unix::fs::MetadataExt;
    use EntryType::{Directory, Link, Regular};
    let tmp = tempfile::tempdir().unwrap();
    let bk = tmp.path().join("bk");
    fs::create_dir_all(&bk).unwrap();
    evil_archive(
        &bk,
        &[
            ("sub", Directory, "", ""),
            ("sub/a.txt", Regular, "", "a"),
            ("b.txt", Link, "sub/a.txt", ""),
        ],
    );
    let dir = tmp.path().join("mydata");
    FsDirDriver::new("mydata", &dir).restore(&bk).unwrap();
    let (a, b) = (
        fs::metadata(dir.join("sub/a.txt")).unwrap(),
        fs::metadata(dir.join("b.txt")).unwrap(),
    );
    assert_eq!((a.ino(), a.nlink()), (b.ino(), 2));
}

/// A name that merely starts with dots is a file name, not a traversal
/// (Go's prefix test rejected it).
#[test]
fn dotted_names_are_not_traversal() {
    let tmp = tempfile::tempdir().unwrap();
    let bk = tmp.path().join("bk");
    fs::create_dir_all(&bk).unwrap();
    evil_archive(&bk, &[("..data", EntryType::Regular, "", "x")]);
    let dir = tmp.path().join("mydata");
    FsDirDriver::new("mydata", &dir).restore(&bk).unwrap();
    assert_eq!(tree(&dir), vec![("..data".into(), "file x".into())]);
}

/// A link inside the tree is restored as a link.
#[test]
fn links_inside_the_tree_are_kept() {
    let tmp = tempfile::tempdir().unwrap();
    let bk = tmp.path().join("bk");
    fs::create_dir_all(&bk).unwrap();
    evil_archive(
        &bk,
        &[
            ("sub", EntryType::Directory, "", ""),
            ("sub/a.txt", EntryType::Regular, "", "a"),
            ("sub/up", EntryType::Symlink, "../sub/a.txt", ""),
            ("hard", EntryType::Link, "sub/a.txt", ""),
        ],
    );
    let dir = tmp.path().join("mydata");
    FsDirDriver::new("mydata", &dir).restore(&bk).unwrap();
    assert_eq!(fs::read_to_string(dir.join("sub/up")).unwrap(), "a");
    assert_eq!(fs::read_to_string(dir.join("hard")).unwrap(), "a");
}

/// A corrupt archive fails after extraction started: the original stays.
#[test]
fn truncated_archive_leaves_the_original() {
    let tmp = tempfile::tempdir().unwrap();
    let dir = tmp.path().join("mydata");
    sample(&dir);
    let bk = tmp.path().join("bk");
    FsDirDriver::new("mydata", &dir).backup(&bk).unwrap();
    let archive = bk.join("mydata.tar.gz");
    let mut bytes = Vec::new();
    fs::File::open(&archive)
        .unwrap()
        .read_to_end(&mut bytes)
        .unwrap();
    fs::write(&archive, &bytes[..bytes.len() / 2]).unwrap();

    let before = tree(&dir);
    assert!(FsDirDriver::new("mydata", &dir).restore(&bk).is_err());
    assert_eq!(tree(&dir), before);
    assert_eq!(
        fs::read_dir(tmp.path()).unwrap().count(),
        2,
        "no staging left"
    );
}

// ---- lock ----

#[test]
fn lock_file_and_exclusion() {
    let dir = tempfile::tempdir().unwrap();
    let mk = || FsDirDriver::new("data", dir.path().join("d")).with_data_dir(dir.path());
    let (a, b) = (mk(), mk());
    let unlock = a.as_locker().unwrap().lock(&bg()).unwrap();
    assert!(
        dir.path().join("data/.lock").exists(),
        "<data dir>/<name>/.lock"
    );

    let (ctx, _h) = bg().with_timeout(Duration::from_millis(150));
    let err = b.as_locker().unwrap().lock(&ctx).err().expect("held");
    assert_eq!(
        err.downcast_ref::<ContextError>(),
        Some(&ContextError::DeadlineExceeded)
    );
    unlock().unwrap();
    b.as_locker().unwrap().lock(&bg()).unwrap()().unwrap();
}

// ---- end to end ----

fn migrator(dir: &Path, data: &Path, manual: bool) -> Migrator {
    let mut m = Migrator::new("tool", "1.2.0").data_dir(data);
    if manual {
        m = m.manual_rollback();
    }
    let d = dir.to_path_buf();
    m.register(Migration::new("1.1.0", "mydata", move |_| {
        fs::write(d.join("file.txt"), "migrated")?;
        fs::write(d.join("new.txt"), "new")?;
        Ok(())
    }));
    m.register(Migration::new("1.2.0", "mydata", |_| {
        Err::<(), BoxError>("simulated failure".into())
    }));
    m.add_driver(FsDirDriver::new("mydata", dir).with_data_dir(data));
    m
}

fn run_env() -> (tempfile::TempDir, PathBuf, PathBuf) {
    let tmp = tempfile::tempdir().unwrap();
    let (dir, data) = (tmp.path().join("mydata"), tmp.path().join("data"));
    sample(&dir);
    fs::create_dir_all(data.join("mydata")).unwrap();
    fs::write(data.join("mydata/version"), "1.0.0").unwrap();
    (tmp, dir, data)
}

/// A failed run restores the directory from the backup the Migrator took.
#[test]
fn run_failure_restores_the_directory() {
    let (_tmp, dir, data) = run_env();
    let before = tree(&dir);
    let err = migrator(&dir, &data, false).run(&bg()).unwrap_err();
    assert!(err.to_string().contains("simulated failure"), "{err}");
    assert!(!err.is_not_restored(), "{err}");
    assert_eq!(tree(&dir), before);
    assert_eq!(read_version_file_in(&data, "mydata").unwrap(), "1.0.0");
    assert!(data
        .join("backups/mydata/pre-1.1.0/mydata.tar.gz")
        .is_file());
    migrator(&dir, &data, false).run(&bg()).unwrap_err(); // the lock was released
}

#[test]
fn rollback_latest_restores_the_directory() {
    let (_tmp, dir, data) = run_env();
    let before = tree(&dir);
    let mut m = migrator(&dir, &data, true);
    assert!(m.run(&bg()).is_err());
    assert_eq!(read_version_file_in(&data, "mydata").unwrap(), "1.1.0");
    assert_eq!(fs::read_to_string(dir.join("new.txt")).unwrap(), "new");

    m.rollback_latest().unwrap();
    assert_eq!(tree(&dir), before);
    assert_eq!(read_version_file_in(&data, "mydata").unwrap(), "1.0.0");
}
