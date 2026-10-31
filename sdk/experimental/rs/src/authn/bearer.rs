use super::error::AuthnError;

/// The token of an `Authorization: Bearer` header value (RFC 6750
/// section 2.1; the scheme is case-insensitive). A missing or empty
/// header is [`AuthnError::NoToken`] without detail; any other scheme
/// is the same refusal with the reason.
pub fn bearer_token(authorization: Option<&str>) -> Result<&str, AuthnError> {
    let h = authorization.unwrap_or("").trim();
    if h.is_empty() {
        return Err(AuthnError::NoToken(None));
    }
    match h.split_once(' ') {
        Some((scheme, tok)) if scheme.eq_ignore_ascii_case("bearer") && !tok.trim().is_empty() => {
            Ok(tok.trim())
        }
        _ => Err(AuthnError::NoToken(Some(
            "the Authorization header is not a bearer token".into(),
        ))),
    }
}
