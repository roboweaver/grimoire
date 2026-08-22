package php

import (
	"reflect"
	"testing"
)

func TestSerialize(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
	}{
		{"bool true", true, "b:1;"},
		{"bool false", false, "b:0;"},
		{"int", 42, "i:42;"},
		{"int negative", -7, "i:-7;"},
		{"string", "administrator", `s:13:"administrator";`},
		{"empty string", "", `s:0:"";`},
		{"string utf8 byte length", "café", `s:5:"café";`},
		{
			"capabilities single role",
			map[string]any{"administrator": true},
			`a:1:{s:13:"administrator";b:1;}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Serialize(tt.in)
			if err != nil {
				t.Fatalf("Serialize(%v) error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("Serialize(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestUnserialize(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want any
	}{
		{"bool true", "b:1;", true},
		{"bool false", "b:0;", false},
		{"int", "i:42;", 42},
		{"int negative", "i:-7;", -7},
		{"string", `s:13:"administrator";`, "administrator"},
		{"empty string", `s:0:"";`, ""},
		{"string utf8 byte length", `s:5:"café";`, "café"},
		{
			"capabilities single role",
			`a:1:{s:13:"administrator";b:1;}`,
			map[string]any{"administrator": true},
		},
		{
			"capabilities editor",
			`a:1:{s:6:"editor";b:1;}`,
			map[string]any{"editor": true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Unserialize(tt.in)
			if err != nil {
				t.Fatalf("Unserialize(%q) error: %v", tt.in, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Unserialize(%q) = %#v, want %#v", tt.in, got, tt.want)
			}
		})
	}
}

func TestUnserializeErrors(t *testing.T) {
	bad := []string{
		"",
		"x:1;",
		"b:2;",
		"i:;",
		"i:abc;",
		`s:5:"ab";`,         // declared length mismatch
		`s:13:"admin";`,     // too short
		"a:1:{s:3:\"foo\";", // truncated array
		"b:1",               // missing terminator
		// A huge declared array count with a short/empty body must fail parsing
		// quickly. The map size hint is clamped so this cannot force a giant
		// preallocation from an attacker-controlled number (LOW 3).
		"a:2000000000:{}",
	}
	for _, s := range bad {
		t.Run(s, func(t *testing.T) {
			if _, err := Unserialize(s); err == nil {
				t.Fatalf("Unserialize(%q) = nil error, want error", s)
			}
		})
	}
}

func TestRoundTrip(t *testing.T) {
	values := []any{
		true,
		false,
		0,
		12345,
		"hello world",
		map[string]any{"administrator": true},
		map[string]any{"editor": true, "custom_cap": true},
	}
	for _, v := range values {
		s, err := Serialize(v)
		if err != nil {
			t.Fatalf("Serialize(%#v) error: %v", v, err)
		}
		got, err := Unserialize(s)
		if err != nil {
			t.Fatalf("Unserialize(%q) error: %v", s, err)
		}
		if !reflect.DeepEqual(got, v) {
			t.Fatalf("round-trip %#v -> %q -> %#v", v, s, got)
		}
	}
}
