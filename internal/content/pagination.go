package content

// Pagination defaults for post listings.
const (
	DefaultPerPage = 10
	MaxPerPage     = 100
)

// clamp converts a possibly out-of-range page and a requested per-page size
// into a SQL limit/offset plus the clamped 1-based page. page < 1 is treated
// as page 1. perPage <= 0 defaults to DefaultPerPage; perPage > MaxPerPage is
// capped at MaxPerPage.
func clamp(page, perPage int) (limit, offset, clampedPage int) {
	if page < 1 {
		page = 1
	}
	switch {
	case perPage <= 0:
		perPage = DefaultPerPage
	case perPage > MaxPerPage:
		perPage = MaxPerPage
	}
	return perPage, (page - 1) * perPage, page
}

// Page is the pagination contract shared by every paginated read path: public
// home/category, admin post list, and admin media list (Req 8.1). TotalPages
// is 0 when Total is 0 (an empty result set has no pages), matching
// AdminService.List's existing convention -- never clamped up to 1.
type Page struct {
	Page       int
	PerPage    int
	Total      int
	TotalPages int
}

// newPage builds a Page from a clamped page/limit and a total row count.
func newPage(page, perPage, total int) Page {
	return Page{Page: page, PerPage: perPage, Total: total, TotalPages: TotalPages(total, perPage)}
}

// TotalPages computes the page count for a total row count and page size,
// matching newPage's convention: 0 when total is 0 or perPage is
// non-positive, never rounded up to 1. Exported for callers that already
// hold Total/PerPage from elsewhere (e.g. an admin JSON handler after a
// repository Count call) and only need the page count, not a full Page.
func TotalPages(total, perPage int) int {
	if perPage <= 0 || total <= 0 {
		return 0
	}
	return (total + perPage - 1) / perPage
}
