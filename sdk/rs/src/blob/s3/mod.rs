//! S3 blob backend (feature `blob-s3`), a port of Go's
//! `hop.top/kit/go/storage/blob/s3`.
//!
//! [`S3Store`] speaks the S3 REST API directly: SigV4-signed `PutObject`,
//! `GetObject`, `HeadObject`, `DeleteObject` and `ListObjectsV2`, issued
//! through the netpolicy [`GuardedClient`](crate::netpolicy::GuardedClient)
//! so `--offline` and the host allowlist apply. It works against AWS and
//! against S3-compatible servers (LocalStack, MinIO) via an endpoint
//! override with path-style addressing.
//!
//! ```no_run
//! use std::io::Read;
//! use hop_top_kit::blob::s3::{S3Config, S3Store};
//! use hop_top_kit::blob::Store;
//! use hop_top_kit::netpolicy::NetPolicy;
//!
//! # fn main() -> Result<(), hop_top_kit::blob::BlobError> {
//! let cfg = S3Config::from_env("my-bucket")?.with_prefix("tool-data");
//! let store = S3Store::new(cfg, NetPolicy::online())?;
//! store.put("notes/a.txt", "hello".as_bytes(), "text/plain")?;
//! let mut body = String::new();
//! store.get("notes/a.txt")?.read_to_string(&mut body).unwrap();
//! # Ok(()) }
//! ```
//!
//! # Semantics (same as Go)
//!
//! - Keys live under `prefix` (trailing slashes ignored): the S3 key is the
//!   prefix joined with the cleaned key. `list` strips the prefix again and
//!   matches by string prefix, so `"logs/"` excludes `"logs-old/x"`.
//! - Content type is the object's native `Content-Type`. `list` cannot
//!   report it (`ListObjectsV2` carries none), so it is empty there; use
//!   [`Stater::stat`](crate::blob::Stater) (one `HeadObject`). A `put` with
//!   no type is stored as S3's default and reported as such by `stat`.
//! - `delete` of a missing key succeeds; `exists` is `false` without error
//!   for a missing key; `get` and `stat` of one are
//!   [`BlobError::NotFound`].
//!
//! # Differences from Go
//!
//! - Credentials come from [`S3Config`] (explicit, or the standard
//!   `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` / `AWS_SESSION_TOKEN` /
//!   `AWS_REGION` / `AWS_ENDPOINT_URL_S3` variables). There is no
//!   shared-config, profile, SSO or instance-metadata chain: Go gets one
//!   from the AWS SDK; here a caller needing it resolves the credentials
//!   itself and passes them in.
//! - `put` and `get` buffer the whole object in memory (SigV4 needs the
//!   payload hash up front, and [`Store::get`]
//!   returns an owned reader). A single `PutObject` is capped by S3 at
//!   5 GiB; there is no multipart upload.
//! - A key whose `..` segments climb out of the prefix is rejected with
//!   [`BlobError::KeyEscapesRoot`];
//!   Go's `path.Join` would silently address a sibling key.
//!
//! The backend is synchronous like the rest of [`crate::blob`]; see
//! [`GuardedTransport`] for how that sits on the async client.

mod sigv4;
mod transport;
mod xml;

use std::io::{Cursor, Read};
use std::sync::Arc;
use std::time::{SystemTime, UNIX_EPOCH};

use super::{BlobError, Object, Stater, Store};
use crate::netpolicy::NetPolicy;

pub use sigv4::Credentials;
pub use transport::{GuardedTransport, HttpRequest, HttpResponse, Transport, TransportError};

use sigv4::{authorization, sha256_hex, uri_encode, Request, EMPTY_SHA256};

/// Where and as whom an [`S3Store`] talks to S3.
#[derive(Debug, Clone)]
pub struct S3Config {
    bucket: String,
    prefix: String,
    region: String,
    endpoint: Option<String>,
    path_style: bool,
    credentials: Credentials,
}

impl S3Config {
    /// Config for `bucket` in `region` with explicit `credentials`: AWS
    /// endpoint, virtual-hosted addressing, empty prefix.
    pub fn new(
        bucket: impl Into<String>,
        region: impl Into<String>,
        credentials: Credentials,
    ) -> Self {
        S3Config {
            bucket: bucket.into(),
            prefix: String::new(),
            region: region.into(),
            endpoint: None,
            path_style: false,
            credentials,
        }
    }

    /// Config for `bucket` from the standard AWS environment variables.
    ///
    /// Reads `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY` (required),
    /// `AWS_SESSION_TOKEN`, `AWS_REGION` or `AWS_DEFAULT_REGION` (required),
    /// and `AWS_ENDPOINT_URL_S3` or `AWS_ENDPOINT_URL`, which selects
    /// path-style addressing. Nothing else is consulted.
    pub fn from_env(bucket: impl Into<String>) -> Result<Self, BlobError> {
        Self::from_lookup(bucket, |k| std::env::var(k).ok())
    }

    /// [`S3Config::from_env`] over an arbitrary variable lookup.
    pub fn from_lookup(
        bucket: impl Into<String>,
        get: impl Fn(&str) -> Option<String>,
    ) -> Result<Self, BlobError> {
        let var = |k: &str| get(k).filter(|v| !v.is_empty());
        let need =
            |k: &str| var(k).ok_or_else(|| BlobError::InvalidConfig(format!("{k} is not set")));
        let mut creds =
            Credentials::new(need("AWS_ACCESS_KEY_ID")?, need("AWS_SECRET_ACCESS_KEY")?);
        if let Some(t) = var("AWS_SESSION_TOKEN") {
            creds = creds.with_session_token(t);
        }
        let region = var("AWS_REGION")
            .or_else(|| var("AWS_DEFAULT_REGION"))
            .ok_or_else(|| BlobError::InvalidConfig("AWS_REGION is not set".into()))?;
        let mut cfg = S3Config::new(bucket, region, creds);
        if let Some(ep) = var("AWS_ENDPOINT_URL_S3").or_else(|| var("AWS_ENDPOINT_URL")) {
            cfg = cfg.with_endpoint(ep);
        }
        Ok(cfg)
    }

    /// Store objects under `prefix`. Trailing slashes are ignored, so
    /// `"tool-data"` and `"tool-data/"` address the same objects.
    #[must_use]
    pub fn with_prefix(mut self, prefix: impl AsRef<str>) -> Self {
        self.prefix = prefix.as_ref().trim_end_matches('/').to_string();
        self
    }

    /// Talk to `endpoint` (`http://localhost:4566`) instead of AWS, and
    /// switch to path-style addressing, which S3-compatible servers need.
    /// Follow with [`S3Config::with_path_style`]`(false)` for a server that
    /// does virtual-hosted addressing.
    #[must_use]
    pub fn with_endpoint(mut self, endpoint: impl Into<String>) -> Self {
        self.endpoint = Some(endpoint.into());
        self.path_style = true;
        self
    }

    /// Choose path-style (`host/bucket/key`) or virtual-hosted
    /// (`bucket.host/key`) addressing.
    #[must_use]
    pub fn with_path_style(mut self, path_style: bool) -> Self {
        self.path_style = path_style;
        self
    }
}

/// Resolved addressing: scheme, authority (the `Host` header), and the path
/// that precedes the key.
#[derive(Debug, Clone)]
struct Target {
    scheme: String,
    authority: String,
    path_prefix: String,
}

impl Target {
    fn resolve(cfg: &S3Config) -> Result<Target, BlobError> {
        let bad = |m: String| BlobError::InvalidConfig(m);
        if cfg.bucket.is_empty() {
            return Err(bad("bucket is empty".into()));
        }
        if cfg.region.is_empty() {
            return Err(bad("region is empty".into()));
        }
        let (scheme, host, port) = match &cfg.endpoint {
            Some(ep) => {
                let u = reqwest::Url::parse(ep).map_err(|e| bad(format!("endpoint: {e}")))?;
                if !matches!(u.scheme(), "http" | "https") {
                    return Err(bad(format!(
                        "endpoint scheme {:?} is not http or https",
                        u.scheme()
                    )));
                }
                if !matches!(u.path(), "" | "/") || u.query().is_some() || u.fragment().is_some() {
                    return Err(bad(
                        "endpoint must not carry a path, query or fragment".into()
                    ));
                }
                let host = u
                    .host_str()
                    .ok_or_else(|| bad("endpoint has no host".into()))?;
                (u.scheme().to_string(), host.to_string(), u.port())
            }
            None => (
                "https".to_string(),
                format!("s3.{}.amazonaws.com", cfg.region),
                None,
            ),
        };
        let host = if cfg.path_style {
            host
        } else {
            format!("{}.{host}", cfg.bucket)
        };
        let authority = match port {
            Some(p) => format!("{host}:{p}"),
            None => host,
        };
        let path_prefix = if cfg.path_style {
            format!("/{}", cfg.bucket)
        } else {
            String::new()
        };
        Ok(Target {
            scheme,
            authority,
            path_prefix,
        })
    }
}

/// S3-backed [`Store`]. See the [module docs](self).
#[derive(Debug, Clone)]
pub struct S3Store {
    cfg: S3Config,
    target: Target,
    transport: Arc<dyn Transport>,
}

impl S3Store {
    /// Store over the guarded HTTP client: every request is judged by
    /// `policy` first, so an offline policy refuses a non-loopback endpoint.
    pub fn new(cfg: S3Config, policy: NetPolicy) -> Result<Self, BlobError> {
        Self::with_transport(cfg, Arc::new(GuardedTransport::new(policy)?))
    }

    /// Store over a caller-supplied [`Transport`] (proxy, test double,
    /// record/replay layer). The caller owns policy enforcement.
    pub fn with_transport(cfg: S3Config, transport: Arc<dyn Transport>) -> Result<Self, BlobError> {
        let target = Target::resolve(&cfg)?;
        Ok(S3Store {
            cfg,
            target,
            transport,
        })
    }

    /// Full S3 key for a caller key: prefix joined with the cleaned key
    /// (Go `path.Join(prefix, key)`), refusing keys that climb out of the
    /// prefix or name nothing.
    fn key(&self, key: &str) -> Result<String, BlobError> {
        let mut segs: Vec<&str> = Vec::new();
        for seg in key.split('/') {
            match seg {
                "" | "." => {}
                ".." => {
                    if segs.pop().is_none() {
                        return Err(BlobError::KeyEscapesRoot(key.to_string()));
                    }
                }
                s => segs.push(s),
            }
        }
        if segs.is_empty() {
            return Err(BlobError::KeyEscapesRoot(key.to_string()));
        }
        let joined = segs.join("/");
        Ok(if self.cfg.prefix.is_empty() {
            joined
        } else {
            format!("{}/{joined}", self.cfg.prefix)
        })
    }

    /// S3 prefix for a caller `List` prefix. Unlike [`S3Store::key`] it is
    /// not cleaned and keeps a trailing slash: `"logs/"` must not match
    /// `"logs-old/x"`.
    fn list_prefix(&self, prefix: &str) -> String {
        if self.cfg.prefix.is_empty() {
            prefix.to_string()
        } else {
            format!("{}/{prefix}", self.cfg.prefix)
        }
    }

    /// Caller key for a full object key.
    fn caller_key(&self, full: &str) -> String {
        if self.cfg.prefix.is_empty() {
            return full.to_string();
        }
        let p = format!("{}/", self.cfg.prefix);
        full.strip_prefix(&p).unwrap_or(full).to_string()
    }

    /// Build and sign a request. `key` is a full object key, or `None` for
    /// the bucket itself.
    fn request(
        &self,
        method: &str,
        key: Option<&str>,
        query: &[(String, String)],
        body: Vec<u8>,
        content_type: &str,
        amz_date: &str,
    ) -> HttpRequest {
        let path = match key {
            Some(k) => format!("{}/{k}", self.target.path_prefix),
            None if self.target.path_prefix.is_empty() => "/".to_string(),
            None => self.target.path_prefix.clone(),
        };
        let payload = if body.is_empty() {
            EMPTY_SHA256.to_string()
        } else {
            sha256_hex(&body)
        };

        let mut headers = vec![
            ("x-amz-content-sha256".to_string(), payload.clone()),
            ("x-amz-date".to_string(), amz_date.to_string()),
        ];
        if let Some(t) = &self.cfg.credentials.session_token {
            headers.push(("x-amz-security-token".into(), t.clone()));
        }
        if !content_type.is_empty() {
            headers.push(("content-type".into(), content_type.to_string()));
        }
        let mut signed = headers.clone();
        signed.push(("host".into(), self.target.authority.clone()));
        headers.push((
            "authorization".into(),
            authorization(
                &self.cfg.credentials,
                &Request {
                    method,
                    path: &path,
                    query,
                    headers: &signed,
                    payload_sha256: &payload,
                    amz_date,
                    region: &self.cfg.region,
                    service: "s3",
                },
            ),
        ));

        let mut url = format!(
            "{}://{}{}",
            self.target.scheme,
            self.target.authority,
            uri_encode(&path, true)
        );
        if !query.is_empty() {
            let q: Vec<String> = query
                .iter()
                .map(|(k, v)| format!("{}={}", uri_encode(k, false), uri_encode(v, false)))
                .collect();
            url.push('?');
            url.push_str(&q.join("&"));
        }
        HttpRequest {
            method: method.to_string(),
            url,
            headers,
            body,
        }
    }

    fn send(
        &self,
        op: &'static str,
        target: &str,
        req: HttpRequest,
    ) -> Result<HttpResponse, BlobError> {
        self.transport
            .send(req)
            .map_err(|source| BlobError::Remote {
                op,
                target: target.to_string(),
                source,
            })
    }

    /// Fail unless `resp` is 2xx.
    fn ok(op: &'static str, target: &str, resp: HttpResponse) -> Result<HttpResponse, BlobError> {
        if (200..300).contains(&resp.status) {
            return Ok(resp);
        }
        Err(BlobError::Remote {
            op,
            target: target.to_string(),
            source: Box::new(ResponseError::new(&resp)),
        })
    }

    fn head(&self, op: &'static str, key: &str) -> Result<(String, HttpResponse), BlobError> {
        let full = self.key(key)?;
        let req = self.request("HEAD", Some(&full), &[], Vec::new(), "", &amz_now());
        Ok((key.to_string(), self.send(op, key, req)?))
    }
}

/// A non-2xx S3 answer: status plus the `Code` and `Message` of its error
/// body when it has one. Reachable as the source of
/// [`BlobError::Remote`].
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ResponseError {
    /// HTTP status code.
    pub status: u16,
    /// S3 error code (`AccessDenied`, `NoSuchBucket`, …); empty for a
    /// bodiless answer such as a `HEAD` 403.
    pub code: String,
    /// S3 error message, possibly empty.
    pub message: String,
}

impl ResponseError {
    fn new(resp: &HttpResponse) -> Self {
        let (code, message) = xml::parse_error(&String::from_utf8_lossy(&resp.body));
        ResponseError {
            status: resp.status,
            code,
            message,
        }
    }
}

impl std::fmt::Display for ResponseError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "status {}", self.status)?;
        if !self.code.is_empty() {
            write!(f, ": {}", self.code)?;
        }
        if !self.message.is_empty() {
            write!(f, ": {}", self.message)?;
        }
        Ok(())
    }
}

impl std::error::Error for ResponseError {}

impl Store for S3Store {
    type Reader = Cursor<Vec<u8>>;

    fn put<R: Read>(&self, key: &str, mut r: R, content_type: &str) -> Result<(), BlobError> {
        let full = self.key(key)?;
        let mut body = Vec::new();
        r.read_to_end(&mut body)
            .map_err(|e| BlobError::io("read body", e))?;
        let req = self.request("PUT", Some(&full), &[], body, content_type, &amz_now());
        let resp = self.send("put", key, req)?;
        Self::ok("put", key, resp).map(|_| ())
    }

    fn get(&self, key: &str) -> Result<Self::Reader, BlobError> {
        let full = self.key(key)?;
        let req = self.request("GET", Some(&full), &[], Vec::new(), "", &amz_now());
        let resp = self.send("get", key, req)?;
        if resp.status == 404 && ResponseError::new(&resp).code == "NoSuchKey" {
            return Err(BlobError::NotFound(key.to_string()));
        }
        Self::ok("get", key, resp).map(|r| Cursor::new(r.body))
    }

    fn delete(&self, key: &str) -> Result<(), BlobError> {
        let full = self.key(key)?;
        let req = self.request("DELETE", Some(&full), &[], Vec::new(), "", &amz_now());
        let resp = self.send("delete", key, req)?;
        Self::ok("delete", key, resp).map(|_| ())
    }

    fn list(&self, prefix: &str) -> Result<Vec<Object>, BlobError> {
        let full = self.list_prefix(prefix);
        let mut objects = Vec::new();
        let mut token: Option<String> = None;
        loop {
            let mut query = vec![
                ("encoding-type".to_string(), "url".to_string()),
                ("list-type".to_string(), "2".to_string()),
                ("prefix".to_string(), full.clone()),
            ];
            if let Some(t) = &token {
                query.push(("continuation-token".into(), t.clone()));
            }
            let req = self.request("GET", None, &query, Vec::new(), "", &amz_now());
            let resp = self.send("list", prefix, req)?;
            let resp = Self::ok("list", prefix, resp)?;
            let page = xml::parse_list(&String::from_utf8_lossy(&resp.body)).map_err(|m| {
                BlobError::Remote {
                    op: "list",
                    target: prefix.to_string(),
                    source: m.into(),
                }
            })?;
            objects.extend(page.entries.into_iter().map(|e| Object {
                key: self.caller_key(&e.key),
                size: e.size,
                content_type: String::new(),
            }));
            match (page.truncated, page.next_token) {
                (true, Some(t)) => token = Some(t),
                _ => return Ok(objects),
            }
        }
    }

    fn exists(&self, key: &str) -> Result<bool, BlobError> {
        let (key, resp) = self.head("exists", key)?;
        if resp.status == 404 {
            return Ok(false);
        }
        Self::ok("exists", &key, resp).map(|_| true)
    }
}

impl Stater for S3Store {
    fn stat(&self, key: &str) -> Result<Object, BlobError> {
        let (key, resp) = self.head("stat", key)?;
        if resp.status == 404 {
            return Err(BlobError::NotFound(key));
        }
        let resp = Self::ok("stat", &key, resp)?;
        let size = resp
            .header("content-length")
            .and_then(|v| v.trim().parse().ok())
            .unwrap_or(0);
        Ok(Object {
            key,
            size,
            content_type: resp.header("content-type").unwrap_or("").to_string(),
        })
    }
}

/// Now as `YYYYMMDDTHHMMSSZ`.
fn amz_now() -> String {
    let secs = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |d| d.as_secs());
    amz_date(secs)
}

/// Unix seconds as `YYYYMMDDTHHMMSSZ` (UTC; civil-from-days, Hinnant).
fn amz_date(secs: u64) -> String {
    let days = (secs / 86_400) as i64;
    let rem = secs % 86_400;
    let z = days + 719_468;
    let era = z.div_euclid(146_097);
    let doe = z.rem_euclid(146_097);
    let yoe = (doe - doe / 1460 + doe / 36_524 - doe / 146_096) / 365;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let d = doy - (153 * mp + 2) / 5 + 1;
    let m = if mp < 10 { mp + 3 } else { mp - 9 };
    let y = yoe + era * 400 + i64::from(m <= 2);
    format!(
        "{y:04}{m:02}{d:02}T{:02}{:02}{:02}Z",
        rem / 3600,
        rem % 3600 / 60,
        rem % 60
    )
}

#[cfg(test)]
mod tests;
