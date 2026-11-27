package wprepo

import (
	"strconv"
)

// This file holds a byte-span scanner for PHP-serialized values. It exists so
// that an option value shared with WordPress can be edited by removing whole
// top-level array entries without decoding and re-encoding it. Decoding is
// lossy for what real sites store (objects such as stdClass, floats, and
// integer-keyed arrays), so the scanner only locates entry boundaries and
// leaves every byte it does not remove untouched.

// phpScanMaxDepth bounds nesting so a hostile value cannot exhaust the stack.
const phpScanMaxDepth = 128

// phpEntry is one top-level key/value pair of a serialized array. The pair
// occupies s[start:end]; the value alone starts at valStart.
type phpEntry struct {
	start, valStart, end int
	key                  string // decoded key text (decimal text for integer keys)
	strKey               bool   // key was a string (s:), not an integer (i:)
}

// scanPHPArray splits a serialized array `a:N:{...}` into its top-level
// entries. ok is false when s is not exactly one well-formed array (truncated,
// trailing bytes, bad length prefix, unsupported type marker, wrong count).
// Nested values of any supported kind, including objects, are skipped by span.
func scanPHPArray(s string) (entries []phpEntry, ok bool) {
	n, pos, ok := phpOpenAggregate(s, 0, 'a')
	if !ok {
		return nil, false
	}
	for i := 0; i < n; i++ {
		e, next, ok := phpScanEntry(s, pos, 1)
		if !ok {
			return nil, false
		}
		entries = append(entries, e)
		pos = next
	}
	if pos >= len(s) || s[pos] != '}' || pos+1 != len(s) {
		return nil, false
	}
	return entries, true
}

// phpScanEntry scans one key/value pair starting at pos.
func phpScanEntry(s string, pos, depth int) (e phpEntry, next int, ok bool) {
	e.start = pos
	if pos >= len(s) {
		return e, 0, false
	}
	switch s[pos] {
	case 's':
		k, end, ok := phpScanString(s, pos)
		if !ok {
			return e, 0, false
		}
		e.key, e.strKey, pos = k, true, end
	case 'i':
		end, ok := phpSkipInt(s, pos)
		if !ok {
			return e, 0, false
		}
		e.key, pos = s[e.start+2:end-1], end
	default:
		return e, 0, false
	}
	e.valStart = pos
	end, ok := phpSkipValue(s, pos, depth)
	if !ok {
		return e, 0, false
	}
	e.end = end
	return e, end, true
}

// phpSkipValue returns the offset just past the serialized value at pos.
func phpSkipValue(s string, pos, depth int) (int, bool) {
	if pos >= len(s) || depth > phpScanMaxDepth {
		return 0, false
	}
	switch s[pos] {
	case 'N':
		if pos+1 < len(s) && s[pos+1] == ';' {
			return pos + 2, true
		}
	case 'b':
		if pos+3 < len(s) && s[pos+1] == ':' && (s[pos+2] == '0' || s[pos+2] == '1') && s[pos+3] == ';' {
			return pos + 4, true
		}
	case 'i':
		return phpSkipInt(s, pos)
	case 'd':
		if pos+1 >= len(s) || s[pos+1] != ':' {
			return 0, false
		}
		i := pos + 2
		for i < len(s) && s[i] != ';' {
			c := s[i]
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '+' || c == '-' || c == '.') {
				return 0, false
			}
			i++
		}
		if i < len(s) && i > pos+2 {
			return i + 1, true
		}
	case 'r', 'R':
		if pos+1 < len(s) && s[pos+1] == ':' {
			if n, next, ok := phpReadUint(s, pos+2); ok && n >= 0 && next < len(s) && s[next] == ';' {
				return next + 1, true
			}
		}
	case 's':
		_, end, ok := phpScanString(s, pos)
		return end, ok
	case 'a':
		n, next, ok := phpOpenAggregate(s, pos, 'a')
		if !ok {
			return 0, false
		}
		return phpSkipMembers(s, next, n, depth)
	case 'O':
		// O:LEN:"Class":N:{ members }
		if pos+1 >= len(s) || s[pos+1] != ':' {
			return 0, false
		}
		l, next, ok := phpReadUint(s, pos+2)
		if !ok || next+2 > len(s) || s[next] != ':' || s[next+1] != '"' {
			return 0, false
		}
		next += 2
		if l > len(s)-next || next+l+1 >= len(s) || s[next+l] != '"' || s[next+l+1] != ':' {
			return 0, false
		}
		n, next, ok := phpReadUint(s, next+l+2)
		if !ok || next+1 >= len(s) || s[next] != ':' || s[next+1] != '{' {
			return 0, false
		}
		return phpSkipMembers(s, next+2, n, depth)
	}
	return 0, false
}

// phpSkipMembers skips n key/value pairs and the closing brace.
func phpSkipMembers(s string, pos, n, depth int) (int, bool) {
	for i := 0; i < n; i++ {
		_, next, ok := phpScanEntry(s, pos, depth+1)
		if !ok {
			return 0, false
		}
		pos = next
	}
	if pos < len(s) && s[pos] == '}' {
		return pos + 1, true
	}
	return 0, false
}

// phpOpenAggregate parses the `a:N:{` header at pos and returns N and the
// offset of the first member.
func phpOpenAggregate(s string, pos int, marker byte) (n, next int, ok bool) {
	if pos+1 >= len(s) || s[pos] != marker || s[pos+1] != ':' {
		return 0, 0, false
	}
	n, next, ok = phpReadUint(s, pos+2)
	if !ok || next+1 >= len(s) || s[next] != ':' || s[next+1] != '{' {
		return 0, 0, false
	}
	return n, next + 2, true
}

// phpReadUint reads one or more ASCII digits at pos.
func phpReadUint(s string, pos int) (n, next int, ok bool) {
	i := pos
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == pos {
		return 0, 0, false
	}
	v, err := strconv.Atoi(s[pos:i])
	if err != nil {
		return 0, 0, false
	}
	return v, i, true
}

// phpSkipInt skips `i:[+-]?digits;` at pos.
func phpSkipInt(s string, pos int) (int, bool) {
	if pos+1 >= len(s) || s[pos] != 'i' || s[pos+1] != ':' {
		return 0, false
	}
	i := pos + 2
	if i < len(s) && (s[i] == '-' || s[i] == '+') {
		i++
	}
	start := i
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == start || i >= len(s) || s[i] != ';' {
		return 0, false
	}
	return i + 1, true
}

// phpScanString reads `s:LEN:"bytes";` at pos and returns the decoded bytes
// and the offset just past the closing `;`.
func phpScanString(s string, pos int) (val string, end int, ok bool) {
	if pos+1 >= len(s) || s[pos] != 's' || s[pos+1] != ':' {
		return "", 0, false
	}
	l, next, ok := phpReadUint(s, pos+2)
	if !ok || next+1 >= len(s) || s[next] != ':' || s[next+1] != '"' {
		return "", 0, false
	}
	start := next + 2
	if l > len(s)-start || len(s)-start-l < 2 || s[start+l] != '"' || s[start+l+1] != ';' {
		return "", 0, false
	}
	return s[start : start+l], start + l + 2, true
}
