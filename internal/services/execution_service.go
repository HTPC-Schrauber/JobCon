package services

import (
	"context"
	"jobcon/internal/db"
	"jobcon/internal/domain"
	"jobcon/internal/runner"
)

type ExecutionService struct {
	db     *db.DB
	runner *runner.ExecutionManager
}

func NewExecutionService(database *db.DB, executionManager *runner.ExecutionManager) *ExecutionService {
	return &ExecutionService{
		db:     database,
		runner: executionManager,
	}
}

func (s *ExecutionService) GetExecution(id string) (*domain.Execution, error) {
	return s.db.GetExecution(id)
}

func (s *ExecutionService) ListExecutions(limit int) ([]domain.Execution, error) {
	return s.db.ListExecutions(limit)
}

func (s *ExecutionService) ListExecutionsForJob(jobID string, limit int) ([]domain.Execution, error) {
	return s.db.ListExecutionsByJob(jobID, limit)
}

func (s *ExecutionService) CancelExecution(ctx context.Context, id string) error {
	return s.runner.AbortExecution(id)
}
