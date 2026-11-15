use std::marker::PhantomData;

use serde::de::DeserializeOwned;
use serde::{Deserialize, Serialize};

use crate::error::{bearer, check, url};
use crate::EngineError;

/// A stored document with its envelope.
#[derive(Debug, Clone, Deserialize, Serialize, PartialEq)]
pub struct DocumentRecord<T> {
    #[serde(rename = "type")]
    pub doc_type: String,
    pub id: String,
    pub data: T,
    pub created_at: String,
    pub updated_at: String,
}

/// One entry of the linearized history (newest first).
#[derive(Debug, Clone, Deserialize, Serialize, PartialEq)]
pub struct Version {
    pub version: u32,
    pub data: serde_json::Value,
    pub timestamp: String,
    /// `create` for version 1, else `update`.
    pub operation: String,
}

/// A node of the version DAG; also a branch head from `branches`.
#[derive(Debug, Clone, Deserialize, Serialize, PartialEq)]
pub struct BranchHead {
    pub version_id: String,
    pub seq: u64,
    pub parent_ids: Vec<String>,
    pub timestamp: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub live: Option<bool>,
}

/// `GET /:type/:id/history?topology=1`: tips plus the whole DAG.
#[derive(Debug, Clone, Deserialize, Serialize, PartialEq)]
pub struct HistoryTopology {
    pub heads: Vec<String>,
    pub versions: Vec<BranchHead>,
}

/// List filters; unset fields are not sent.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct CollectionQuery {
    pub limit: Option<u32>,
    pub offset: Option<u32>,
    /// `id`, `created_at` or `updated_at`.
    pub sort: Option<String>,
    /// Substring match against the serialized data.
    pub search: Option<String>,
}

/// Typed document collection (`/:type/...`).
pub struct Collection<'a, T> {
    pub(crate) client: &'a reqwest::Client,
    pub(crate) base: &'a str,
    pub(crate) type_name: String,
    pub(crate) token: Option<&'a str>,
    pub(crate) _doc: PhantomData<fn() -> T>,
}

impl<T: DeserializeOwned + Serialize> Collection<'_, T> {
    fn route(&self, rest: &[&str]) -> Result<reqwest::Url, EngineError> {
        let mut segs = vec![self.type_name.as_str()];
        segs.extend_from_slice(rest);
        url(self.base, &segs)
    }

    /// Auth is decided by method: GET/HEAD are public.
    fn auth(&self, rb: reqwest::RequestBuilder) -> reqwest::RequestBuilder {
        bearer(rb, self.token)
    }

    pub async fn create(&self, doc: T) -> Result<DocumentRecord<T>, EngineError> {
        let rb = self.client.post(self.route(&[""])?).json(&doc);
        let resp = check(self.auth(rb).send().await?).await?;
        Ok(resp.json().await?)
    }

    pub async fn get(&self, id: &str) -> Result<DocumentRecord<T>, EngineError> {
        let resp = check(self.client.get(self.route(&[id])?).send().await?).await?;
        Ok(resp.json().await?)
    }

    pub async fn list(
        &self,
        query: Option<CollectionQuery>,
    ) -> Result<Vec<DocumentRecord<T>>, EngineError> {
        let mut params: Vec<(&str, String)> = Vec::new();
        if let Some(q) = query {
            if let Some(v) = q.limit {
                params.push(("limit", v.to_string()));
            }
            if let Some(v) = q.offset {
                params.push(("offset", v.to_string()));
            }
            if let Some(v) = q.sort.filter(|s| !s.is_empty()) {
                params.push(("sort", v));
            }
            if let Some(v) = q.search.filter(|s| !s.is_empty()) {
                params.push(("search", v));
            }
        }
        let mut rb = self.client.get(self.route(&[""])?);
        if !params.is_empty() {
            rb = rb.query(&params);
        }
        Ok(check(rb.send().await?).await?.json().await?)
    }

    /// `PUT`: `data` is the whole new document; fields it omits are
    /// dropped, not kept.
    pub async fn update(
        &self,
        id: &str,
        data: serde_json::Value,
    ) -> Result<DocumentRecord<T>, EngineError> {
        let rb = self.client.put(self.route(&[id])?).json(&data);
        Ok(check(self.auth(rb).send().await?).await?.json().await?)
    }

    pub async fn delete(&self, id: &str) -> Result<(), EngineError> {
        let rb = self.client.delete(self.route(&[id])?);
        check(self.auth(rb).send().await?).await?;
        Ok(())
    }

    /// Linearized versions, newest first.
    pub async fn history(&self, id: &str) -> Result<Vec<Version>, EngineError> {
        #[derive(Deserialize)]
        struct Payload {
            versions: Vec<Version>,
        }
        let resp = check(
            self.client
                .get(self.route(&[id, "history"])?)
                .send()
                .await?,
        )
        .await?;
        Ok(resp.json::<Payload>().await?.versions)
    }

    /// The version DAG instead of the linearized list.
    pub async fn history_with_topology(&self, id: &str) -> Result<HistoryTopology, EngineError> {
        let rb = self
            .client
            .get(self.route(&[id, "history"])?)
            .query(&[("topology", "1")]);
        Ok(check(rb.send().await?).await?.json().await?)
    }

    /// Appends a new version equal to `version`. 409 (no such version)
    /// maps to `EngineError::VersionConflict`.
    pub async fn revert(&self, id: &str, version: u32) -> Result<DocumentRecord<T>, EngineError> {
        let rb = self
            .client
            .post(self.route(&[id, "revert"])?)
            .json(&serde_json::json!({ "version": version }));
        let resp = self.auth(rb).send().await?;
        match check(resp).await {
            Err(EngineError::Status {
                status: 409,
                message,
            }) => Err(EngineError::VersionConflict(message)),
            r => Ok(r?.json().await?),
        }
    }

    /// DAG tips; `live` limits to non-abandoned heads.
    pub async fn branches(&self, id: &str, live: bool) -> Result<Vec<BranchHead>, EngineError> {
        #[derive(Deserialize)]
        struct Payload {
            heads: Vec<BranchHead>,
        }
        let mut rb = self.client.get(self.route(&[id, "branches"])?);
        if live {
            rb = rb.query(&[("live", "1")]);
        }
        Ok(check(rb.send().await?)
            .await?
            .json::<Payload>()
            .await?
            .heads)
    }
}
