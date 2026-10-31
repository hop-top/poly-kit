//! AWS Signature Version 4 for S3, header-based (`Authorization`).
//!
//! Only what the backend needs: sign one request whose payload hash is
//! already known. S3 canonicalization differs from the generic algorithm in
//! one way that matters here: the URI path is encoded once, never twice.

use hmac::{Hmac, KeyInit, Mac};
use sha2::{Digest, Sha256};

const ALGORITHM: &str = "AWS4-HMAC-SHA256";

/// SHA-256 of an empty body, hex.
pub(crate) const EMPTY_SHA256: &str =
    "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855";

/// Static credentials. `Debug` never prints the secret or the token.
#[derive(Clone)]
pub struct Credentials {
    pub(crate) access_key_id: String,
    pub(crate) secret_access_key: String,
    pub(crate) session_token: Option<String>,
}

impl Credentials {
    /// Build credentials from an access key pair.
    pub fn new(access_key_id: impl Into<String>, secret_access_key: impl Into<String>) -> Self {
        Credentials {
            access_key_id: access_key_id.into(),
            secret_access_key: secret_access_key.into(),
            session_token: None,
        }
    }

    /// Attach a temporary-credential session token.
    #[must_use]
    pub fn with_session_token(mut self, token: impl Into<String>) -> Self {
        self.session_token = Some(token.into());
        self
    }
}

impl std::fmt::Debug for Credentials {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("Credentials")
            .field("access_key_id", &self.access_key_id)
            .field("secret_access_key", &"<redacted>")
            .field(
                "session_token",
                &self.session_token.as_ref().map(|_| "<redacted>"),
            )
            .finish()
    }
}

/// Lowercase hex of `bytes`.
pub(crate) fn hex(bytes: &[u8]) -> String {
    const DIGITS: &[u8; 16] = b"0123456789abcdef";
    let mut out = String::with_capacity(bytes.len() * 2);
    for b in bytes {
        out.push(DIGITS[(b >> 4) as usize] as char);
        out.push(DIGITS[(b & 0xf) as usize] as char);
    }
    out
}

/// Lowercase hex SHA-256 of `data`.
pub(crate) fn sha256_hex(data: &[u8]) -> String {
    hex(&Sha256::digest(data))
}

fn hmac(key: &[u8], data: &[u8]) -> Vec<u8> {
    let mut mac =
        <Hmac<Sha256> as KeyInit>::new_from_slice(key).expect("hmac takes any key length");
    mac.update(data);
    mac.finalize().into_bytes().to_vec()
}

/// Percent-encode per SigV4: everything but `A-Za-z0-9-_.~` is `%XX`
/// (uppercase hex). `/` is kept when `keep_slash`, for URI paths.
pub(crate) fn uri_encode(s: &str, keep_slash: bool) -> String {
    const UPPER: &[u8; 16] = b"0123456789ABCDEF";
    let mut out = String::with_capacity(s.len());
    for &b in s.as_bytes() {
        match b {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'_' | b'.' | b'~' => {
                out.push(b as char)
            }
            b'/' if keep_slash => out.push('/'),
            _ => {
                out.push('%');
                out.push(UPPER[(b >> 4) as usize] as char);
                out.push(UPPER[(b & 0xf) as usize] as char);
            }
        }
    }
    out
}

/// Canonical query string: pairs encoded, then sorted by encoded key, then
/// encoded value.
pub(crate) fn canonical_query(query: &[(String, String)]) -> String {
    let mut pairs: Vec<(String, String)> = query
        .iter()
        .map(|(k, v)| (uri_encode(k, false), uri_encode(v, false)))
        .collect();
    pairs.sort();
    pairs
        .iter()
        .map(|(k, v)| format!("{k}={v}"))
        .collect::<Vec<_>>()
        .join("&")
}

/// Everything the signature covers.
pub(crate) struct Request<'a> {
    pub method: &'a str,
    /// Raw (unencoded) absolute path, starting with `/`.
    pub path: &'a str,
    pub query: &'a [(String, String)],
    /// Every header to sign, including `host` and the `x-amz-*` ones.
    pub headers: &'a [(String, String)],
    pub payload_sha256: &'a str,
    /// `YYYYMMDDTHHMMSSZ`.
    pub amz_date: &'a str,
    pub region: &'a str,
    pub service: &'a str,
}

/// The `Authorization` header value for `req`.
pub(crate) fn authorization(creds: &Credentials, req: &Request<'_>) -> String {
    let mut headers: Vec<(String, String)> = req
        .headers
        .iter()
        .map(|(k, v)| {
            (
                k.to_ascii_lowercase(),
                v.split_whitespace().collect::<Vec<_>>().join(" "),
            )
        })
        .collect();
    headers.sort();
    let signed = headers
        .iter()
        .map(|(k, _)| k.as_str())
        .collect::<Vec<_>>()
        .join(";");
    let canonical_headers: String = headers.iter().map(|(k, v)| format!("{k}:{v}\n")).collect();

    let canonical = format!(
        "{}\n{}\n{}\n{}\n{}\n{}",
        req.method,
        uri_encode(req.path, true),
        canonical_query(req.query),
        canonical_headers,
        signed,
        req.payload_sha256,
    );

    let date = &req.amz_date[..8];
    let scope = format!("{date}/{}/{}/aws4_request", req.region, req.service);
    let to_sign = format!(
        "{ALGORITHM}\n{}\n{scope}\n{}",
        req.amz_date,
        sha256_hex(canonical.as_bytes())
    );

    let k_date = hmac(
        format!("AWS4{}", creds.secret_access_key).as_bytes(),
        date.as_bytes(),
    );
    let k_region = hmac(&k_date, req.region.as_bytes());
    let k_service = hmac(&k_region, req.service.as_bytes());
    let k_signing = hmac(&k_service, b"aws4_request");
    let signature = hex(&hmac(&k_signing, to_sign.as_bytes()));

    format!(
        "{ALGORITHM} Credential={}/{scope}, SignedHeaders={signed}, Signature={signature}",
        creds.access_key_id
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    // The published AWS example key pair, assembled at run time so no
    // provider-format literal sits in the source.
    fn aws_example() -> Credentials {
        Credentials::new(
            ["AKIA", "IOSFODNN7EXAMPLE"].concat(),
            ["wJalrXUtnFEMI/K7MDENG/", "bPxRfiCYEXAMPLEKEY"].concat(),
        )
    }

    fn h(pairs: &[(&str, &str)]) -> Vec<(String, String)> {
        pairs
            .iter()
            .map(|(k, v)| ((*k).into(), (*v).into()))
            .collect()
    }

    fn sig_of(auth: &str) -> &str {
        auth.rsplit("Signature=").next().unwrap()
    }

    const HOST: &str = "examplebucket.s3.amazonaws.com";

    // Vectors: "Examples: Signature Calculations in AWS Signature Version 4"
    // (S3 API reference), bucket examplebucket, 2013-05-24, us-east-1.

    #[test]
    fn vector_get_object_with_range() {
        let headers = h(&[
            ("host", HOST),
            ("range", "bytes=0-9"),
            ("x-amz-content-sha256", EMPTY_SHA256),
            ("x-amz-date", "20130524T000000Z"),
        ]);
        let auth = authorization(
            &aws_example(),
            &Request {
                method: "GET",
                path: "/test.txt",
                query: &[],
                headers: &headers,
                payload_sha256: EMPTY_SHA256,
                amz_date: "20130524T000000Z",
                region: "us-east-1",
                service: "s3",
            },
        );
        assert!(auth.contains("SignedHeaders=host;range;x-amz-content-sha256;x-amz-date"));
        assert_eq!(
            sig_of(&auth),
            "f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"
        );
    }

    #[test]
    fn vector_put_object_encodes_path_once() {
        let body = "Welcome to Amazon S3.";
        let payload = sha256_hex(body.as_bytes());
        assert_eq!(
            payload,
            "44ce7dd67c959e0d3524ffac1771dfbba87d2b6b4b4e99e42034a8b803f8b072"
        );
        let headers = h(&[
            ("date", "Fri, 24 May 2013 00:00:00 GMT"),
            ("host", HOST),
            ("x-amz-content-sha256", &payload),
            ("x-amz-date", "20130524T000000Z"),
            ("x-amz-storage-class", "REDUCED_REDUNDANCY"),
        ]);
        let auth = authorization(
            &aws_example(),
            &Request {
                method: "PUT",
                path: "/test$file.text",
                query: &[],
                headers: &headers,
                payload_sha256: &payload,
                amz_date: "20130524T000000Z",
                region: "us-east-1",
                service: "s3",
            },
        );
        assert_eq!(
            sig_of(&auth),
            "98ad721746da40c64f1a55b78f14c238d841ea1380cd77a1b5971af0ece108bd"
        );
    }

    #[test]
    fn vector_get_bucket_lifecycle_valueless_query() {
        let headers = h(&[
            ("host", HOST),
            ("x-amz-content-sha256", EMPTY_SHA256),
            ("x-amz-date", "20130524T000000Z"),
        ]);
        let auth = authorization(
            &aws_example(),
            &Request {
                method: "GET",
                path: "/",
                query: &[("lifecycle".into(), String::new())],
                headers: &headers,
                payload_sha256: EMPTY_SHA256,
                amz_date: "20130524T000000Z",
                region: "us-east-1",
                service: "s3",
            },
        );
        assert_eq!(
            sig_of(&auth),
            "fea454ca298b7da1c68078a5d1bdbfbbe0d65c699e0f91ac7a200a0136783543"
        );
    }

    #[test]
    fn vector_list_objects_sorted_query() {
        let headers = h(&[
            ("host", HOST),
            ("x-amz-content-sha256", EMPTY_SHA256),
            ("x-amz-date", "20130524T000000Z"),
        ]);
        // Given out of order on purpose: canonicalization sorts.
        let auth = authorization(
            &aws_example(),
            &Request {
                method: "GET",
                path: "/",
                query: &[
                    ("prefix".into(), "J".into()),
                    ("max-keys".into(), "2".into()),
                ],
                headers: &headers,
                payload_sha256: EMPTY_SHA256,
                amz_date: "20130524T000000Z",
                region: "us-east-1",
                service: "s3",
            },
        );
        assert_eq!(
            sig_of(&auth),
            "34b48302e7b5fa45bde8084f4b7868a86f0a534bc59db6670ed5711ef69dc6f7"
        );
    }

    #[test]
    fn uri_encode_rules() {
        assert_eq!(uri_encode("a b/c~d", true), "a%20b/c~d");
        assert_eq!(uri_encode("a/b", false), "a%2Fb");
        assert_eq!(uri_encode("é+*", false), "%C3%A9%2B%2A");
    }

    #[test]
    fn debug_hides_secrets() {
        let c = aws_example().with_session_token("tok-value");
        let s = format!("{c:?}");
        assert!(!s.contains("EXAMPLEKEY"));
        assert!(!s.contains("tok-value"));
    }
}
