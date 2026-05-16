package sync

import "encoding/json"

// MergeFunc resolves a conflict between local and remote entity states,
// returning the merged result.
type MergeFunc[T any] func(local, remote T) (T, error)

// LastWriteWins returns the Diff with the later timestamp.
// On equal timestamps, the higher NodeID wins (deterministic tiebreak).
func LastWriteWins(local, remote Diff) Diff {
	if remote.Timestamp.Before(local.Timestamp) {
		return local
	}
	if local.Timestamp.Before(remote.Timestamp) {
		return remote
	}
	// Equal physical+logical: tiebreak on NodeID
	if local.NodeID >= remote.NodeID {
		return local
	}
	return remote
}

// ResolveDiff applies a custom MergeFunc to resolve conflicting diffs.
// It deserializes both After payloads, calls fn, and re-serializes the result.
func ResolveDiff[T any](local, remote Diff, fn MergeFunc[T]) (Diff, error) {
	var localVal, remoteVal T

	if local.After != nil {
		if err := json.Unmarshal(local.After, &localVal); err != nil {
			return Diff{}, err
		}
	}
	if remote.After != nil {
		if err := json.Unmarshal(remote.After, &remoteVal); err != nil {
			return Diff{}, err
		}
	}

	merged, err := fn(localVal, remoteVal)
	if err != nil {
		return Diff{}, err
	}

	after, err := json.Marshal(merged)
	if err != nil {
		return Diff{}, err
	}

	// Use the later timestamp for the result
	winner := LastWriteWins(local, remote)
	winner.After = after
	return winner, nil
}
