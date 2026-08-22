package auth

import (
	"fmt"
	"sort"

	"github.com/roboweaver/grimoire/internal/php"
)

// Principal is an authenticated actor. Roles lists the WordPress roles assigned
// to the user; Caps is the effective capability set (the union of the roles'
// capabilities plus any explicit capability grants). Authorization is a lookup
// in Caps via Can.
type Principal struct {
	UserID int64
	Login  string
	Roles  []string
	Caps   map[string]bool
}

// Can reports whether the principal holds the named capability.
func (p Principal) Can(capability string) bool {
	return p.Caps[capability]
}

// knownRole reports whether name is one of the five default roles.
func knownRole(name string) bool {
	_, ok := userLevels[name]
	return ok
}

// NewPrincipal builds a Principal from a set of enabled capability keys, as
// stored in {prefix}capabilities. Keys that name a known role expand to that
// role's capability set; any other key is retained as an explicit capability
// grant (so custom capabilities survive). Recognized roles are recorded in
// Roles in a stable order.
func NewPrincipal(userID int64, login string, keys []string) Principal {
	caps := make(map[string]bool)
	var roles []string
	for _, k := range keys {
		if knownRole(k) {
			roles = append(roles, k)
			for c := range CapabilitiesForRoles(k) {
				caps[c] = true
			}
			continue
		}
		caps[k] = true
	}
	sort.Strings(roles)
	return Principal{UserID: userID, Login: login, Roles: roles, Caps: caps}
}

// PrincipalFromCapabilities decodes a PHP-serialized {prefix}capabilities value
// and builds the corresponding Principal.
func PrincipalFromCapabilities(userID int64, login, serialized string) (Principal, error) {
	keys, err := ParseCapabilities(serialized)
	if err != nil {
		return Principal{}, err
	}
	return NewPrincipal(userID, login, keys), nil
}

// ParseCapabilities decodes a PHP-serialized {prefix}capabilities value into the
// list of enabled keys (role names and/or explicit capabilities whose value is
// truthy). Keys mapped to a false/zero value are omitted. The result is never
// nil; an empty capabilities array yields an empty slice.
func ParseCapabilities(serialized string) ([]string, error) {
	v, err := php.Unserialize(serialized)
	if err != nil {
		return nil, err
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("auth: capabilities is %T, want array", v)
	}
	keys := make([]string, 0, len(m))
	for k, val := range m {
		if truthy(val) {
			keys = append(keys, k)
		}
	}
	return keys, nil
}

// truthy reports whether a decoded PHP value denotes an enabled capability.
func truthy(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case int:
		return t != 0
	case string:
		return t != "" && t != "0"
	default:
		return false
	}
}

// SerializeCapabilities encodes the given role names as a WordPress
// {prefix}capabilities value (role name -> true).
func SerializeCapabilities(roles ...string) (string, error) {
	m := make(map[string]any, len(roles))
	for _, r := range roles {
		m[r] = true
	}
	return php.Serialize(m)
}
