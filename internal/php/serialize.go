package php

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// Serialize encodes a Go value into PHP's serialize() text format.
//
// Supported types: nil (and any nil pointer), bool, int, string, and
// map[string]any / map[string]bool (encoded as a PHP associative array with
// string keys). Nil encodes as PHP's null (N;), matching how WordPress
// stores an Application Password's last_used/last_ip before first use.
// String lengths are counted in bytes, matching PHP semantics. Map keys are
// emitted in sorted order so that output is deterministic for identical
// inputs.
func Serialize(v any) (string, error) {
	var b strings.Builder
	if err := serializeValue(&b, v); err != nil {
		return "", err
	}
	return b.String(), nil
}

func serializeValue(b *strings.Builder, v any) error {
	if v == nil || isNilPointer(v) {
		b.WriteString("N;")
		return nil
	}
	switch val := v.(type) {
	case bool:
		if val {
			b.WriteString("b:1;")
		} else {
			b.WriteString("b:0;")
		}
	case int:
		b.WriteString("i:")
		b.WriteString(strconv.Itoa(val))
		b.WriteByte(';')
	case string:
		serializeString(b, val)
	case map[string]any:
		return serializeMap(b, val)
	case map[string]bool:
		m := make(map[string]any, len(val))
		for k, vv := range val {
			m[k] = vv
		}
		return serializeMap(b, m)
	default:
		return fmt.Errorf("php: unsupported type %T", v)
	}
	return nil
}

func serializeString(b *strings.Builder, s string) {
	b.WriteString("s:")
	b.WriteString(strconv.Itoa(len(s)))
	b.WriteString(`:"`)
	b.WriteString(s)
	b.WriteString(`";`)
}

func serializeMap(b *strings.Builder, m map[string]any) error {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	b.WriteString("a:")
	b.WriteString(strconv.Itoa(len(m)))
	b.WriteString(":{")
	for _, k := range keys {
		serializeString(b, k)
		if err := serializeValue(b, m[k]); err != nil {
			return err
		}
	}
	b.WriteByte('}')
	return nil
}

// isNilPointer reports whether v holds a nil pointer of any type (e.g.
// (*string)(nil)), which a plain `v == nil` comparison misses because v is a
// non-nil interface wrapping a nil pointer.
func isNilPointer(v any) bool {
	rv := reflect.ValueOf(v)
	return rv.Kind() == reflect.Ptr && rv.IsNil()
}
