//! The `<PROGRAM>_COMPLETION_DESCRIPTIONS` switch. In its own test
//! binary: it sets a process environment variable, which no other test
//! in the same process may race.

#![cfg(feature = "cli")]

use clap::Command;
use hop_top_kit::cli::Root;

#[test]
fn completion_descriptions_env_turns_descriptions_off() {
    let root = Root::new(
        Command::new("envtool-x").subcommand(Command::new("deploy").about("Deploy a target")),
    );
    let complete = || {
        let (mut out, mut err) = (Vec::new(), Vec::new());
        root.complete(&["d".to_string()], false, &mut out, &mut err)
            .unwrap();
        String::from_utf8(out).unwrap()
    };
    assert_eq!(complete(), "deploy\tDeploy a target\n:4\n");
    std::env::set_var("ENVTOOL_X_COMPLETION_DESCRIPTIONS", "false");
    let off = complete();
    std::env::set_var("ENVTOOL_X_COMPLETION_DESCRIPTIONS", "");
    std::env::set_var("COBRA_COMPLETION_DESCRIPTIONS", "0");
    let cobra_off = complete();
    std::env::remove_var("ENVTOOL_X_COMPLETION_DESCRIPTIONS");
    std::env::remove_var("COBRA_COMPLETION_DESCRIPTIONS");
    assert_eq!(off, "deploy\n:4\n");
    assert_eq!(cobra_off, "deploy\n:4\n");
}
