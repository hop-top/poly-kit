#![cfg(feature = "http")]

use hop_top_kit_engine::EngineError;

#[test]
fn error_reports_status_and_message() {
    let e = EngineError::Status {
        status: 404,
        message: "not found".into(),
    };
    assert_eq!(e.to_string(), "engine returned 404: not found");
}

#[test]
fn version_conflict_is_distinct() {
    let e = EngineError::VersionConflict("no such version".into());
    assert!(matches!(e, EngineError::VersionConflict(_)));
    assert_eq!(e.to_string(), "version conflict: no such version");
}
