use serde::{Deserialize, Serialize};

use crate::error::{bearer, check, url};
use crate::EngineError;

#[derive(Debug, Clone, Deserialize, Serialize, PartialEq)]
pub struct PeerInfo {
    pub id: String,
    #[serde(default)]
    pub name: Option<String>,
    pub address: String,
    pub trusted: bool,
}

/// Peer discovery and trust.
pub struct PeerClient<'a> {
    pub(crate) client: &'a reqwest::Client,
    pub(crate) base: &'a str,
    pub(crate) token: Option<&'a str>,
}

impl PeerClient<'_> {
    pub async fn list(&self) -> Result<Vec<PeerInfo>, EngineError> {
        let resp = check(self.client.get(url(self.base, &["peers"])?).send().await?).await?;
        Ok(resp.json().await?)
    }

    pub async fn trust(&self, id: &str) -> Result<(), EngineError> {
        self.act(id, "trust").await
    }

    pub async fn block(&self, id: &str) -> Result<(), EngineError> {
        self.act(id, "block").await
    }

    async fn act(&self, id: &str, verb: &str) -> Result<(), EngineError> {
        let rb = self.client.post(url(self.base, &["peers", id, verb])?);
        check(bearer(rb, self.token).send().await?).await?;
        Ok(())
    }
}
