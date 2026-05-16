package sync

import (
	"time"

	"hop.top/kit/go/runtime/domain"
)

// RemoteStatus reports the health of a single remote.
type RemoteStatus struct {
	Name         string
	Connected    bool
	LastSync     time.Time
	PendingDiffs int
	LastError    error
	Lag          time.Duration
}

// Status returns the current health state of all registered remotes.
func (r *Replicator[T]) Status() []RemoteStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()

	remotes := r.remotes.List()
	out := make([]RemoteStatus, 0, len(remotes))

	queueLen := len(r.queue)
	for _, rem := range remotes {
		st, ok := r.statuses[rem.Name]
		if !ok {
			continue
		}

		cursor := r.cursors[rem.Name]
		pending := queueLen - cursor
		if pending < 0 {
			pending = 0
		}

		var lag time.Duration
		if !st.lastSync.IsZero() {
			lag = time.Since(st.lastSync)
		}

		out = append(out, RemoteStatus{
			Name:         rem.Name,
			Connected:    st.connected,
			LastSync:     st.lastSync,
			PendingDiffs: pending,
			LastError:    st.lastErr,
			Lag:          lag,
		})
	}
	return out
}

// compile-time interface check
var _ domain.Entity = (*statusEntity)(nil)

type statusEntity struct{ id string }

func (s statusEntity) GetID() string { return s.id }
