//! Conflict resolution. Go's merge.go.

use serde::de::DeserializeOwned;
use serde::Serialize;

use super::diff::Diff;

/// Why a merge failed: a snapshot did not decode or encode, or the
/// merge function refused.
pub type MergeError = Box<dyn std::error::Error + Send + Sync + 'static>;

/// Resolves a conflict between the local and remote state of an entity,
/// returning the merged result. Go's `MergeFunc[T]`.
pub type MergeFunc<T> = std::sync::Arc<dyn Fn(T, T) -> Result<T, MergeError> + Send + Sync>;

/// Returns the diff with the later timestamp. On equal timestamps the
/// greater `node_id` wins, the local one on a tie: the comparison is
/// `local.node_id >= remote.node_id`, which both sides of a sync must
/// share to pick the same winner. Go's `LastWriteWins`.
pub fn last_write_wins(local: Diff, remote: Diff) -> Diff {
    if remote.timestamp.before(&local.timestamp) {
        return local;
    }
    if local.timestamp.before(&remote.timestamp) {
        return remote;
    }
    if local.node_id >= remote.node_id {
        local
    } else {
        remote
    }
}

/// Resolves conflicting diffs with a custom merge: decodes both `after`
/// snapshots (an absent one is `T::default()`), calls `merge`, encodes
/// the result and returns the [`last_write_wins`] diff carrying it.
/// Go's `ResolveDiff`.
pub fn resolve_diff<T>(
    local: &Diff,
    remote: &Diff,
    merge: impl FnOnce(T, T) -> Result<T, MergeError>,
) -> Result<Diff, MergeError>
where
    T: Serialize + DeserializeOwned + Default,
{
    let decode = |d: &Diff| -> Result<T, MergeError> {
        match &d.after {
            Some(b) => Ok(serde_json::from_slice(b)?),
            None => Ok(T::default()),
        }
    };
    let merged = merge(decode(local)?, decode(remote)?)?;
    let mut winner = last_write_wins(local.clone(), remote.clone());
    winner.after = Some(serde_json::to_vec(&merged)?);
    Ok(winner)
}
