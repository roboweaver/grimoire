package web

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// --- WordPress REST error shape (Req 13.1) ---
//
// This is deliberately distinct from writeJSONError's {error:{code,message}}
// envelope used by /admin/api/*: wp-json clients expect WordPress's own
// {code,message,data:{status}} shape.

type restError struct {
	Code    string        `json:"code"`
	Message string        `json:"message"`
	Data    restErrorData `json:"data"`
}

type restErrorData struct {
	Status int `json:"status"`
}

// writeRESTError writes the standard WordPress REST error body and sets the
// matching HTTP status. code should be one of WordPress's own error codes
// where a direct analog exists (Req 13.2), e.g. "rest_no_route",
// "rest_post_invalid_id", "rest_forbidden", "rest_invalid_credentials",
// "rest_not_implemented".
func writeRESTError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(restError{Code: code, Message: message, Data: restErrorData{Status: status}})
}

// writeRESTJSON writes v as a wp-json response body with the given status,
// always setting the WordPress-expected Content-Type (Req 1.4).
func writeRESTJSON(w http.ResponseWriter, status int, v any) error {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(v)
}

// setRESTPaginationHeaders sets X-WP-Total/X-WP-TotalPages on a collection
// response (Req 6.1). It must be called before the status/body are written.
func setRESTPaginationHeaders(w http.ResponseWriter, total, perPage int) {
	totalPages := 0
	if perPage > 0 {
		totalPages = (total + perPage - 1) / perPage
	}
	w.Header().Set("X-WP-Total", strconv.Itoa(total))
	w.Header().Set("X-WP-TotalPages", strconv.Itoa(totalPages))
}

// --- Absolute URL construction (Req 6.6) ---

// requestBaseURL returns the scheme+host prefix ("https://example.com") used
// to build every absolute link/href/source_url in a wp-json response, derived
// from the incoming request's TLS state and Host header at request time.
// grimoire has no stored site-wide base-URL configuration (M1-M4 precedent),
// so this is always computed fresh; an operator behind a reverse proxy that
// does not correctly forward the Host header is responsible for fixing that
// at the edge (Req 6.6), matching every other Host-header-dependent behavior
// in the system.
func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// restAbs resolves a relative content-layer path (e.g. "/hello-world" from
// content.RESTMapper) into an absolute URL against the request's base.
func restAbs(r *http.Request, path string) string {
	base := requestBaseURL(r)
	if path == "" {
		return base + "/"
	}
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		// Already absolute (e.g. guid.rendered, which is a stored value, not
		// a link/href/source_url subject to Req 6.6's construction rule).
		return path
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return base + path
}

// --- _links / _embedded (Req 6.2-6.5) ---

// restLink is one WordPress-shaped link entry under a _links relation.
type restLink struct {
	Href       string `json:"href"`
	Embeddable bool   `json:"embeddable,omitempty"`
}

// restLinks is the top-level "_links" object: relation name -> entries.
// WordPress always uses an array of entries per relation, even when there is
// only ever one (Req 6.4).
type restLinks map[string][]restLink

// restLinkBuilder accumulates named relations for one item's _links.
type restLinkBuilder struct {
	links restLinks
}

func newRESTLinks() *restLinkBuilder {
	return &restLinkBuilder{links: restLinks{}}
}

func (b *restLinkBuilder) add(rel, href string) *restLinkBuilder {
	b.links[rel] = append(b.links[rel], restLink{Href: href})
	return b
}

func (b *restLinkBuilder) addEmbeddable(rel, href string) *restLinkBuilder {
	b.links[rel] = append(b.links[rel], restLink{Href: href, Embeddable: true})
	return b
}

// embedRequested reports whether the request asked for _embedded resources
// via the "_embed" query parameter (bare "_embed" or "_embed=author,replies"
// both count, Req 6.3; this implementation does not filter by the named
// relations, matching WordPress's own behavior when no filter list narrows
// it further than "embed everything embeddable").
func embedRequested(r *http.Request) bool {
	_, ok := r.URL.Query()["_embed"]
	return ok
}

// withEnvelope re-marshals v (a REST view-model struct) merged with the
// item's _links object and, when non-nil, its _embedded object, producing
// the flat top-level shape real WordPress emits: {...fields..., "_links":
// {...}, "_embedded": {...}}. v must marshal to a JSON object.
func withEnvelope(v any, links restLinks, embedded map[string]any) (map[string]any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	out["_links"] = links
	if embedded != nil {
		out["_embedded"] = embedded
	}
	return out, nil
}

// --- Pagination query parsing (Req 2.3) ---

// restPaging is a parsed, clamped page/per_page pair plus the derived
// limit/offset for a repository query.
type restPaging struct {
	Page    int
	PerPage int
	Limit   int
	Offset  int
}

// parseRESTPaging reads "page" (default 1) and "per_page" (default 10,
// per WordPress's own default, capped at max) from the query string,
// clamping both to sane, always-positive values.
func parseRESTPaging(r *http.Request, max int) restPaging {
	q := r.URL.Query()
	page := atoiDefault(q.Get("page"), 1)
	if page < 1 {
		page = 1
	}
	perPage := atoiDefault(q.Get("per_page"), 10)
	if perPage < 1 {
		perPage = 10
	}
	if max > 0 && perPage > max {
		perPage = max
	}
	return restPaging{Page: page, PerPage: perPage, Limit: perPage, Offset: (page - 1) * perPage}
}

// restOrder reads orderby/order query params, defaulting to WordPress's own
// defaults ("date" descending) and rejecting any value other than the
// whitelisted ones (Req 2.3), falling back to the default rather than
// erroring on garbage input.
func restOrder(r *http.Request) (orderBy, order string) {
	q := r.URL.Query()
	orderBy = q.Get("orderby")
	if orderBy != "date" && orderBy != "id" {
		orderBy = "date"
	}
	order = strings.ToLower(q.Get("order"))
	if order != "asc" && order != "desc" {
		order = "desc"
	}
	return orderBy, order
}
