package services

import (
	"context"
	"fmt"
	"jobcon/internal/db"
	"jobcon/internal/domain"
	"jobcon/internal/runner"
)

type JobService struct {
	db     *db.DB
	runner *runner.ExecutionManager
}

func NewJobService(database *db.DB, executionManager *runner.ExecutionManager) *JobService {
	return &JobService{
		db:     database,
		runner: executionManager,
	}
}

// Queries

func (s *JobService) ListJobs() ([]domain.Job, error) {
	return s.db.ListJobs()
}

func (s *JobService) ListJobsPaged(filter domain.JobFilter) (*domain.JobPageResult, error) {
	return s.db.ListJobsPaged(filter)
}

func (s *JobService) GetJob(id string) (*domain.Job, error) {
	return s.db.GetJob(id)
}

// Commands

func (s *JobService) CreateJob(ctx context.Context, cmd CreateJobCommand) error {
	if err := cmd.Validate(); err != nil {
		return err
	}

	if !cmd.Force {
		existing, err := s.db.GetJobsByArtifact(cmd.ArtifactID)
		if err == nil && len(existing) > 0 {
			return fmt.Errorf("a job with artifact '%s' already exists (job ID: '%s', name: '%s'). Use force=true to proceed", cmd.ArtifactID, existing[0].ID, existing[0].Name)
		}
	}

	return s.db.CreateJob(&cmd.Job)
}

func (s *JobService) UpdateJob(ctx context.Context, cmd UpdateJobCommand) error {
	if err := cmd.Validate(); err != nil {
		return err
	}
	cmd.Job.ID = cmd.ID
	return s.db.UpdateJob(&cmd.Job)
}

func (s *JobService) DeleteJob(ctx context.Context, id string) error {
	return s.db.DeleteJob(id)
}

func (s *JobService) RunJob(ctx context.Context, cmd RunJobCommand) (*domain.Execution, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	return s.runner.StartExecution(ctx, cmd.JobID, "run", cmd.Version, cmd.ContextName, cmd.Params, cmd.TriggeredBy)
}

func (s *JobService) DeployJob(ctx context.Context, cmd DeployJobCommand) (*domain.Execution, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}

	job, err := s.db.GetJob(cmd.JobID)
	if err != nil {
		return nil, err
	}

	targetVersion := cmd.Version
	if targetVersion == "" {
		targetVersion = job.ActiveVersion
	}

	if !cmd.Force {
		if job.IsDeployed && job.DeployedVersion != "" && job.DeployedVersion != targetVersion {
			return nil, fmt.Errorf("another version '%s' of artifact '%s' is already deployed on server. Use force=true to proceed", job.DeployedVersion, job.ArtifactID)
		}

		serverJobs, err := s.db.GetJobsByArtifactOnServer(job.ArtifactID, job.ServerID)
		if err == nil {
			for _, sj := range serverJobs {
				if sj.ID != job.ID && sj.IsDeployed && sj.DeployedVersion != "" && sj.DeployedVersion != targetVersion {
					return nil, fmt.Errorf("artifact '%s' is already deployed on this server by job '%s' with version '%s'. Use force=true to proceed", job.ArtifactID, sj.Name, sj.DeployedVersion)
				}
			}
		}
	}

	return s.runner.StartExecution(ctx, cmd.JobID, "deploy", targetVersion, "", nil, cmd.TriggeredBy)
}

func (s *JobService) UndeployJob(ctx context.Context, cmd UndeployJobCommand) (*domain.Execution, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	return s.runner.StartExecution(ctx, cmd.JobID, "undeploy", "", "", nil, cmd.TriggeredBy)
}

// Bulk Actions

func (s *JobService) BulkRun(ctx context.Context, cmd BulkRunCommand) ([]*domain.Execution, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	return s.runner.StartBulkRunQueue(ctx, cmd.JobIDs, cmd.Concurrency, cmd.TriggeredBy)
}

func (s *JobService) BulkDeploy(ctx context.Context, cmd BulkDeployCommand) ([]*domain.Execution, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	var execs []*domain.Execution
	var errs []error
	for _, id := range cmd.JobIDs {
		exec, err := s.DeployJob(ctx, DeployJobCommand{
			JobID:       id,
			TriggeredBy: cmd.TriggeredBy,
			Force:       cmd.Force,
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("job %s: %w", id, err))
		} else {
			execs = append(execs, exec)
		}
	}
	if len(errs) > 0 && len(execs) == 0 {
		return nil, fmt.Errorf("all deployments failed: %v", errs)
	}
	return execs, nil
}

func (s *JobService) BulkUndeploy(ctx context.Context, cmd BulkUndeployCommand) ([]*domain.Execution, error) {
	if err := cmd.Validate(); err != nil {
		return nil, err
	}
	var execs []*domain.Execution
	var errs []error
	for _, id := range cmd.JobIDs {
		exec, err := s.UndeployJob(ctx, UndeployJobCommand{
			JobID:       id,
			TriggeredBy: cmd.TriggeredBy,
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("job %s: %w", id, err))
		} else {
			execs = append(execs, exec)
		}
	}
	if len(errs) > 0 && len(execs) == 0 {
		return nil, fmt.Errorf("all undeployments failed: %v", errs)
	}
	return execs, nil
}
