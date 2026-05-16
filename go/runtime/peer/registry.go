package peer

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"hop.top/kit/go/storage/sqlstore"
)

// TrustLevel indicates a peer's trust status.
type TrustLevel int

const (
	Unknown TrustLevel = iota
	Trusted
	Blocked
	PendingTOFU
)

// PeerRecord extends PeerInfo with trust and timing metadata.
type PeerRecord struct {
	PeerInfo
	Trust     TrustLevel `json:"trust"`
	FirstSeen time.Time  `json:"first_seen"`
	LastSeen  time.Time  `json:"last_seen"`
}

// Registry persists peer records in a sqlstore.
type Registry struct {
	store *sqlstore.Store
}

// NewRegistry creates a Registry backed by the given store.
func NewRegistry(store *sqlstore.Store) *Registry {
	return &Registry{store: store}
}

func peerKey(id string) string { return "peer:" + id }

// Add inserts a new peer record with Unknown trust.
func (r *Registry) Add(info PeerInfo) error {
	if err := info.Validate(); err != nil {
		return err
	}
	now := time.Now().UTC()
	rec := PeerRecord{
		PeerInfo:  info,
		Trust:     Unknown,
		FirstSeen: now,
		LastSeen:  now,
	}
	return r.store.Put(context.Background(), peerKey(info.ID), rec)
}

// Get retrieves a peer record by ID. Returns nil if not found.
func (r *Registry) Get(id string) (*PeerRecord, error) {
	var rec PeerRecord
	found, err := r.store.Get(context.Background(), peerKey(id), &rec)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	return &rec, nil
}

// List returns all peer records.
func (r *Registry) List() ([]PeerRecord, error) {
	return r.query("")
}

// Trusted returns only peers with Trust == Trusted.
func (r *Registry) Trusted() ([]PeerRecord, error) {
	all, err := r.query("")
	if err != nil {
		return nil, err
	}
	var out []PeerRecord
	for _, rec := range all {
		if rec.Trust == Trusted {
			out = append(out, rec)
		}
	}
	return out, nil
}

// Pending returns peers with Trust == PendingTOFU.
func (r *Registry) Pending() ([]PeerRecord, error) {
	all, err := r.query("")
	if err != nil {
		return nil, err
	}
	var out []PeerRecord
	for _, rec := range all {
		if rec.Trust == PendingTOFU {
			out = append(out, rec)
		}
	}
	return out, nil
}

// SetTrust updates a peer's trust level.
func (r *Registry) SetTrust(id string, level TrustLevel) error {
	rec, err := r.Get(id)
	if err != nil {
		return err
	}
	if rec == nil {
		return fmt.Errorf("peer: %s not found", id)
	}
	rec.Trust = level
	return r.store.Put(context.Background(), peerKey(id), rec)
}

// UpdateLastSeen refreshes the peer's last-seen timestamp.
func (r *Registry) UpdateLastSeen(id string) error {
	rec, err := r.Get(id)
	if err != nil {
		return err
	}
	if rec == nil {
		return fmt.Errorf("peer: %s not found", id)
	}
	rec.LastSeen = time.Now().UTC()
	return r.store.Put(context.Background(), peerKey(id), rec)
}

// Remove deletes a peer record.
func (r *Registry) Remove(id string) error {
	db := r.store.DB()
	_, err := db.ExecContext(context.Background(),
		"DELETE FROM kv WHERE key = ?", peerKey(id))
	return err
}

// query scans all peer: keys from the kv table.
func (r *Registry) query(_ string) ([]PeerRecord, error) {
	db := r.store.DB()
	rows, err := db.QueryContext(context.Background(),
		"SELECT payload FROM kv WHERE key LIKE 'peer:%'")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []PeerRecord
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var rec PeerRecord
		if err := json.Unmarshal([]byte(payload), &rec); err != nil {
			return nil, err
		}
		records = append(records, rec)
	}
	return records, rows.Err()
}
