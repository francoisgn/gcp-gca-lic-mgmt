package roles

import (
	"os"
	"slices"
	"testing"
)

func TestRoles(t *testing.T) {
	r, err := Parse([]byte(`
super_admins: [Boss@Example.com]
admins: [alice@example.com, boss@example.com, bob@example.com, alice@example.com]
`))
	if err != nil {
		t.Fatal(err)
	}
	if r.RoleOf("BOSS@example.com") != RoleSuperAdmin || r.RoleOf("bob@example.com") != RoleAdmin || r.RoleOf("x@example.com") != RoleUser {
		t.Fatal("RoleOf")
	}
	// Super admins are not listed as contacts after an escalation.
	if got := r.Admins(); !slices.Equal(got, []string{"alice@example.com", "bob@example.com"}) {
		t.Fatalf("Admins() = %v", got)
	}
	if !RoleSuperAdmin.IsAdmin() || RoleUser.IsAdmin() || RoleAdmin.IsSuperAdmin() {
		t.Fatal("role predicates")
	}
	if _, err := Parse([]byte(`admins: [a@example.com]`)); err == nil {
		t.Fatal("a roles file without super admin must be rejected")
	}
}

func TestRepositoryRolesFileIsValid(t *testing.T) {
	raw, err := os.ReadFile("../../config/roles.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(raw); err != nil {
		t.Fatal(err)
	}
}
