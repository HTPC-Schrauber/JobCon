package services

import (
	"errors"
	"jobcon/internal/domain"
	"strings"
)

// Job DTOs

type CreateJobCommand struct {
	domain.Job
	Force bool `json:"force"`
}

func (c *CreateJobCommand) Validate() error {
	if c.Name == "" {
		return errors.New("name is required")
	}
	if err := domain.ValidateIdentifier(c.ArtifactID, "ArtifactID"); err != nil {
		return err
	}
	if err := domain.ValidateIdentifier(c.GroupID, "GroupID"); err != nil {
		return err
	}
	if err := domain.ValidateIdentifier(c.ActiveVersion, "ActiveVersion"); err != nil {
		return err
	}
	if err := domain.ValidateIdentifier(c.DefaultContext, "DefaultContext"); err != nil {
		return err
	}
	return nil
}

type UpdateJobCommand struct {
	ID string
	CreateJobCommand
}

type RunJobCommand struct {
	JobID       string
	Version     string            `json:"version"`
	ContextName string            `json:"context"`
	Params      map[string]string `json:"params"`
	TriggeredBy string
}

func (c *RunJobCommand) Validate() error {
	if err := domain.ValidateIdentifier(c.JobID, "JobID"); err != nil {
		return err
	}
	if err := domain.ValidateIdentifier(c.Version, "Version"); err != nil {
		return err
	}
	if err := domain.ValidateIdentifier(c.ContextName, "ContextName"); err != nil {
		return err
	}
	for k := range c.Params {
		if err := domain.ValidateIdentifier(k, "Parameter Key"); err != nil {
			return err
		}
	}
	return nil
}

type DeployJobCommand struct {
	JobID       string
	Version     string `json:"version"`
	TriggeredBy string
	Force       bool `json:"force"`
}

func (c *DeployJobCommand) Validate() error {
	if err := domain.ValidateIdentifier(c.JobID, "JobID"); err != nil {
		return err
	}
	if err := domain.ValidateIdentifier(c.Version, "Version"); err != nil {
		return err
	}
	return nil
}

type UndeployJobCommand struct {
	JobID       string
	TriggeredBy string
}

func (c *UndeployJobCommand) Validate() error {
	return domain.ValidateIdentifier(c.JobID, "JobID")
}

// Bulk Commands

type BulkRunCommand struct {
	JobIDs      []string `json:"job_ids"`
	Concurrency int      `json:"concurrency,omitempty"`
	TriggeredBy string
}

func (c *BulkRunCommand) Validate() error {
	for _, id := range c.JobIDs {
		if err := domain.ValidateIdentifier(id, "JobID"); err != nil {
			return err
		}
	}
	return nil
}

type BulkDeployCommand struct {
	JobIDs      []string `json:"job_ids"`
	TriggeredBy string
	Force       bool `json:"force"`
}

func (c *BulkDeployCommand) Validate() error {
	for _, id := range c.JobIDs {
		if err := domain.ValidateIdentifier(id, "JobID"); err != nil {
			return err
		}
	}
	return nil
}

type BulkUndeployCommand struct {
	JobIDs      []string `json:"job_ids"`
	TriggeredBy string
	Force       bool `json:"force"`
}

func (c *BulkUndeployCommand) Validate() error {
	for _, id := range c.JobIDs {
		if err := domain.ValidateIdentifier(id, "JobID"); err != nil {
			return err
		}
	}
	return nil
}

// Server DTOs

type CreateServerCommand struct {
	domain.Server
}

func (c *CreateServerCommand) Validate() error {
	if c.Name == "" || c.Host == "" {
		return errors.New("name and host are required")
	}
	return nil
}

type UpdateServerCommand struct {
	ID string
	CreateServerCommand
}

// User DTOs

type CreateUserCommand struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	Role        string `json:"role"`
	AuthSource  string `json:"auth_source"`
	IsActive    bool   `json:"is_active"`
}

func (c *CreateUserCommand) Validate() error {
	if strings.TrimSpace(c.Username) == "" {
		return errors.New("username is required")
	}
	return nil
}

type UpdateUserCommand struct {
	ID string
	CreateUserCommand
}

func (c *UpdateUserCommand) Validate() error {
	// Username is not updated, so we don't validate it here.
	return nil
}

type UpdatePasswordCommand struct {
	UserID   string
	Password string
}

func (c *UpdatePasswordCommand) Validate() error {
	if c.Password == "" {
		return errors.New("password is required")
	}
	return nil
}
