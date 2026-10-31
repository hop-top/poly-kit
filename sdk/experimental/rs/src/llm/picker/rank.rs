//! Token-window elimination and the tier rankers.

use std::cmp::Ordering;

use super::{
    BudgetTier, Elimination, Model, RequestProfile, ELIM_CONTEXT_WINDOW, ELIM_OUTPUT_TOKENS,
};

/// Drops candidates whose known limits violate the profile's bounds. An
/// unknown limit (0) passes: only a positively known mismatch eliminates.
pub(crate) fn apply_token_window(
    candidates: Vec<Model>,
    p: &RequestProfile,
) -> (Vec<Model>, Vec<Elimination>) {
    let mut survivors = Vec::new();
    let mut eliminated = Vec::new();
    for m in candidates {
        if p.max_input_tokens > 0 && m.limit.context > 0 && m.limit.context < p.max_input_tokens {
            let detail = format!(
                "context_window {} < required {}",
                m.limit.context, p.max_input_tokens
            );
            eliminated.push(Elimination {
                model: m,
                stage: ELIM_CONTEXT_WINDOW,
                detail,
            });
            continue;
        }
        if p.max_output_tokens > 0 && m.limit.output > 0 && m.limit.output < p.max_output_tokens {
            let detail = format!(
                "output_tokens {} < required {}",
                m.limit.output, p.max_output_tokens
            );
            eliminated.push(Elimination {
                model: m,
                stage: ELIM_OUTPUT_TOKENS,
                detail,
            });
            continue;
        }
        survivors.push(m);
    }
    (survivors, eliminated)
}

/// The token-weighted USD price of the Cheap and Balanced rankers. A
/// missing price is 0, so local models are not penalized.
fn weighted_price(m: &Model) -> f64 {
    m.cost.map_or(0.0, |c| c.input * 0.75 + c.output * 0.25)
}

/// Premium's tiebreak: priced models beat unpriced ones.
fn input_cost(m: &Model) -> f64 {
    m.cost.map_or(0.0, |c| c.input)
}

fn by_provider_id(a: &Model, b: &Model) -> Ordering {
    (&a.provider, &a.id).cmp(&(&b.provider, &b.id))
}

fn f(a: f64, b: f64) -> Ordering {
    a.partial_cmp(&b).unwrap_or(Ordering::Equal)
}

/// Orders `survivors` for `budget` (stable, ending in the (provider, id)
/// tiebreak) and returns the winner. `survivors` is not empty.
pub(crate) fn rank(mut survivors: Vec<Model>, budget: BudgetTier) -> Model {
    match budget {
        BudgetTier::Premium => {
            survivors.sort_by(|a, b| {
                b.limit
                    .context
                    .cmp(&a.limit.context)
                    .then_with(|| f(input_cost(b), input_cost(a)))
                    .then_with(|| by_provider_id(a, b))
            });
            survivors.swap_remove(0)
        }
        BudgetTier::Balanced => {
            survivors.sort_by(|a, b| {
                f(weighted_price(a), weighted_price(b)).then_with(|| by_provider_id(a, b))
            });
            // The median index: 0 for one survivor, b for [a, b, c].
            let mid = survivors.len() / 2;
            survivors.swap_remove(mid)
        }
        BudgetTier::Cheap => {
            survivors.sort_by(|a, b| {
                f(weighted_price(a), weighted_price(b)).then_with(|| by_provider_id(a, b))
            });
            survivors.swap_remove(0)
        }
    }
}
