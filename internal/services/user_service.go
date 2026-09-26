package services

import (
	"context"
	"errors"
	"jobcon/internal/auth"
	"jobcon/internal/db"
	"jobcon/internal/domain"

	"github.com/google/uuid"
)

type UserService struct {
	db *db.DB
}

func NewUserService(database *db.DB) *UserService {
	return &UserService{
		db: database,
	}
}

func (s *UserService) ListUsers() ([]domain.User, error) {
	return s.db.ListUsers()
}

func (s *UserService) GetUser(id string) (*domain.User, error) {
	return s.db.GetUserByID(id)
}

func (s *UserService) CreateUser(ctx context.Context, cmd CreateUserCommand) (*domain.User, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}

	authSource := cmd.AuthSource
	if authSource == "" {
		authSource = "local"
	}

	var hash string
	if authSource == "local" {
		if cmd.Password == "" {
			return nil, errors.New("password is required for local users")
		}
		var err error
		hash, err = auth.HashPassword(cmd.Password)
		if err != nil {
			return nil, err
		}
	} else {
		if cmd.DisplayName == "" {
			cmd.DisplayName = cmd.Username
		}
	}

	role := cmd.Role
	if role == "" {
		role = "viewer"
	}

	user := domain.User{
		ID:           uuid.New().String(),
		Username:     cmd.Username,
		PasswordHash: hash,
		DisplayName:  cmd.DisplayName,
		Email:        cmd.Email,
		Role:         role,
		AuthSource:   authSource,
		IsActive:     true, // Default to true
	}
	if err := s.db.CreateUser(&user); err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *UserService) UpdateUser(ctx context.Context, cmd UpdateUserCommand) error {
	if err := cmd.Validate(); err != nil {
		return err
	}
	user := domain.User{
		ID:          cmd.ID,
		Username:    cmd.Username,
		DisplayName: cmd.DisplayName,
		Email:       cmd.Email,
		Role:        cmd.Role,
		AuthSource:  cmd.AuthSource,
		IsActive:    cmd.IsActive,
	}
	return s.db.UpdateUser(&user)
}

func (s *UserService) DeleteUser(ctx context.Context, id string) error {
	return s.db.DeleteUser(id)
}

func (s *UserService) UpdatePassword(ctx context.Context, cmd UpdatePasswordCommand) error {
	if err := cmd.Validate(); err != nil {
		return err
	}
	hash, err := auth.HashPassword(cmd.Password)
	if err != nil {
		return err
	}
	return s.db.UpdateUserPassword(cmd.UserID, hash)
}
