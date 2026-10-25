package web

import (
	"net/http"
	"strconv"
)

// restForceDelete reports whether the request opts into a permanent (non-trash)
// delete via ?force=true. WordPress treats force as a boolean query parameter;
// grimoire accepts the canonical "true" and "1" forms and reads it from the
// query string only (DELETE carries no body on these routes). Any other value,
// or its absence, is false — which the caller maps to 400 rest_trash_not_supported
// for the no-trash resource types (attachments, users).
func restForceDelete(r *http.Request) bool {
	switch r.URL.Query().Get("force") {
	case "true", "1":
		return true
	default:
		return false
	}
}

// parseRESTReassign parses the DELETE /users/{id} reassign query parameter. It
// returns (reassignTo, valid):
//   - absent            -> (0, false)  — reassign is required; a missing value is invalid
//   - "false"           -> (0, true)   — delete the user's authored content
//   - a positive int n  -> (n, true)   — reassign authored content to user n
//   - anything else      -> (0, false) — "true", 0, negative, or non-integer is invalid
//
// The bool reports validity, not presence: the handler maps !valid to
// 400 rest_user_invalid_reassign, and separately verifies that a positive id
// resolves to an existing user.
func parseRESTReassign(r *http.Request) (int64, bool) {
	q := r.URL.Query()
	if !q.Has("reassign") {
		return 0, false
	}
	raw := q.Get("reassign")
	if raw == "false" {
		return 0, true
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// restDeleteEnvelope builds WordPress's delete response body,
// {"deleted": true, "previous": <object>}. previous is the full resource
// representation a GET of the same id returned immediately before deletion. A
// map[string]any keeps the nested envelope (itself a mapped resource) serializing
// unchanged through writeRESTResponse.
func restDeleteEnvelope(previous map[string]any) map[string]any {
	return map[string]any{"deleted": true, "previous": previous}
}
