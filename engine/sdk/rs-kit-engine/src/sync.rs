use serde::{Deserialize, Serialize};

use crate::error::{bearer, check, url};
use crate::EngineError;

/// One configured sync remote plus its live status fields.
#[derive(Debug, Clone, Deserialize, Serialize, PartialEq)]
pub struct RemoteStatus {
    pub name: String,
    pub url: String,
    pub mode: String,
    #[serde(default)]
    pub filter: Option<String>,
    #[serde(default)]
    pub connected: Option<bool>,
    #[serde(default)]
    pub last_sync: Option<String>,
    #[serde(default)]
    pub pending_diffs: Option<u64>,
    #[serde(default)]
    pub last_error: Option<String>,
    #[serde(default)]
    pub lag_ms: Option<u64>,
}

/// A registered remote as echoed by `POST /sync/remotes`.
#[derive(Debug, Clone, Deserialize, Serialize, PartialEq)]
pub struct RemoteConfig {
    pub name: String,
    pub url: String,
    #[serde(default)]
    pub mode: Option<String>,
    #[serde(default)]
    pub filter: Option<String>,
}

/// Sync remote management.
pub struct SyncClient<'a> {
    pub(crate) client: &'a reqwest::Client,
    pub(crate) base: &'a str,
    pub(crate) token: Option<&'a str>,
}

impl SyncClient<'_> {
    /// `mode` is `push`, `pull` or `both`.
    pub async fn add_remote(
        &self,
        name: &str,
        remote_url: &str,
        mode: &str,
        filter: &str,
    ) -> Result<RemoteConfig, EngineError> {
        let rb = self
            .client
            .post(url(self.base, &["sync", "remotes"])?)
            .json(&serde_json::json!({"name": name, "url": remote_url, "mode": mode, "filter": filter}));
        let resp = check(bearer(rb, self.token).send().await?).await?;
        Ok(resp.json().await?)
    }

    pub async fn remove_remote(&self, name: &str) -> Result<(), EngineError> {
        let rb = self
            .client
            .delete(url(self.base, &["sync", "remotes", name])?);
        check(bearer(rb, self.token).send().await?).await?;
        Ok(())
    }

    pub async fn status(&self) -> Result<Vec<RemoteStatus>, EngineError> {
        #[derive(Deserialize)]
        struct Payload {
            remotes: Vec<RemoteStatus>,
        }
        let resp = check(
            self.client
                .get(url(self.base, &["sync", "status"])?)
                .send()
                .await?,
        )
        .await?;
        Ok(resp.json::<Payload>().await?.remotes)
    }
}
