package content

import (
	"regexp"
	"strings"
)

// slugRE is the allowed slug shape for registered-type posts.
var slugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// Slugify derives a slug from text: lower-cased, every run of characters
// outside [a-z0-9] collapsed to one hyphen, leading and trailing hyphens
// trimmed. It is the same algorithm as the term-name slugifier in
// internal/web.
func Slugify(name string) string {
	var b strings.Builder
	prevHyphen := true // trims a leading hyphen
	for _, ch := range strings.ToLower(name) {
		switch {
		case ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9':
			b.WriteRune(ch)
			prevHyphen = false
		default:
			if !prevHyphen {
				b.WriteByte('-')
				prevHyphen = true
			}
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}
