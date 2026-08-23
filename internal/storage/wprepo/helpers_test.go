package wprepo

import (
	"testing"
	"time"
)

func TestParseTS(t *testing.T) {
	want := time.Date(2026, 9, 6, 1, 53, 41, 0, time.UTC)

	cases := []struct {
		name     string
		in       string
		wantZero bool
		want     time.Time
	}{
		{
			name: "space layout (parseTime=false / lexical path)",
			in:   "2026-09-06 01:53:41",
			want: want,
		},
		{
			name: "RFC3339 (parseTime=true scanned into string, whole seconds)",
			in:   "2026-09-06T01:53:41Z",
			want: want,
		},
		{
			name: "RFC3339Nano (fractional seconds variant)",
			in:   "2026-09-06T01:53:41.123456Z",
			want: time.Date(2026, 9, 6, 1, 53, 41, 123456000, time.UTC),
		},
		{
			name:     "empty string yields zero time",
			in:       "",
			wantZero: true,
		},
		{
			name:     "garbage yields zero time",
			in:       "garbage",
			wantZero: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseTS(tc.in)
			if tc.wantZero {
				if !got.IsZero() {
					t.Fatalf("parseTS(%q) = %v, want zero time", tc.in, got)
				}
				return
			}
			if got.IsZero() {
				t.Fatalf("parseTS(%q) returned zero time, want %v", tc.in, tc.want)
			}
			if !got.Equal(tc.want) {
				t.Fatalf("parseTS(%q) = %v, want %v", tc.in, got, tc.want)
			}
			if !got.After(time.Now().Add(-time.Hour)) {
				// sanity: a future timestamp must be After(now)
				t.Fatalf("parseTS(%q) = %v not treated as a real instant", tc.in, got)
			}
		})
	}
}
