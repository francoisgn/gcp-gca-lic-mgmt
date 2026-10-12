// Package roles loads the list of administrators and super administrators
// from a YAML file versioned in git (config/roles.yaml). Any change goes
// through a pull request and a redeploy: there is no admin UI to grant roles,
// on purpose (four-eyes principle, full history in git).
package roles

import (
	"fmt"
	"os"
	"slices"

	"gopkg.in/yaml.v3"

	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/domain"
)

// Role of a portal user. Roles are cumulative: a super admin is also an admin.
type Role string

const (
	RoleUser       Role = "user"
	RoleAdmin      Role = "admin"
	RoleSuperAdmin Role = "superadmin"
)

// IsAdmin reports whether the role grants access to the admin pages.
func (r Role) IsAdmin() bool { return r == RoleAdmin || r == RoleSuperAdmin }

// IsSuperAdmin reports whether the role grants super admin actions
// (force subscription refresh, run jobs, resolve deviations).
func (r Role) IsSuperAdmin() bool { return r == RoleSuperAdmin }

// Label returns the English label displayed in the top bar.
func (r Role) Label() string {
	switch r {
	case RoleSuperAdmin:
		return "Super admin"
	case RoleAdmin:
		return "Admin"
	}
	return "User"
}

// file mirrors the YAML layout.
type file struct {
	SuperAdmins []string `yaml:"super_admins"`
	Admins      []string `yaml:"admins"`
}

// Roles is an immutable, normalised view of the roles file.
type Roles struct {
	superAdmins map[string]bool
	admins      map[string]bool
	adminList   []string // admins that are not super admins, sorted (shown to users on escalation)
}

// Load parses the YAML roles file.
func Load(path string) (*Roles, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read roles file: %w", err)
	}
	return Parse(raw)
}

// Parse builds Roles from YAML content.
func Parse(raw []byte) (*Roles, error) {
	var f file
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("parse roles file: %w", err)
	}
	r := &Roles{superAdmins: map[string]bool{}, admins: map[string]bool{}}
	for _, e := range f.SuperAdmins {
		if e = domain.NormalizeEmail(e); e != "" {
			r.superAdmins[e] = true
		}
	}
	for _, e := range f.Admins {
		if e = domain.NormalizeEmail(e); e != "" {
			r.admins[e] = true
			if !r.superAdmins[e] {
				r.adminList = append(r.adminList, e)
			}
		}
	}
	slices.Sort(r.adminList)
	r.adminList = slices.Compact(r.adminList)
	if len(r.superAdmins) == 0 {
		return nil, fmt.Errorf("roles file must declare at least one super admin")
	}
	return r, nil
}

// RoleOf returns the role of an e-mail address.
func (r *Roles) RoleOf(email string) Role {
	email = domain.NormalizeEmail(email)
	switch {
	case r.superAdmins[email]:
		return RoleSuperAdmin
	case r.admins[email]:
		return RoleAdmin
	}
	return RoleUser
}

// Admins returns the administrators that are not super admins. This is the
// list displayed to a user after an escalation ("who to contact").
func (r *Roles) Admins() []string { return slices.Clone(r.adminList) }

// AllAdmins returns every e-mail allowed on admin pages (admins + super admins).
func (r *Roles) AllAdmins() []string {
	var out []string
	for e := range r.superAdmins {
		out = append(out, e)
	}
	for e := range r.admins {
		if !r.superAdmins[e] {
			out = append(out, e)
		}
	}
	slices.Sort(out)
	return out
}
