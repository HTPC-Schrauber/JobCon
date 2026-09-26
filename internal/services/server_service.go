package services

import (
	"context"
	"jobcon/internal/db"
	"jobcon/internal/domain"
)

type ServerService struct {
	db *db.DB
}

func NewServerService(database *db.DB) *ServerService {
	return &ServerService{
		db: database,
	}
}

func (s *ServerService) ListServers() ([]domain.Server, error) {
	return s.db.ListServers()
}

func (s *ServerService) GetServer(id string) (*domain.Server, error) {
	return s.db.GetServer(id)
}

func (s *ServerService) CreateServer(ctx context.Context, cmd CreateServerCommand) error {
	if err := cmd.Validate(); err != nil {
		return err
	}
	return s.db.CreateServer(&cmd.Server)
}

func (s *ServerService) UpdateServer(ctx context.Context, cmd UpdateServerCommand) error {
	if err := cmd.Validate(); err != nil {
		return err
	}
	cmd.Server.ID = cmd.ID
	return s.db.UpdateServer(&cmd.Server)
}

func (s *ServerService) DeleteServer(ctx context.Context, id string) error {
	return s.db.DeleteServer(id)
}
