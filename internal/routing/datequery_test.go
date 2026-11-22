package routing_test

import (
	"testing"

	"github.com/roboweaver/grimoire/internal/routing"
)

// M16 task 6.1 -- TDD RED step for the ?m= date query parser.
//
// ParseDateQuery and DateQuery do not exist yet (task 6.2), so this file is
// expected NOT to compile; the compile failure naming those two symbols is the
// correct RED signal.

func TestParseDateQuery_Accepts(t *testing.T) {
	cases := []struct {
		in   string
		want routing.DateRef
	}{
		{"2024", routing.DateRef{Year: 2024}},
		{"202405", routing.DateRef{Year: 2024, Month: 5}},
		{"20240517", routing.DateRef{Year: 2024, Month: 5, Day: 17}},
		{"20240229", routing.DateRef{Year: 2024, Month: 2, Day: 29}}, // leap day
		{"202402", routing.DateRef{Year: 2024, Month: 2}},
		{"0999", routing.DateRef{Year: 999}}, // zero-padded year; Range accepts Year > 0
	}
	for _, c := range cases {
		got, ok := routing.ParseDateQuery(c.in)
		if !ok || got != c.want {
			t.Errorf("ParseDateQuery(%q) = %+v, %v; want %+v, true", c.in, got, ok, c.want)
		}
	}
}

func TestParseDateQuery_Rejects(t *testing.T) {
	cases := []string{
		"", "202", "20245", "2024051", "202405170",
		"202413", "202400", "20240230", "20240500", "20230229",
		"+20240", "2024 05", " 2024", "2024-05",
		"２０２４",       // full-width digits
		"2024\n",     // trailing newline
		"2024051712", // hour form
		"0000",       // year 0: DateRef.Range rejects Year <= 0
	}
	for _, in := range cases {
		if got, ok := routing.ParseDateQuery(in); ok {
			t.Errorf("ParseDateQuery(%q) = %+v, true; want rejected", in, got)
		}
	}
}

// Every accepted value must be consistent with DateRef.Range, the calendar
// authority: an accepted DateRef always has a valid Range.
func TestParseDateQuery_AcceptedImpliesRange(t *testing.T) {
	for _, in := range []string{"2024", "202405", "20240517", "20240229", "202402"} {
		d, ok := routing.ParseDateQuery(in)
		if !ok {
			t.Fatalf("ParseDateQuery(%q) rejected", in)
		}
		if _, _, rok := d.Range(); !rok {
			t.Errorf("ParseDateQuery(%q) = %+v but Range() is not ok", in, d)
		}
	}
}

func TestDateQuery_RoundTrip(t *testing.T) {
	for _, in := range []string{"2024", "202405", "20240517", "20240229", "202402", "0999"} {
		d, ok := routing.ParseDateQuery(in)
		if !ok {
			t.Fatalf("ParseDateQuery(%q) rejected", in)
		}
		if got := routing.DateQuery(d); got != in {
			t.Errorf("DateQuery(ParseDateQuery(%q)) = %q", in, got)
		}
	}
}

func TestDateQuery_Granularity(t *testing.T) {
	cases := []struct {
		d    routing.DateRef
		want string
	}{
		{routing.DateRef{Year: 2024}, "2024"},
		{routing.DateRef{Year: 2024, Month: 5}, "202405"},
		{routing.DateRef{Year: 2024, Month: 5, Day: 7}, "20240507"},
	}
	for _, c := range cases {
		if got := routing.DateQuery(c.d); got != c.want {
			t.Errorf("DateQuery(%+v) = %q; want %q", c.d, got, c.want)
		}
	}
}
