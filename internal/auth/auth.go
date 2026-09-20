package auth

import (
	"context"
	"jobcon/internal/db"
)

const (
	RoleAdmin    = "admin"
	RoleOperator = "operator"
	RoleViewer   = "viewer"
)

// Authenticator is the pluggable interface for authentication (Local, LDAP/AD, etc.)
type Authenticator interface {
	Authenticate(ctx context.Context, username, password string) (*db.User, error)
}

// RoleLevels defines the hierarchy of roles (higher number = more privileges)
var roleLevels = map[string]int{
	RoleViewer:   1,
	RoleOperator: 2,
	RoleAdmin:    3,
}

// HasPermission checks if a user's role satisfies the required role level
func HasPermission(userRole, requiredRole string) bool {
	uLevel, ok1 := roleLevels[userRole]
	rLevel, ok2 := roleLevels[requiredRole]
	if !ok1 || !ok2 {
		return false
	}
	return uLevel >= rLevel
}
