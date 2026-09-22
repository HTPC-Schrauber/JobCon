package auth

import (
	"context"
	"errors"
	"jobcon/internal/config"
	"jobcon/internal/db"
)

// MultiAuthenticator coordinates authentication across both local bcrypt accounts and LDAP/Active Directory
type MultiAuthenticator struct {
	db          *db.DB
	localAuth   *LocalAuthenticator
	ldapService *LDAPService
	cfg         *config.Config
}

func NewMultiAuthenticator(database *db.DB, cfg *config.Config) *MultiAuthenticator {
	return &MultiAuthenticator{
		db:          database,
		localAuth:   NewLocalAuthenticator(database),
		ldapService: NewLDAPService(database, cfg),
		cfg:         cfg,
	}
}

func (a *MultiAuthenticator) LDAPService() *LDAPService {
	return a.ldapService
}

func (a *MultiAuthenticator) LocalAuth() *LocalAuthenticator {
	return a.localAuth
}

// Authenticate verifies credentials against the appropriate backend based on the user's auth_source
func (a *MultiAuthenticator) Authenticate(ctx context.Context, username, password string) (*db.User, error) {
	u, err := a.db.GetUserByUsername(username)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}

	if !u.IsActive {
		return nil, ErrUserInactive
	}

	// 1. LDAP / Active Directory user
	if u.AuthSource == "ldap" {
		info, err := a.ldapService.AuthenticateLDAP(username, password)
		if err != nil {
			return nil, ErrInvalidCredentials
		}

		// Synchronize display name and email from LDAP if available
		needsUpdate := false
		if info.DisplayName != "" && (u.DisplayName == "" || u.DisplayName == u.Username) {
			u.DisplayName = info.DisplayName
			needsUpdate = true
		}
		if info.Email != "" && u.Email == "" {
			u.Email = info.Email
			needsUpdate = true
		}
		if needsUpdate {
			_ = a.db.UpdateUser(u)
		}

		_ = a.db.UpdateUserLastLogin(u.ID)
		return u, nil
	}

	// 2. Local user (bcrypt)
	return a.localAuth.Authenticate(ctx, username, password)
}
