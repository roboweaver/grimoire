package content

// Pagination defaults for post listings.
const (
	DefaultPerPage = 10
	MaxPerPage     = 100
)

// clamp converts a 1-based page and a requested per-page size into a SQL
// limit/offset. page < 1 is treated as page 1. perPage <= 0 defaults to
// DefaultPerPage; perPage > MaxPerPage is capped at MaxPerPage.
func clamp(page, perPage int) (limit, offset int) {
	if page < 1 {
		page = 1
	}
	switch {
	case perPage <= 0:
		perPage = DefaultPerPage
	case perPage > MaxPerPage:
		perPage = MaxPerPage
	}
	return perPage, (page - 1) * perPage
}
