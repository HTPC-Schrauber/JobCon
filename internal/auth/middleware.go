package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"jobcon/internal/db"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type contextKey string

const (
	UserKey contextKey = "jobcon_user"
)

type Session struct {
	User      *db.User
	ExpiresAt time.Time
}

type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]Session
	ttl      time.Duration
}

func NewSessionManager(ttl time.Duration) *SessionManager {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	sm := &SessionManager{
		sessions: make(map[string]Session),
		ttl:      ttl,
	}

	// Periodic cleanup of expired sessions
	go func() {
		ticker := time.NewTicker(15 * time.Minute)
		for range ticker.C {
			sm.mu.Lock()
			now := time.Now()
			for token, s := range sm.sessions {
				if now.After(s.ExpiresAt) {
					delete(sm.sessions, token)
				}
			}
			sm.mu.Unlock()
		}
	}()

	return sm
}

func (sm *SessionManager) CreateSession(user *db.User) string {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	token := uuid.New().String()
	sm.sessions[token] = Session{
		User:      user,
		ExpiresAt: time.Now().Add(sm.ttl),
	}
	return token
}

func (sm *SessionManager) GetSession(token string) (*db.User, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	s, exists := sm.sessions[token]
	if !exists || time.Now().After(s.ExpiresAt) {
		return nil, false
	}
	return s.User, true
}

func (sm *SessionManager) DestroySession(token string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	delete(sm.sessions, token)
}

// UserFromContext retrieves the authenticated user from the request context
func UserFromContext(ctx context.Context) *db.User {
	if u, ok := ctx.Value(UserKey).(*db.User); ok {
		return u
	}
	return nil
}

type Middleware struct {
	authenticator Authenticator
	db            *db.DB
	sessions      *SessionManager
}

func NewMiddleware(auth Authenticator, database *db.DB, sessions *SessionManager) *Middleware {
	return &Middleware{
		authenticator: auth,
		db:            database,
		sessions:      sessions,
	}
}

// AuthenticateRequest inspects the request for Bearer token, BasicAuth, or Session cookie
func (m *Middleware) AuthenticateRequest(r *http.Request) (*db.User, error) {
	// 1. API Bearer Token
	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		rawToken := strings.TrimPrefix(authHeader, "Bearer ")
		hash := sha256.Sum256([]byte(rawToken))
		tokenHash := hex.EncodeToString(hash[:])

		apiToken, err := m.db.GetAPITokenByHash(tokenHash)
		if err == nil {
			_ = m.db.UpdateAPITokenLastUsed(apiToken.ID)
			return &db.User{
				ID:          "token:" + apiToken.ID,
				Username:    "token:" + apiToken.Name,
				DisplayName: "API Token: " + apiToken.Name,
				Role:        apiToken.Role,
				AuthSource:  "token",
				IsActive:    true,
			}, nil
		}
	}

	// 2. HTTP BasicAuth
	if username, password, ok := r.BasicAuth(); ok {
		user, err := m.authenticator.Authenticate(r.Context(), username, password)
		if err == nil {
			return user, nil
		}
	}

	// 3. Session Cookie
	cookie, err := r.Cookie("jobcon_session")
	if err == nil && cookie.Value != "" {
		if user, ok := m.sessions.GetSession(cookie.Value); ok {
			return user, nil
		}
	}

	return nil, errors.New("unauthenticated")
}

// Wrap applies authentication to all incoming requests, injecting user into context if present
func (m *Middleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, _ := m.AuthenticateRequest(r)
		if user != nil {
			ctx := context.WithValue(r.Context(), UserKey, user)
			r = r.WithContext(ctx)
		}
		next.ServeHTTP(w, r)
	})
}

// RequireAuth blocks unauthenticated requests
func (m *Middleware) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := UserFromContext(r.Context())
		if user == nil {
			user, _ = m.AuthenticateRequest(r)
			if user != nil {
				ctx := context.WithValue(r.Context(), UserKey, user)
				r = r.WithContext(ctx)
			}
		}

		if user == nil {
			if strings.HasPrefix(r.URL.Path, "/api/") || r.Header.Get("Accept") == "application/json" {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireRole checks if the authenticated user has sufficient role privileges
func (m *Middleware) RequireRole(requiredRole string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := UserFromContext(r.Context())
			if user == nil {
				if strings.HasPrefix(r.URL.Path, "/api/") {
					http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
					return
				}
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}

			if !HasPermission(user.Role, requiredRole) {
				if strings.HasPrefix(r.URL.Path, "/api/") {
					http.Error(w, `{"error":"forbidden","message":"insufficient permissions"}`, http.StatusForbidden)
					return
				}
				http.Error(w, "Forbidden: Sie verfügen nicht über die erforderliche Berechtigung.", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
