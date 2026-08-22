package php

import (
	"fmt"
	"strconv"
)

// Unserialize decodes a PHP serialize() string into a Go value.
//
// It returns bool, int, string, or map[string]any (for PHP associative
// arrays). Array keys are decoded as strings; integer PHP keys are rendered in
// their decimal form. The entire input must be consumed, otherwise an error is
// returned. Unsupported type markers (objects, floats, references, null) yield
// an error rather than a partial decode.
func Unserialize(s string) (any, error) {
	p := &parser{s: s}
	v, err := p.value()
	if err != nil {
		return nil, err
	}
	if p.pos != len(p.s) {
		return nil, fmt.Errorf("php: trailing data at offset %d", p.pos)
	}
	return v, nil
}

type parser struct {
	s   string
	pos int
}

func (p *parser) value() (any, error) {
	if p.pos >= len(p.s) {
		return nil, fmt.Errorf("php: unexpected end of input")
	}
	switch p.s[p.pos] {
	case 'b':
		return p.boolean()
	case 'i':
		return p.integer()
	case 's':
		return p.string()
	case 'a':
		return p.array()
	default:
		return nil, fmt.Errorf("php: unknown type marker %q at offset %d", p.s[p.pos], p.pos)
	}
}

// expect consumes the exact literal at the current position or errors.
func (p *parser) expect(lit string) error {
	if p.pos+len(lit) > len(p.s) || p.s[p.pos:p.pos+len(lit)] != lit {
		return fmt.Errorf("php: expected %q at offset %d", lit, p.pos)
	}
	p.pos += len(lit)
	return nil
}

func (p *parser) boolean() (any, error) {
	if err := p.expect("b:"); err != nil {
		return nil, err
	}
	if p.pos >= len(p.s) {
		return nil, fmt.Errorf("php: unexpected end of input in bool")
	}
	var b bool
	switch p.s[p.pos] {
	case '1':
		b = true
	case '0':
		b = false
	default:
		return nil, fmt.Errorf("php: invalid bool value %q at offset %d", p.s[p.pos], p.pos)
	}
	p.pos++
	if err := p.expect(";"); err != nil {
		return nil, err
	}
	return b, nil
}

func (p *parser) integer() (any, error) {
	if err := p.expect("i:"); err != nil {
		return nil, err
	}
	digits, err := p.readUntil(';')
	if err != nil {
		return nil, err
	}
	n, err := strconv.Atoi(digits)
	if err != nil {
		return nil, fmt.Errorf("php: invalid int %q: %w", digits, err)
	}
	return n, nil
}

func (p *parser) string() (any, error) {
	s, err := p.rawString()
	if err != nil {
		return nil, err
	}
	if err := p.expect(";"); err != nil {
		return nil, err
	}
	return s, nil
}

// rawString parses s:<len>:"<bytes>" without the trailing terminator, so it can
// be reused for both scalar strings (which end in ';') and array keys (which do
// not).
func (p *parser) rawString() (string, error) {
	if err := p.expect("s:"); err != nil {
		return "", err
	}
	lenStr, err := p.readUntil(':')
	if err != nil {
		return "", err
	}
	n, err := strconv.Atoi(lenStr)
	if err != nil || n < 0 {
		return "", fmt.Errorf("php: invalid string length %q at offset %d", lenStr, p.pos)
	}
	if err := p.expect(`"`); err != nil {
		return "", err
	}
	if p.pos+n > len(p.s) {
		return "", fmt.Errorf("php: string length %d exceeds input at offset %d", n, p.pos)
	}
	val := p.s[p.pos : p.pos+n]
	p.pos += n
	if err := p.expect(`"`); err != nil {
		return "", fmt.Errorf("php: string length mismatch near offset %d", p.pos)
	}
	return val, nil
}

func (p *parser) array() (any, error) {
	if err := p.expect("a:"); err != nil {
		return nil, err
	}
	countStr, err := p.readUntil(':')
	if err != nil {
		return nil, err
	}
	count, err := strconv.Atoi(countStr)
	if err != nil || count < 0 {
		return nil, fmt.Errorf("php: invalid array count %q", countStr)
	}
	if err := p.expect("{"); err != nil {
		return nil, err
	}
	// Clamp the map size hint: count is attacker-controlled, so we never
	// pre-size from it directly (e.g. a:2000000000:{} would force a huge
	// allocation). The map still grows correctly as real elements are parsed;
	// each element is length/consumption-validated below.
	const maxPrealloc = 1024
	m := make(map[string]any, min(count, maxPrealloc))
	for i := 0; i < count; i++ {
		key, err := p.arrayKey()
		if err != nil {
			return nil, err
		}
		val, err := p.value()
		if err != nil {
			return nil, err
		}
		m[key] = val
	}
	if err := p.expect("}"); err != nil {
		return nil, err
	}
	return m, nil
}

// arrayKey decodes an array key, which PHP serializes as either an integer or a
// string. Both are normalized to a Go string.
func (p *parser) arrayKey() (string, error) {
	if p.pos >= len(p.s) {
		return "", fmt.Errorf("php: unexpected end of input in array key")
	}
	switch p.s[p.pos] {
	case 's':
		s, err := p.rawString()
		if err != nil {
			return "", err
		}
		if err := p.expect(";"); err != nil {
			return "", err
		}
		return s, nil
	case 'i':
		v, err := p.integer()
		if err != nil {
			return "", err
		}
		return strconv.Itoa(v.(int)), nil
	default:
		return "", fmt.Errorf("php: invalid array key marker %q at offset %d", p.s[p.pos], p.pos)
	}
}

// readUntil returns the bytes from the current position up to (but not
// including) delim, advancing past delim. It errors if delim is not found or
// the run is empty.
func (p *parser) readUntil(delim byte) (string, error) {
	start := p.pos
	for p.pos < len(p.s) && p.s[p.pos] != delim {
		p.pos++
	}
	if p.pos >= len(p.s) {
		return "", fmt.Errorf("php: expected %q after offset %d", string(delim), start)
	}
	if p.pos == start {
		return "", fmt.Errorf("php: empty numeric field at offset %d", start)
	}
	out := p.s[start:p.pos]
	p.pos++ // consume delim
	return out, nil
}
