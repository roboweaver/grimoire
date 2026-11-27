package php

import (
	"math"
	"testing"
)

// Real WordPress writes doubles in _wp_attachment_metadata (for example
// `s:6:"height";d:1600;`). Before these existed, one double made the whole
// value undecodable, so a media delete left every size file on disk.
func TestUnserializeDouble(t *testing.T) {
	cases := []struct {
		in   string
		want float64
	}{
		{"d:1600;", 1600},
		{"d:0.5;", 0.5},
		{"d:-2.25;", -2.25},
		{"d:1.0E+25;", 1.0e25},
		{"d:1.0E-5;", 1.0e-5},
		{"d:INF;", math.Inf(1)},
		{"d:-INF;", math.Inf(-1)},
	}
	for _, c := range cases {
		v, err := Unserialize(c.in)
		if err != nil {
			t.Errorf("Unserialize(%q) error: %v", c.in, err)
			continue
		}
		f, ok := v.(float64)
		if !ok || f != c.want {
			t.Errorf("Unserialize(%q) = %#v, want float64 %v", c.in, v, c.want)
		}
	}
	v, err := Unserialize("d:NAN;")
	if f, ok := v.(float64); err != nil || !ok || !math.IsNaN(f) {
		t.Errorf("Unserialize(d:NAN;) = %#v, %v; want NaN", v, err)
	}
}

func TestUnserializeDoubleNestedInArray(t *testing.T) {
	v, err := Unserialize(`a:2:{s:5:"width";i:1600;s:6:"height";d:1600;}`)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	m := v.(map[string]any)
	if m["width"] != 1600 || m["height"] != float64(1600) {
		t.Fatalf("got %#v", m)
	}
}

func TestUnserializeDoubleMalformed(t *testing.T) {
	for _, in := range []string{"d:;", "d:1.2.3;", "d:abc;", "d:12", "d:0x1p3;", "d:1_0;", "d:inf;", "d:nan;", "d:+;", "d:1e;"} {
		if v, err := Unserialize(in); err == nil {
			t.Errorf("Unserialize(%q) = %#v, want an error", in, v)
		}
	}
}

// Serialize deliberately does not accept float64: no write path relies on it,
// and widening it is a separate decision.
func TestSerializeStillRejectsFloat(t *testing.T) {
	if _, err := Serialize(1.5); err == nil {
		t.Fatal("Serialize(float64) succeeded; it must keep returning an error")
	}
}
