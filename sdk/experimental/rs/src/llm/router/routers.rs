//! The scoring routers: random, matrix factorization, BERT and
//! similarity-weighted Elo ranking.

use std::collections::{BTreeMap, BTreeSet};
use std::sync::{Arc, Mutex};

use crate::llm::error::LlmError;
use crate::llm::provider::BoxFuture;
use crate::llm::triton::Scorer;
use crate::util::retry::{Rng, SplitMix64};

use super::{Embedder, Router};

pub(crate) fn dot(a: &[f64], b: &[f64]) -> f64 {
    a.iter().zip(b).map(|(x, y)| x * y).sum()
}

pub(crate) fn vec_norm(v: &[f64]) -> f64 {
    v.iter().map(|x| x * x).sum::<f64>().sqrt()
}

fn err(msg: impl std::fmt::Display) -> LlmError {
    LlmError::Invalid(msg.to_string())
}

/// A uniform random score in `[0, 1]`: a baseline for comparisons and
/// tests.
pub struct RandomRouter {
    rng: Mutex<SplitMix64>,
}

impl RandomRouter {
    /// Seeded from entropy.
    pub fn new() -> Self {
        RandomRouter {
            rng: Mutex::new(SplitMix64::from_entropy()),
        }
    }

    /// Deterministic for a given seed.
    pub fn with_seed(seed: u64) -> Self {
        RandomRouter {
            rng: Mutex::new(SplitMix64::new(seed)),
        }
    }
}

impl Default for RandomRouter {
    fn default() -> Self {
        Self::new()
    }
}

impl Router for RandomRouter {
    fn score<'a>(&'a self, _prompt: &'a str) -> BoxFuture<'a, Result<f64, LlmError>> {
        Box::pin(async move {
            let bits = self.rng.lock().expect("rng lock").next_u64();
            // 53 random bits make a float in [0, 1).
            Ok((bits >> 11) as f64 / (1u64 << 53) as f64)
        })
    }
}

/// Configures an [`MfRouter`].
pub struct MfConfig {
    pub embedder: Option<Arc<dyn Embedder>>,
    pub scorer: Option<Arc<dyn Scorer>>,
    /// Empty means `gpt-4-1106-preview`.
    pub strong_model: String,
    /// Empty means `mixtral-8x7b-instruct-v0.1`.
    pub weak_model: String,
}

/// Matrix-factorization routing: embeds the prompt, and a Triton scorer
/// running the MF model returns the strong model's win probability.
pub struct MfRouter {
    embedder: Arc<dyn Embedder>,
    scorer: Arc<dyn Scorer>,
    strong_model: String,
    weak_model: String,
}

impl MfRouter {
    pub fn new(cfg: MfConfig) -> Result<Self, LlmError> {
        let embedder = cfg
            .embedder
            .ok_or_else(|| err("mf: embedder is required"))?;
        let scorer = cfg.scorer.ok_or_else(|| err("mf: scorer is required"))?;
        let or = |v: String, d: &str| if v.is_empty() { d.to_string() } else { v };
        Ok(MfRouter {
            embedder,
            scorer,
            strong_model: or(cfg.strong_model, "gpt-4-1106-preview"),
            weak_model: or(cfg.weak_model, "mixtral-8x7b-instruct-v0.1"),
        })
    }

    pub fn strong_model(&self) -> &str {
        &self.strong_model
    }

    pub fn weak_model(&self) -> &str {
        &self.weak_model
    }
}

impl Router for MfRouter {
    fn score<'a>(&'a self, prompt: &'a str) -> BoxFuture<'a, Result<f64, LlmError>> {
        Box::pin(async move {
            let emb = self
                .embedder
                .embed(prompt)
                .await
                .map_err(|e| err(format!("mf: embed prompt: {e}")))?;
            let input: Vec<f32> = emb.iter().map(|&v| v as f32).collect();
            self.scorer
                .score(&input)
                .await
                .map_err(|e| err(format!("mf: scorer: {e}")))
        })
    }
}

/// Converts text into token ids for BERT-style models, handling padding
/// and truncation.
pub trait Tokenizer: Send + Sync {
    fn tokenize(&self, text: &str) -> Result<Vec<i32>, LlmError>;
}

/// Configures a [`BertRouter`].
pub struct BertConfig {
    pub tokenizer: Option<Arc<dyn Tokenizer>>,
    pub scorer: Option<Arc<dyn Scorer>>,
    /// 0 or less means 3.
    pub num_labels: i32,
}

/// BERT classification routing: tokenizes locally and a Triton scorer
/// returns the strong model's win rate. When the server returns raw
/// logits instead, [`score_from_logits`] turns them into the rate.
pub struct BertRouter {
    tokenizer: Arc<dyn Tokenizer>,
    scorer: Arc<dyn Scorer>,
    num_labels: i32,
}

impl BertRouter {
    pub fn new(cfg: BertConfig) -> Result<Self, LlmError> {
        let tokenizer = cfg
            .tokenizer
            .ok_or_else(|| err("bert: tokenizer is required"))?;
        let scorer = cfg.scorer.ok_or_else(|| err("bert: scorer is required"))?;
        Ok(BertRouter {
            tokenizer,
            scorer,
            num_labels: if cfg.num_labels <= 0 {
                3
            } else {
                cfg.num_labels
            },
        })
    }

    pub fn num_labels(&self) -> i32 {
        self.num_labels
    }
}

impl Router for BertRouter {
    fn score<'a>(&'a self, prompt: &'a str) -> BoxFuture<'a, Result<f64, LlmError>> {
        Box::pin(async move {
            let ids = self
                .tokenizer
                .tokenize(prompt)
                .map_err(|e| err(format!("bert: tokenize: {e}")))?;
            let input: Vec<f32> = ids.iter().map(|&i| i as f32).collect();
            self.scorer
                .score(&input)
                .await
                .map_err(|e| err(format!("bert: scorer: {e}")))
        })
    }
}

/// The strong model's win rate from raw `[strong_wins, tie, weak_wins]`
/// logits: one minus the softmax mass of the last two classes. 0.5 for
/// no logits.
pub fn score_from_logits(logits: &[f64]) -> f64 {
    if logits.is_empty() {
        return 0.5;
    }
    let sm = softmax(logits);
    let start = sm.len().saturating_sub(2);
    1.0 - sm[start..].iter().sum::<f64>()
}

/// Softmax, numerically stable (the maximum is subtracted).
pub fn softmax(logits: &[f64]) -> Vec<f64> {
    let Some(max) = logits.iter().copied().reduce(f64::max) else {
        return Vec::new();
    };
    let exp: Vec<f64> = logits.iter().map(|v| (v - max).exp()).collect();
    let sum: f64 = exp.iter().sum();
    exp.into_iter().map(|e| e / sum).collect()
}

/// One arena battle outcome.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct BattleRecord {
    pub model_a: String,
    pub model_b: String,
    /// `model_a`, `model_b`, `tie` or `tie (bothbad)`.
    pub winner: String,
}

/// Configures a [`SwRankingRouter`].
pub struct SwConfig {
    pub embedder: Option<Arc<dyn Embedder>>,
    pub strong_model: String,
    pub weak_model: String,
    /// 0 or less means 10.
    pub num_tiers: i32,
    pub battles: Vec<BattleRecord>,
    /// One embedding per battle.
    pub battle_embeddings: Vec<Vec<f64>>,
}

/// Similarity-weighted Elo ranking: embeds the prompt, weights arena
/// battles by their similarity to it, re-estimates tier Elo ratings and
/// derives the strong tier's win rate against the weak one.
pub struct SwRankingRouter {
    embedder: Arc<dyn Embedder>,
    strong_model: String,
    weak_model: String,
    battle_embeddings: Vec<Vec<f64>>,
    /// Battles with models replaced by their tier.
    battles: Vec<BattleRecord>,
    model2tier: BTreeMap<String, usize>,
}

impl SwRankingRouter {
    pub fn new(cfg: SwConfig) -> Result<Self, LlmError> {
        let embedder = cfg
            .embedder
            .ok_or_else(|| err("sw: embedder is required"))?;
        if cfg.strong_model.is_empty() || cfg.weak_model.is_empty() {
            return Err(err("sw: strong and weak model names required"));
        }
        if cfg.battles.is_empty() {
            return Err(err("sw: battles data is required"));
        }
        if cfg.battles.len() != cfg.battle_embeddings.len() {
            return Err(err(format!(
                "sw: battles ({}) and embeddings ({}) length mismatch",
                cfg.battles.len(),
                cfg.battle_embeddings.len()
            )));
        }
        let num_tiers = if cfg.num_tiers <= 0 {
            10
        } else {
            cfg.num_tiers as usize
        };
        let ratings = compute_elo_mle(&cfg.battles, None);
        let model2tier = compute_tiers(&ratings, num_tiers);
        let tier = |m: &str| model2tier.get(m).copied().unwrap_or(0).to_string();
        let battles = cfg
            .battles
            .iter()
            .map(|b| BattleRecord {
                model_a: tier(&b.model_a),
                model_b: tier(&b.model_b),
                winner: b.winner.clone(),
            })
            .collect();
        Ok(SwRankingRouter {
            embedder,
            strong_model: cfg.strong_model,
            weak_model: cfg.weak_model,
            battle_embeddings: cfg.battle_embeddings,
            battles,
            model2tier,
        })
    }
}

impl Router for SwRankingRouter {
    fn score<'a>(&'a self, prompt: &'a str) -> BoxFuture<'a, Result<f64, LlmError>> {
        Box::pin(async move {
            let p = self
                .embedder
                .embed(prompt)
                .await
                .map_err(|e| err(format!("sw: embed prompt: {e}")))?;
            let pn = match vec_norm(&p) {
                0.0 => 1.0,
                n => n,
            };
            let sims: Vec<f64> = self
                .battle_embeddings
                .iter()
                .map(|e| match vec_norm(e) {
                    0.0 => 0.0,
                    en => dot(e, &p) / (en * pn),
                })
                .collect();
            let weights = compute_weightings(&sims);
            let weighted = compute_elo_mle(&self.battles, Some(&weights));
            let key = |m: &str| self.model2tier.get(m).copied().unwrap_or(0).to_string();
            let strong = weighted
                .get(&key(&self.strong_model))
                .copied()
                .unwrap_or(0.0);
            let weak = weighted.get(&key(&self.weak_model)).copied().unwrap_or(0.0);
            let weak_win = 1.0 / (1.0 + 10f64.powf((strong - weak) / 400.0));
            Ok(1.0 - weak_win)
        })
    }
}

/// Exponential weights from similarities: `10 * 10^(s / max)`, or a
/// uniform 10 when no similarity is positive.
pub(crate) fn compute_weightings(sims: &[f64]) -> Vec<f64> {
    let max = sims.iter().copied().fold(f64::NEG_INFINITY, f64::max);
    if max <= 0.0 {
        return vec![10.0; sims.len()];
    }
    sims.iter().map(|s| 10.0 * 10f64.powf(s / max)).collect()
}

/// Elo ratings by gradient-descent maximum likelihood (a simplified port
/// of the Python logistic-regression fit). Models are visited in name
/// order, so a fit is reproducible.
pub(crate) fn compute_elo_mle(
    battles: &[BattleRecord],
    weights: Option<&[f64]>,
) -> BTreeMap<String, f64> {
    const SCALE: f64 = 400.0;
    const INIT: f64 = 1000.0;
    const LR: f64 = 0.01;
    const ITERATIONS: usize = 200;
    let models: Vec<&String> = battles
        .iter()
        .flat_map(|b| [&b.model_a, &b.model_b])
        .collect::<BTreeSet<_>>()
        .into_iter()
        .collect();
    let idx: BTreeMap<&String, usize> = models.iter().enumerate().map(|(i, m)| (*m, i)).collect();
    let mut coefs = vec![0.0; models.len()];
    let log_base = 10f64.ln();
    for _ in 0..ITERATIONS {
        let mut grad = vec![0.0; models.len()];
        for (i, b) in battles.iter().enumerate() {
            let (a, c) = (idx[&b.model_a], idx[&b.model_b]);
            let x = log_base * (coefs[a] - coefs[c]);
            let pred = 1.0 / (1.0 + (-x).exp());
            let y = match b.winner.as_str() {
                "model_a" => 1.0,
                "tie" | "tie (bothbad)" => 0.5,
                _ => 0.0,
            };
            let w = weights.and_then(|w| w.get(i)).copied().unwrap_or(1.0);
            let e = (y - pred) * w * log_base;
            grad[a] += e;
            grad[c] -= e;
        }
        for (c, g) in coefs.iter_mut().zip(&grad) {
            *c += LR * g;
        }
    }
    models
        .into_iter()
        .zip(coefs)
        .map(|(m, c)| (m.clone(), SCALE * c + INIT))
        .collect()
}

/// Partitions models into `num_tiers` equal-size groups by descending
/// rating (ties by name): the highest rated are tier 0. With at least as
/// many tiers as models, each model is its own tier.
pub(crate) fn compute_tiers(
    ratings: &BTreeMap<String, f64>,
    num_tiers: usize,
) -> BTreeMap<String, usize> {
    if ratings.is_empty() || num_tiers == 0 {
        return BTreeMap::new();
    }
    let mut sorted: Vec<(&String, f64)> = ratings.iter().map(|(m, r)| (m, *r)).collect();
    sorted.sort_by(|a, b| {
        b.1.partial_cmp(&a.1)
            .unwrap_or(std::cmp::Ordering::Equal)
            .then_with(|| a.0.cmp(b.0))
    });
    let n = sorted.len();
    if num_tiers >= n {
        return sorted
            .into_iter()
            .enumerate()
            .map(|(i, (m, _))| (m.clone(), i))
            .collect();
    }
    let (per, extra) = (n / num_tiers, n % num_tiers);
    let mut out = BTreeMap::new();
    let mut it = sorted.into_iter();
    for tier in 0..num_tiers {
        let count = per + usize::from(tier < extra);
        for (m, _) in it.by_ref().take(count) {
            out.insert(m.clone(), tier);
        }
    }
    out
}
