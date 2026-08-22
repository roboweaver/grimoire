package auth

import (
	"reflect"
	"sort"
	"testing"

	"github.com/roboweaver/grimoire/internal/php"
)

func TestParseCapabilities(t *testing.T) {
	cases := []struct {
		name       string
		serialized string
		want       []string
	}{
		{
			name:       "single administrator",
			serialized: `a:1:{s:13:"administrator";b:1;}`,
			want:       []string{"administrator"},
		},
		{
			name:       "multiple roles",
			serialized: `a:2:{s:6:"author";b:1;s:11:"contributor";b:1;}`,
			want:       []string{"author", "contributor"},
		},
		{
			name:       "false-valued keys are ignored",
			serialized: `a:2:{s:6:"editor";b:1;s:10:"subscriber";b:0;}`,
			want:       []string{"editor"},
		},
		{
			name:       "custom capability key retained",
			serialized: `a:2:{s:9:"my_custom";b:1;s:6:"author";b:1;}`,
			want:       []string{"author", "my_custom"},
		},
		{
			name:       "empty array",
			serialized: `a:0:{}`,
			want:       []string{},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseCapabilities(c.serialized)
			if err != nil {
				t.Fatalf("ParseCapabilities(%q) error: %v", c.serialized, err)
			}
			sort.Strings(got)
			want := append([]string(nil), c.want...)
			sort.Strings(want)
			if len(got) == 0 && len(want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("ParseCapabilities(%q) = %v, want %v", c.serialized, got, want)
			}
		})
	}
}

func TestParseCapabilities_invalid(t *testing.T) {
	if _, err := ParseCapabilities("not-serialized"); err == nil {
		t.Errorf("expected error for malformed capabilities string")
	}
}

func TestSerializeCapabilities_roundTrip(t *testing.T) {
	s, err := SerializeCapabilities(RoleAdministrator)
	if err != nil {
		t.Fatalf("SerializeCapabilities error: %v", err)
	}
	// Must be valid PHP and decode back to the same role via the real unserializer.
	v, err := php.Unserialize(s)
	if err != nil {
		t.Fatalf("Unserialize(%q) error: %v", s, err)
	}
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", v)
	}
	if b, _ := m["administrator"].(bool); !b {
		t.Errorf("serialized capabilities missing administrator=true: %q", s)
	}
	roles, err := ParseCapabilities(s)
	if err != nil {
		t.Fatalf("ParseCapabilities error: %v", err)
	}
	if len(roles) != 1 || roles[0] != RoleAdministrator {
		t.Errorf("round-trip roles = %v, want [administrator]", roles)
	}
}

func TestNewPrincipal_capsFromRoles(t *testing.T) {
	p := NewPrincipal(7, "jane", []string{RoleEditor})
	if p.UserID != 7 || p.Login != "jane" {
		t.Errorf("principal identity = (%d,%q), want (7,jane)", p.UserID, p.Login)
	}
	if !p.Can("edit_others_posts") {
		t.Errorf("editor principal should be able to edit_others_posts")
	}
	if p.Can("manage_options") {
		t.Errorf("editor principal must not be able to manage_options")
	}
}

func TestNewPrincipal_customCapRetained(t *testing.T) {
	p := NewPrincipal(3, "bob", []string{RoleSubscriber, "special_power"})
	if !p.Can("read") {
		t.Errorf("subscriber should be able to read")
	}
	if !p.Can("special_power") {
		t.Errorf("explicit custom capability should be granted")
	}
	if p.Can("edit_posts") {
		t.Errorf("subscriber+custom must not gain edit_posts")
	}
}

func TestPrincipalFromCapabilities(t *testing.T) {
	p, err := PrincipalFromCapabilities(1, "admin", `a:1:{s:13:"administrator";b:1;}`)
	if err != nil {
		t.Fatalf("PrincipalFromCapabilities error: %v", err)
	}
	if !p.Can("manage_options") || !p.Can("edit_users") {
		t.Errorf("administrator principal should hold manage_options and edit_users")
	}
	found := false
	for _, r := range p.Roles {
		if r == RoleAdministrator {
			found = true
		}
	}
	if !found {
		t.Errorf("principal roles = %v, want administrator present", p.Roles)
	}
}

func TestPrincipal_CanNil(t *testing.T) {
	var p Principal
	if p.Can("read") {
		t.Errorf("zero-value principal should not have any capability")
	}
}
