package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"jobcon/internal/db"
	"math/big"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrUserInactive       = errors.New("user account is inactive")
)

type LocalAuthenticator struct {
	db *db.DB
}

func NewLocalAuthenticator(database *db.DB) *LocalAuthenticator {
	return &LocalAuthenticator{db: database}
}

func (a *LocalAuthenticator) Authenticate(ctx context.Context, username, password string) (*db.User, error) {
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

	if !CheckPassword(u.PasswordHash, password) {
		return nil, ErrInvalidCredentials
	}

	// Update last login timestamp asynchronously or synchronously
	_ = a.db.UpdateUserLastLogin(u.ID)
	return u, nil
}

// HashPassword hashes a plain-text password using bcrypt
func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(bytes), err
}

// CheckPassword compares a bcrypt hashed password with its plain-text equivalent
func CheckPassword(hash, password string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// GenerateRandomPassword creates a secure 16-character alphanumeric password
func GenerateRandomPassword(length int) (string, error) {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%^&*-_"
	result := make([]byte, length)
	for i := 0; i < length; i++ {
		num, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			return "", err
		}
		result[i] = charset[num.Int64()]
	}
	return string(result), nil
}

// EnsureAdminUser checks if any user exists in the database. If not, it creates a default admin user.
func EnsureAdminUser(database *db.DB, explicitPassword string) (string, error) {
	count, err := database.CountUsers()
	if err != nil {
		return "", fmt.Errorf("failed to count users: %w", err)
	}
	if count > 0 {
		return "", nil // Admin or users already exist
	}

	adminPassword := explicitPassword
	var generated bool
	if adminPassword == "" {
		adminPassword, err = GenerateRandomPassword(16)
		if err != nil {
			return "", fmt.Errorf("failed to generate random admin password: %w", err)
		}
		generated = true
	}

	hash, err := HashPassword(adminPassword)
	if err != nil {
		return "", fmt.Errorf("failed to hash admin password: %w", err)
	}

	adminUser := &db.User{
		ID:           uuid.New().String(),
		Username:     "admin",
		PasswordHash: hash,
		DisplayName:  "Administrator",
		Email:        "admin@jobcon.local",
		Role:         RoleAdmin,
		AuthSource:   "local",
		IsActive:     true,
	}

	if err := database.CreateUser(adminUser); err != nil {
		return "", fmt.Errorf("failed to create admin user: %w", err)
	}

	if generated {
		return adminPassword, nil
	}
	return "", nil
}
