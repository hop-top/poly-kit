//! The default data directory follows `$XDG_DATA_HOME`, as Go's
//! `xdg.DataDir("hop/<tool>")`. Its own binary: it sets the environment.

#![cfg(all(feature = "upgrade", not(target_arch = "wasm32")))]

use std::fs;

use hop_top_kit::upgrade::{default_data_dir, read_version_file, Migration, Migrator};
use hop_top_kit::xdg;

#[test]
fn default_data_dir_follows_xdg_data_home() {
    let tmp = tempfile::tempdir().unwrap();
    std::env::set_var("XDG_DATA_HOME", tmp.path());
    assert_eq!(xdg::data_home(), tmp.path());
    assert_eq!(xdg::raw_data_dir("t"), tmp.path().join("t"));
    assert_eq!(
        default_data_dir("mytool"),
        tmp.path().join("hop").join("mytool")
    );

    let mut m = Migrator::new("mytool", "1.0.0");
    m.register(Migration::new("1.0.0", "config", || Ok(())));
    m.add_driver(
        hop_top_kit::upgrade::configfile::ConfigFileDriver::new(["/nonexistent"])
            .with_tool("mytool"),
    );
    m.run().unwrap();
    assert_eq!(read_version_file("mytool", "config").unwrap(), "1.0.0");
    assert!(tmp
        .path()
        .join("hop/mytool/backups/config/pre-1.0.0")
        .is_dir());
    assert!(fs::metadata(tmp.path().join("hop/mytool/config/version")).is_ok());

    std::env::set_var("XDG_DATA_HOME", "relative/ignored");
    assert_ne!(xdg::data_home(), std::path::Path::new("relative/ignored"));
}
