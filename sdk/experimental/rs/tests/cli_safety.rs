//! Factor 10 delegation safety: `--force` guard (sdk/ts `safety.ts`,
//! sdk/py `safety.py`).

#![cfg(feature = "cli")]

use hop_top_kit::cli::safety::{safety_guard, SafetyLevel, SafetyOpts};
use hop_top_kit::output::{CliError, EXIT_UNAUTHORIZED};

fn opts(force: bool, tty: bool) -> SafetyOpts {
    SafetyOpts { force, tty }
}

#[test]
fn read_always_passes() {
    for tty in [true, false] {
        assert!(safety_guard(SafetyLevel::Read, opts(false, tty)).is_ok());
    }
}

#[test]
fn caution_passes_on_a_tty_and_needs_force_off_it() {
    assert!(safety_guard(SafetyLevel::Caution, opts(false, true)).is_ok());
    let e = safety_guard(SafetyLevel::Caution, opts(false, false)).unwrap_err();
    assert!(e.to_string().contains("--force"), "{e}");
    assert!(e.to_string().contains("non-TTY"), "{e}");
    assert!(safety_guard(SafetyLevel::Caution, opts(true, false)).is_ok());
}

#[test]
fn dangerous_always_needs_force() {
    for tty in [true, false] {
        let e = safety_guard(SafetyLevel::Dangerous, opts(false, tty)).unwrap_err();
        assert_eq!(e.to_string(), "dangerous operation requires --force");
        assert!(safety_guard(SafetyLevel::Dangerous, opts(true, tty)).is_ok());
    }
}

#[test]
fn a_refusal_is_an_unauthorized_envelope() {
    let e = safety_guard(SafetyLevel::Dangerous, opts(false, true)).unwrap_err();
    let ce: CliError = e.into();
    assert_eq!(ce.exit_code, EXIT_UNAUTHORIZED);
}

#[test]
fn levels_parse_from_their_names() {
    assert_eq!("read".parse(), Ok(SafetyLevel::Read));
    assert_eq!("caution".parse(), Ok(SafetyLevel::Caution));
    assert_eq!("dangerous".parse(), Ok(SafetyLevel::Dangerous));
    assert!("risky".parse::<SafetyLevel>().is_err());
}

#[test]
fn detect_reads_the_force_flag_and_stdout() {
    let o = SafetyOpts::detect(true);
    assert!(o.force);
}
