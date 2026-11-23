package php

import (
	"math/rand"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"testing/quick"
)

func TestSerializeIntMap(t *testing.T) {
	got, err := Serialize(map[int]string{12: "1", 7: "0", 30: "spam"})
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	want := `a:3:{i:7;s:1:"0";i:12;s:1:"1";i:30;s:4:"spam";}`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSerializeEmptyIntMap(t *testing.T) {
	got, err := Serialize(map[int]string{})
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	if got != "a:0:{}" {
		t.Fatalf("got %q, want %q", got, "a:0:{}")
	}
}

// Feature: trash-and-restore, Property 5: Integer-keyed PHP arrays round-trip
//
// Validates: Requirements 4.5
func TestPropertySerializeIntMapRoundTrip(t *testing.T) {
	prop := func(m map[int]string) bool {
		out, err := Serialize(m)
		if err != nil {
			return false
		}
		back, err := Unserialize(out)
		if err != nil {
			return false
		}
		got, ok := back.(map[string]any)
		if !ok {
			return false
		}
		want := make(map[string]any, len(m))
		for k, v := range m {
			want[strconv.Itoa(k)] = v
		}
		if !reflect.DeepEqual(got, want) {
			return false
		}
		// Keys must appear in ascending numeric order in the output.
		keys := make([]int, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Ints(keys)
		pos := 0
		for _, k := range keys {
			marker := "i:" + strconv.Itoa(k) + ";s:"
			i := strings.Index(out[pos:], marker)
			if i < 0 {
				return false
			}
			pos += i + len(marker)
		}
		return true
	}
	cfg := &quick.Config{
		MaxCount: 200,
		Rand:     rand.New(rand.NewSource(5)),
		Values: func(args []reflect.Value, r *rand.Rand) {
			m := make(map[int]string)
			for i, n := 0, r.Intn(12); i < n; i++ {
				b := make([]byte, r.Intn(10))
				for j := range b {
					b[j] = byte('a' + r.Intn(26))
				}
				m[r.Intn(2000)-100] = string(b)
			}
			args[0] = reflect.ValueOf(m)
		},
	}
	if err := quick.Check(prop, cfg); err != nil {
		t.Fatal(err)
	}
}
