package auth

import (
	"context"
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
