package domain

// Query holds common list/search parameters for repository queries.
type Query struct {
	// Limit caps the number of results. Zero means no limit.
	Limit int
	// Offset skips the first N results (for pagination).
	Offset int
	// Sort specifies the column or field to order by.
	Sort string
	// Search is a free-text filter applied via LIKE or similar.
	Search string
}
