package auth

import (
	"context"
	"jobcon/internal/config"
	"jobcon/internal/db"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalAuthAndAdminInitialization(t *testing.T) {
	tmpDir := t.TempDir()
	database, err := db.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	// 1. Ensure initial admin user is created
	adminPass, err := EnsureAdminUser(database, "SecretPassword123!")
	if err != nil {
		t.Fatalf("failed to ensure admin user: %v", err)
	}
	if adminPass != "" {
		t.Errorf("expected no generated password when explicit one provided")
	}

	// 2. Authenticate admin
	authenticator := NewLocalAuthenticator(database)
	user, err := authenticator.Authenticate(context.Background(), "admin", "SecretPassword123!")
	if err != nil {
		t.Fatalf("failed to authenticate admin: %v", err)
	}
	if user.Role != RoleAdmin {
		t.Errorf("expected role admin, got %s", user.Role)
	}

	// 3. Test invalid password
	_, err = authenticator.Authenticate(context.Background(), "admin", "WrongPassword")
	if err != ErrInvalidCredentials {
		t.Errorf("expected ErrInvalidCredentials, got %v", err)
	}

	// 4. Test Role permissions
	if !HasPermission(RoleAdmin, RoleOperator) {
		t.Errorf("admin should have operator permission")
	}
	if !HasPermission(RoleOperator, RoleViewer) {
		t.Errorf("operator should have viewer permission")
	}
	if HasPermission(RoleViewer, RoleOperator) {
		t.Errorf("viewer should not have operator permission")
	}

	// 5. Test Middleware with BasicAuth
	sessions := NewSessionManager(1 * time.Hour)
	mw := NewMiddleware(authenticator, database, sessions)

	handlerCalled := false
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := UserFromContext(r.Context())
		if u == nil || u.Username != "admin" {
			t.Errorf("expected admin user in context, got %v", u)
		}
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	protectedHandler := mw.Wrap(mw.RequireAuth(mw.RequireRole(RoleAdmin)(testHandler)))

	req := httptest.NewRequest("GET", "/api/v1/test", nil)
	req.SetBasicAuth("admin", "SecretPassword123!")
	rec := httptest.NewRecorder()

	protectedHandler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}
	if !handlerCalled {
		t.Errorf("handler was not called")
	}
}

func TestGenerateAPIToken(t *testing.T) {
	token, err := GenerateAPIToken(40)
	if err != nil {
		t.Fatalf("unexpected error generating token: %v", err)
	}

	if !strings.HasPrefix(token, "jobcon_") {
		t.Fatalf("expected token to start with 'jobcon_', got %s", token)
	}

	secret := strings.TrimPrefix(token, "jobcon_")
	if len(secret) != 40 {
		t.Fatalf("expected secret length 40, got %d (%s)", len(secret), secret)
	}

	for _, c := range secret {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			t.Fatalf("token secret contains non-alphanumeric character: %c", c)
		}
	}
}

func TestBuildUserBindIdentity(t *testing.T) {
	// Case 1: user provided full UPN
	cfg := config.LDAPConfig{}
	id := BuildUserBindIdentity("user@domain.com", cfg)
	if id != "user@domain.com" {
		t.Errorf("expected user@domain.com, got %s", id)
	}

	// Case 2: user provided DOMAIN\user
	id = BuildUserBindIdentity(`CORP\jdoe`, cfg)
	if id != `CORP\jdoe` {
		t.Errorf(`expected CORP\jdoe, got %s`, id)
	}

	// Case 3: configured UserBindTemplate
	cfg.UserBindTemplate = "%s@intern.firma.de"
	id = BuildUserBindIdentity("m.mustermann", cfg)
	if id != "m.mustermann@intern.firma.de" {
		t.Errorf("expected m.mustermann@intern.firma.de, got %s", id)
	}

	// Case 4: derived from BaseDN
	cfg.UserBindTemplate = ""
	cfg.BaseDN = "OU=Benutzer,DC=ad,DC=firma,DC=local"
	id = BuildUserBindIdentity("jdoe", cfg)
	if id != "jdoe@ad.firma.local" {
		t.Errorf("expected jdoe@ad.firma.local, got %s", id)
	}
}

func TestGetEffectiveLDAPConfig(t *testing.T) {
	tmpDir := t.TempDir()
	database, err := db.Open(filepath.Join(tmpDir, "test_ldap_cfg.db"))
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer database.Close()

	baseCfg := config.LDAPConfig{
		Enabled: false,
		Host:    "default.host",
		UseSSL:  true,
	}

	// Initial effective config
	eff := GetEffectiveLDAPConfig(database, baseCfg)
	if eff.Port != 636 {
		t.Errorf("expected default port 636 for SSL, got %d", eff.Port)
	}
	if eff.AttrUsername != "sAMAccountName" || eff.AttrFirstName != "givenName" || eff.AttrLastName != "sn" || eff.AttrEmail != "mail" {
		t.Errorf("unexpected default attributes: %+v", eff)
	}

	// Set overrides in DB
	_ = database.SetSetting("ldap_host", "custom.ldap.server")
	_ = database.SetSetting("ldap_port", "389")
	_ = database.SetSetting("ldap_use_ssl", "false")
	_ = database.SetSetting("ldap_attr_username", "uid")

	eff = GetEffectiveLDAPConfig(database, baseCfg)
	if eff.Host != "custom.ldap.server" {
		t.Errorf("expected host custom.ldap.server, got %s", eff.Host)
	}
	if eff.Port != 389 {
		t.Errorf("expected port 389, got %d", eff.Port)
	}
	if eff.UseSSL != false {
		t.Errorf("expected UseSSL false")
	}
	if eff.AttrUsername != "uid" {
		t.Errorf("expected AttrUsername uid, got %s", eff.AttrUsername)
	}
}

func TestMultiAuthenticatorLocalUser(t *testing.T) {
	tmpDir := t.TempDir()
	database, err := db.Open(filepath.Join(tmpDir, "test_multi.db"))
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer database.Close()

	cfg := config.DefaultConfig()
	multiAuth := NewMultiAuthenticator(database, cfg)

	hash, _ := HashPassword("LocalPass123!")
	localUser := &db.User{
		ID:           "u1",
		Username:     "alice",
		PasswordHash: hash,
		DisplayName:  "Alice Local",
		Role:         "operator",
		AuthSource:   "local",
		IsActive:     true,
	}
	_ = database.CreateUser(localUser)

	// Successful login
	u, err := multiAuth.Authenticate(context.Background(), "alice", "LocalPass123!")
	if err != nil || u.ID != "u1" {
		t.Fatalf("expected successful local authentication, got u=%v, err=%v", u, err)
	}

	// Wrong password
	_, err = multiAuth.Authenticate(context.Background(), "alice", "WrongPass")
	if err != ErrInvalidCredentials {
		t.Errorf("expected ErrInvalidCredentials, got %v", err)
	}

	// Inactive user
	localUser.IsActive = false
	_ = database.UpdateUser(localUser)
	_, err = multiAuth.Authenticate(context.Background(), "alice", "LocalPass123!")
	if err != ErrUserInactive {
		t.Errorf("expected ErrUserInactive, got %v", err)
	}
}

