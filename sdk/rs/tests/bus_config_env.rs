//! The real process-environment reader. Kept in its own integration test
//! so it is the only test in its binary: `set_var` mutates global state
//! and must not run beside other threads reading the environment.

#![cfg(feature = "bus")]

use hop_top_kit::bus::{mode_from_env, Mode, ENV_ENFORCE};

#[test]
fn real_env_reader() {
    std::env::set_var(ENV_ENFORCE, "strict");
    assert_eq!(mode_from_env(), Mode::Strict);
    std::env::set_var(ENV_ENFORCE, "bogus");
    assert_eq!(mode_from_env(), Mode::Warn);
    std::env::remove_var(ENV_ENFORCE);
    assert_eq!(mode_from_env(), Mode::Warn);
}
