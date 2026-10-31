//! The model catalog the picker queries: Go's `aim` types and filter
//! semantics, with an in-memory source.

use crate::llm::error::LlmError;
use crate::llm::provider::BoxFuture;

/// Input and output modalities a model supports.
#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct Modalities {
    pub input: Vec<String>,
    pub output: Vec<String>,
}

/// Token windows; 0 means unknown.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub struct Limits {
    pub context: u64,
    pub input: u64,
    pub output: u64,
}

/// Price in USD per million tokens.
#[derive(Debug, Clone, Copy, PartialEq, Default)]
pub struct Cost {
    pub input: f64,
    pub output: f64,
    pub cache_read: f64,
    pub cache_write: f64,
}

/// A catalog model. `provider` is the catalog provider's ID, the scheme
/// a pool entry names.
#[derive(Debug, Clone, PartialEq, Default)]
pub struct Model {
    pub id: String,
    pub name: String,
    pub family: String,
    pub provider: String,
    pub modalities: Modalities,
    pub tool_call: bool,
    pub reasoning: bool,
    pub open_weights: bool,
    pub structured_output: bool,
    pub temperature: bool,
    /// `None` for models that publish no price (local, open weights).
    pub cost: Option<Cost>,
    pub limit: Limits,
}

/// Constrains a catalog query. Non-empty fields are ANDed. Modality
/// lists match by subset; the `Option<bool>` fields are tristates, `None`
/// meaning no filter.
#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct Filter {
    pub input: Vec<String>,
    pub output: Vec<String>,
    /// Exact, case-sensitive provider ID.
    pub provider: String,
    /// Exact, case-sensitive family.
    pub family: String,
    pub tool_call: Option<bool>,
    pub reasoning: Option<bool>,
    pub open_weights: Option<bool>,
    pub structured_output: Option<bool>,
    pub temperature: Option<bool>,
    /// Case-insensitive substring of the model ID or name.
    pub query: String,
}

impl Filter {
    /// Whether `m` passes.
    pub fn matches(&self, m: &Model) -> bool {
        let subset = |need: &[String], have: &[String]| need.iter().all(|n| have.contains(n));
        let tri = |want: Option<bool>, got: bool| want.is_none_or(|w| w == got);
        (self.provider.is_empty() || m.provider == self.provider)
            && (self.family.is_empty() || m.family == self.family)
            && subset(&self.input, &m.modalities.input)
            && subset(&self.output, &m.modalities.output)
            && tri(self.tool_call, m.tool_call)
            && tri(self.reasoning, m.reasoning)
            && tri(self.open_weights, m.open_weights)
            && tri(self.structured_output, m.structured_output)
            && tri(self.temperature, m.temperature)
            && (self.query.is_empty() || {
                let q = self.query.to_lowercase();
                m.id.to_lowercase().contains(&q) || m.name.to_lowercase().contains(&q)
            })
    }
}

/// Where the picker gets models. Implement it over a remote catalog;
/// the result need not be filtered or sorted by the source (the picker
/// ranks), but must honor `filter`.
pub trait ModelCatalog: Send + Sync {
    fn models<'a>(&'a self, filter: &'a Filter) -> BoxFuture<'a, Result<Vec<Model>, LlmError>>;
}

/// An in-memory catalog, filtered the way a registry does and ordered
/// by (provider, id).
#[derive(Debug, Clone, Default)]
pub struct StaticCatalog {
    models: Vec<Model>,
}

impl StaticCatalog {
    pub fn new(models: Vec<Model>) -> Self {
        StaticCatalog { models }
    }
}

impl ModelCatalog for StaticCatalog {
    fn models<'a>(&'a self, filter: &'a Filter) -> BoxFuture<'a, Result<Vec<Model>, LlmError>> {
        Box::pin(async move {
            let mut out: Vec<Model> = self
                .models
                .iter()
                .filter(|m| filter.matches(m))
                .cloned()
                .collect();
            out.sort_by(|a, b| (&a.provider, &a.id).cmp(&(&b.provider, &b.id)));
            Ok(out)
        })
    }
}
