package domain

// Entity is the base constraint for all domain objects.
// Every entity must be identifiable by a string ID.
type Entity interface {
	GetID() string
}
