use serde::{Deserialize, Serialize};

use crate::error::{bearer, check, url};
use crate::EngineError;

#[derive(Debug, Clone, Deserialize, Serialize, PartialEq)]
pub struct PublicKeyInfo {
    #[serde(default)]
    pub id: Option<String>,
    pub public_key: String,
    pub fingerprint: String,
}

#[derive(Debug, Clone, Copy, Deserialize, Serialize, PartialEq)]
pub struct VerifyResult {
    pub valid: bool,
}

/// The engine's identity key.
pub struct IdentityClient<'a> {
    pub(crate) client: &'a reqwest::Client,
    pub(crate) base: &'a str,
    pub(crate) token: Option<&'a str>,
}

impl IdentityClient<'_> {
    pub async fn public_key(&self) -> Result<PublicKeyInfo, EngineError> {
        let resp = check(
            self.client
                .get(url(self.base, &["identity"])?)
                .send()
                .await?,
        )
        .await?;
        Ok(resp.json().await?)
    }

    pub async fn verify(&self, data: &str, signature: &str) -> Result<VerifyResult, EngineError> {
        let rb = self
            .client
            .post(url(self.base, &["identity", "verify"])?)
            .json(&serde_json::json!({"data": data, "signature": signature}));
        let resp = check(bearer(rb, self.token).send().await?).await?;
        Ok(resp.json().await?)
    }
}
