//! Experimental URI facade for Rust kit consumers.
//!
//! This module delegates to `hop-top-cite`; it does not implement URI parsing or
//! handler generation itself. It is feature-gated because the Rust SDK remains
//! experimental.

pub use hop_top_cite::{
    complete_with_scheme, desktop_filename, parse, parse_with_policy, parse_with_policy_options,
    snippet, ActionRoute, AmbiguousVanityError, CompletionResult, HandlerError, HandlerSpec,
    Language, ParseOptions, Policy, Registry, ResolvedAction, TypeRegistration, Uri, UriError,
    VanityAlias, VanityCandidate,
};

/// Parse a URI with the default `hop-top-cite` policy.
pub fn parse_uri(input: &str) -> Result<Uri, UriError> {
    parse(input)
}

/// Resolve an action route to a command plan without executing it.
pub fn resolve(uri: &Uri, policy: &Policy) -> Result<ResolvedAction, UriError> {
    policy.resolve_action(uri)
}

/// Return the stable OS handler identifier for a handler spec.
pub fn handler_id(spec: &HandlerSpec) -> Result<String, HandlerError> {
    spec.handler_id()
}

/// Render an OS-specific handler snippet.
pub fn handler_snippet(platform: &str, spec: &HandlerSpec) -> Result<String, HandlerError> {
    snippet(platform, spec)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parse_uri_delegates_to_hop_top_cite() {
        let uri = parse_uri("task://hop-top/uri/T-0001").expect("parse uri");
        assert_eq!(uri.scheme, "task");
        assert_eq!(uri.namespace, "hop-top/uri");
        assert_eq!(uri.id, "T-0001");
        assert_eq!(uri.canonical(), "task://hop-top/uri/T-0001");
    }

    #[test]
    fn handler_helpers_delegate_to_hop_top_cite() {
        let spec = HandlerSpec {
            vendor: "hop-top".to_string(),
            app: "tlc".to_string(),
            instance: String::new(),
            language: Language::Rust,
            scheme: "tlc".to_string(),
            version: String::new(),
            channel: String::new(),
            app_path: "/usr/local/bin/tlc".to_string(),
            display_name: String::new(),
        };

        assert_eq!(handler_id(&spec).unwrap(), "hop-top.tlc.rs.tlc");
        assert!(handler_snippet("linux", &spec)
            .unwrap()
            .contains("X-Hop-Handler-ID=hop-top.tlc.rs.tlc"));
    }
}
