package rbac

import "testing"

func TestHasRole(t *testing.T) {
	roles := []string{"user", "admin"}
	if !HasRole(roles, "admin") {
		t.Error("expected admin role to be present")
	}
	if !HasRole(roles, "user") {
		t.Error("expected user role to be present")
	}
	if HasRole(roles, "superuser") {
		t.Error("did not expect superuser role")
	}
	if HasRole(nil, "user") {
		t.Error("nil roles should never match")
	}
}
