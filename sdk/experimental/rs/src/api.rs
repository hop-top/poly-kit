use reqwest::Client;
use serde::{Deserialize, Serialize};
use std::fmt;

/// Structured error returned by the API on non-2xx responses.
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
pub struct ApiClient {
    base_url: String,
    client: Client,
    auth_token: Option<String>,
}

impl ApiClient {
    /// Create a new client pointing at the given base URL.
    pub fn new(base_url: &str) -> Self {
        Self {
            base_url: base_url.trim_end_matches('/').to_string(),
            client: Client::new(),
            auth_token: None,
        }
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
        let resp = self
            .request(reqwest::Method::POST, "")
            .json(entity)
            .send()
            .await
            .map_err(|e| transport_error(&e))?;
        parse_response(resp).await
    }

    /// GET /{id} — fetch a single entity.
    pub async fn get<T>(&self, id: &str) -> Result<T, ApiError>
    where
        T: for<'de> Deserialize<'de>,
    {
        let resp = self
            .request(reqwest::Method::GET, &format!("/{id}"))
            .send()
            .await
            .map_err(|e| transport_error(&e))?;
        parse_response(resp).await
    }

    /// GET / — list entities matching the query.
    pub async fn list<T>(&self, q: &Query) -> Result<Vec<T>, ApiError>
    where
        T: for<'de> Deserialize<'de>,
    {
        let resp = self
            .request(reqwest::Method::GET, "")
            .query(q)
            .send()
            .await
            .map_err(|e| transport_error(&e))?;
        parse_response(resp).await
    }

    /// PUT /{id} — update an entity.
    pub async fn update<T>(&self, id: &str, entity: &T) -> Result<T, ApiError>
    where
        T: Serialize + for<'de> Deserialize<'de>,
    {
        let resp = self
            .request(reqwest::Method::PUT, &format!("/{id}"))
            .json(entity)
            .send()
            .await
            .map_err(|e| transport_error(&e))?;
        parse_response(resp).await
    }

    /// DELETE /{id} — remove an entity.
    pub async fn delete(&self, id: &str) -> Result<(), ApiError> {
        let resp = self
            .request(reqwest::Method::DELETE, &format!("/{id}"))
            .send()
            .await
            .map_err(|e| transport_error(&e))?;
        let status = resp.status();
        if status.is_success() {
            return Ok(());
        }
        Err(parse_error(resp).await)
    }

    fn request(&self, method: reqwest::Method, path: &str) -> reqwest::RequestBuilder {
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

async fn parse_response<T: for<'de> Deserialize<'de>>(
    resp: reqwest::Response,
) -> Result<T, ApiError> {
    let status = resp.status();
    if status.is_success() {
        resp.json::<T>().await.map_err(|e| transport_error(&e))
    } else {
        Err(parse_error(resp).await)
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

fn transport_error(e: &reqwest::Error) -> ApiError {
    ApiError {
        status: 0,
        code: "transport_error".into(),
        message: e.to_string(),
    }
}

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
