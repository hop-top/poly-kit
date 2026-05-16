package domain

import "context"

// AuditEntry records a single auditable action on an entity.
type AuditEntry struct {
	EntityID  string
	Timestamp string
	By        string
	Action    string
	Note      string
}

// AuditRepository persists and retrieves audit trail entries.
type AuditRepository interface {
	// AddEntry records an audit entry.
	AddEntry(ctx context.Context, entry *AuditEntry) error

	// ListEntries returns all audit entries for the given entity ID,
	// ordered by timestamp ascending.
	ListEntries(ctx context.Context, entityID string) ([]*AuditEntry, error)
}
