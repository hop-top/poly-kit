use crate::netpolicy::{destination, GuardedClient, NetError, NetPolicy, RequestBuilder};
use reqwest::Method;
use serde::{Deserialize, Serialize};
use std::fmt;

/// Structured error returned by [`ApiClient`].
///
/// On a non-2xx response it is the API's own error body (or a fallback
/// carrying the status). When no response is decoded, `status` is 0 and
/// `code` says why: [`OFFLINE_CODE`] for a request refused by the
/// `--offline` policy, `"transport_error"` for one attempted and failed
/// (connect, send, body decode). In both cases `message` names the
/// destination as `METHOD scheme://host[:port]path` only; query, fragment
/// and userinfo never appear, as they may carry credentials.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ApiError {
    pub status: u16,
    pub code: String,
    pub message: String,
}

impl fmt::Display for ApiError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{} ({}): {}", self.status, self.code, self.message)
    }
}

impl std::error::Error for ApiError {}

/// Query parameters for list endpoints.
#[derive(Debug, Default, Serialize)]
pub struct Query {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub limit: Option<u32>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub offset: Option<u32>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub sort: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub search: Option<String>,
}

/// HTTP client for the kit REST API.
///
/// Requests are issued through [`GuardedClient`], so the `--offline`
/// policy is enforced beneath every method here — a caller does not opt
/// in and cannot opt out.
pub struct ApiClient {
    base_url: String,
    client: GuardedClient,
    auth_token: Option<String>,
}

impl ApiClient {
    /// Create a new client pointing at the given base URL, permitting
    /// network access.
    ///
    /// # Panics
    ///
    /// Panics if the TLS backend cannot be initialised, matching
    /// `reqwest::Client::new`. Use [`ApiClient::with_policy`] to handle
    /// that failure.
    pub fn new(base_url: &str) -> Self {
        Self::with_policy(base_url, NetPolicy::default()).expect("build http client")
    }

    /// Create a new client under an explicit network policy. This is
    /// the constructor the CLI layer calls once it registers
    /// `--offline`: `ApiClient::with_policy(url, NetPolicy::new(offline))`.
    pub fn with_policy(base_url: &str, policy: NetPolicy) -> Result<Self, reqwest::Error> {
        Ok(Self {
            base_url: base_url.trim_end_matches('/').to_string(),
            client: GuardedClient::new(policy)?,
            auth_token: None,
        })
    }

    /// Set a bearer token for authenticated requests.
    pub fn with_auth(mut self, token: &str) -> Self {
        self.auth_token = Some(token.to_string());
        self
    }

    /// POST / — create an entity.
    pub async fn create<T>(&self, entity: &T) -> Result<T, ApiError>
    where
        T: Serialize + for<'de> Deserialize<'de>,
    {
        self.call(Method::POST, "", |r| r.json(entity)).await
    }

    /// GET /{id} — fetch a single entity.
    pub async fn get<T>(&self, id: &str) -> Result<T, ApiError>
    where
        T: for<'de> Deserialize<'de>,
    {
        self.call(Method::GET, &format!("/{id}"), |r| r).await
    }

    /// GET / — list entities matching the query.
    pub async fn list<T>(&self, q: &Query) -> Result<Vec<T>, ApiError>
    where
        T: for<'de> Deserialize<'de>,
    {
        self.call(Method::GET, "", |r| r.query(q)).await
    }

    /// PUT /{id} — update an entity.
    pub async fn update<T>(&self, id: &str, entity: &T) -> Result<T, ApiError>
    where
        T: Serialize + for<'de> Deserialize<'de>,
    {
        self.call(Method::PUT, &format!("/{id}"), |r| r.json(entity))
            .await
    }

    /// DELETE /{id} — remove an entity.
    pub async fn delete(&self, id: &str) -> Result<(), ApiError> {
        let resp = self.send(&Method::DELETE, &format!("/{id}"), |r| r).await?;
        if resp.status().is_success() {
            return Ok(());
        }
        Err(parse_error(resp).await)
    }

    /// Issue a request and decode a 2xx JSON body into `T`.
    async fn call<T>(
        &self,
        method: Method,
        path: &str,
        build: impl FnOnce(RequestBuilder) -> RequestBuilder,
    ) -> Result<T, ApiError>
    where
        T: for<'de> Deserialize<'de>,
    {
        let resp = self.send(&method, path, build).await?;
        if resp.status().is_success() {
            resp.json::<T>()
                .await
                .map_err(|e| transport_error(&method, e))
        } else {
            Err(parse_error(resp).await)
        }
    }

    /// Issue a request through the guard, mapping any failure to
    /// `ApiError`.
    async fn send(
        &self,
        method: &Method,
        path: &str,
        build: impl FnOnce(RequestBuilder) -> RequestBuilder,
    ) -> Result<reqwest::Response, ApiError> {
        build(self.request(method.clone(), path))
            .send()
            .await
            .map_err(|e| net_error(method, e))
    }

    fn request(&self, method: Method, path: &str) -> RequestBuilder {
        let url = format!("{}{}", self.base_url, path);
        let mut req = self
            .client
            .request(method, &url)
            .header("Content-Type", "application/json");
        if let Some(ref token) = self.auth_token {
            req = req.bearer_auth(token);
        }
        req
    }
}

async fn parse_error(resp: reqwest::Response) -> ApiError {
    let status = resp.status().as_u16();
    resp.json::<ApiError>().await.unwrap_or_else(|_| ApiError {
        status,
        code: "unknown".into(),
        message: format!("request failed with status {status}"),
    })
}

/// Map a reqwest failure onto `ApiError`. reqwest's own message quotes
/// the full request URL (query and fragment included), so the URL is
/// taken off the error and named through [`destination`] instead, the
/// same rule as the offline refusal.
fn transport_error(method: &Method, e: reqwest::Error) -> ApiError {
    let message = match e.url().map(destination) {
        Some(dest) => format!("{method} {dest}: {}", e.without_url()),
        None => format!("{method}: {e}"),
    };
    ApiError {
        status: 0,
        code: "transport_error".into(),
        message,
    }
}

/// Map a guarded-client failure onto `ApiError`. An offline refusal
/// keeps its own code so callers can branch on it without parsing the
/// message; anything else is an ordinary transport failure.
fn net_error(method: &Method, e: NetError) -> ApiError {
    match e {
        NetError::Offline(o) => ApiError {
            status: 0,
            code: OFFLINE_CODE.into(),
            message: o.to_string(),
        },
        NetError::Transport(t) => transport_error(method, t),
    }
}

/// `ApiError::code` set when a request was refused by the `--offline`
/// policy rather than attempted and failed.
pub const OFFLINE_CODE: &str = "offline";

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn api_error_display() {
        let err = ApiError {
            status: 404,
            code: "not_found".into(),
            message: "not found".into(),
        };
        assert_eq!(err.to_string(), "404 (not_found): not found");
    }

    #[test]
    fn api_error_is_std_error() {
        let err: Box<dyn std::error::Error> = Box::new(ApiError {
            status: 500,
            code: "internal_error".into(),
            message: "boom".into(),
        });
        assert!(err.to_string().contains("boom"));
    }

    #[test]
    fn query_default_is_empty() {
        let q = Query::default();
        assert!(q.limit.is_none());
        assert!(q.offset.is_none());
        assert!(q.sort.is_none());
        assert!(q.search.is_none());
    }

    #[test]
    fn with_auth_sets_token() {
        let client = ApiClient::new("http://localhost").with_auth("tok123");
        assert_eq!(client.auth_token.as_deref(), Some("tok123"));
    }

    #[test]
    fn base_url_trims_trailing_slash() {
        let client = ApiClient::new("http://localhost:8080/");
        assert_eq!(client.base_url, "http://localhost:8080");
    }
}
