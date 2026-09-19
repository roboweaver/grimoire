package migrate

import "testing"

func TestLikePrefixEscapesWildcards(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			// The case that matters in practice: a WordPress prefix ends in an
			// underscore, which LIKE would otherwise treat as "any character",
			// so wp_% would also match a wpx_ table.
			name: "trailing underscore is escaped",
			in:   "wp_",
			want: `wp\_`,
		},
		{"no metacharacters", "wp", "wp"},
		{"percent", "a%b", `a\%b`},
		{"backslash escaped first", `a\b`, `a\\b`},
		{"combination", `w_p%x\y`, `w\_p\%x\\y`},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := likePrefix(tc.in); got != tc.want {
				t.Errorf("likePrefix(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestSyncIdentitySequencesSkipsNonPostgres checks the guard clause without a
// database. Only PostgreSQL leaves its key generator behind after an explicit-id
// insert, so the other vendors must return immediately -- and must not touch the
// handle, which is why passing nil here is a valid assertion rather than a
// shortcut.
func TestSyncIdentitySequencesSkipsNonPostgres(t *testing.T) {
	for _, vendor := range []string{"mysql", "sqlite", "", "cockroach"} {
		t.Run(vendor, func(t *testing.T) {
			if err := SyncIdentitySequences(t.Context(), nil, vendor, "wp_"); err != nil {
				t.Errorf("SyncIdentitySequences(%q) = %v, want nil", vendor, err)
			}
		})
	}
}
