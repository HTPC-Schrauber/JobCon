package runner

import (
	"context"
	"errors"
	"fmt"
	"jobcon/internal/config"
	"jobcon/internal/db"
	"jobcon/internal/storage"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

var (
	ErrJobAlreadyRunning = errors.New("job is already running and concurrency is disabled")
	ErrExecutionNotFound = errors.New("execution not found")
)

type ActiveExecution struct {
	Execution   *db.Execution
	Broadcaster *LogBroadcaster
	Cancel      context.CancelFunc
}

type ExecutionManager struct {
	db        *db.DB
	storage   *storage.LogStorage
	sshRunner *SSHRunner
	nexusCfg  *config.NexusConfig

	mu     sync.RWMutex
	active map[string]*ActiveExecution
}

func NewExecutionManager(database *db.DB, storage *storage.LogStorage, sshRunner *SSHRunner, nexusCfg *config.NexusConfig) *ExecutionManager {
	return &ExecutionManager{
		db:        database,
		storage:   storage,
		sshRunner: sshRunner,
		nexusCfg:  nexusCfg,
		active:    make(map[string]*ActiveExecution),
	}
}

// BuildNexusURL constructs the standard Maven repository download URL for Talend job ZIPs
func (m *ExecutionManager) BuildNexusURL(job *db.Job, version string) string {
	groupPath := strings.ReplaceAll(job.GroupID, ".", "/")
	baseURL, _ := m.db.GetSetting("nexus_base_url", m.nexusCfg.BaseURL)
	baseURL = strings.TrimRight(baseURL, "/")
	// Format: {base_url}/{repo}/{group_path}/{artifact_id}/{version}/{artifact_id}-{version}.zip
	return fmt.Sprintf("%s/%s/%s/%s/%s/%s-%s.zip",
		baseURL, job.NexusRepo, groupPath, job.ArtifactID, version, job.ArtifactID, version)
}

// GetNexusCredentials returns username and password from DB settings or config fallback
func (m *ExecutionManager) GetNexusCredentials() (string, string) {
	user, _ := m.db.GetSetting("nexus_username", m.nexusCfg.Username)
	pass, _ := m.db.GetSetting("nexus_password", m.nexusCfg.Password)
	return user, pass
}

// StartExecution initiates a deploy or run action asynchronously, returning the created Execution record
func (m *ExecutionManager) StartExecution(
	ctx context.Context,
	jobID string,
	action string, // "run" or "deploy"
	version string,
	contextName string,
	params map[string]string,
	triggeredBy string,
) (*db.Execution, error) {
	job, err := m.db.GetJob(jobID)
	if err != nil {
		return nil, fmt.Errorf("job not found: %w", err)
	}

	server, err := m.db.GetServer(job.ServerID)
	if err != nil {
		return nil, fmt.Errorf("target execution server %s not found: %w", job.ServerID, err)
	}

	// Concurrency check
	if !job.AllowConcurrent {
		activeCount, err := m.db.CountActiveExecutionsForJob(jobID)
		if err != nil {
			return nil, fmt.Errorf("failed to check active executions: %w", err)
		}
		if activeCount > 0 {
			return nil, ErrJobAlreadyRunning
		}
	}

	// Determine version & context
	targetVersion := version
	if targetVersion == "" {
		targetVersion = job.ActiveVersion
	}
	targetContext := contextName
	if targetContext == "" {
		targetContext = job.DefaultContext
	}

	executionID := fmt.Sprintf("exec_%s", uuid.New().String()[:8])
	nexusURL := m.BuildNexusURL(job, targetVersion)

	// Create log writer
	logFile, logPath, err := m.storage.CreateLogWriter(executionID)
	if err != nil {
		return nil, fmt.Errorf("failed to create log file: %w", err)
	}

	broadcaster := NewLogBroadcaster(5000)
	multiWriter := NewMultiWriter(logFile, broadcaster)

	// Create DB execution record
	execution := &db.Execution{
		ID:          executionID,
		JobID:       jobID,
		JobName:     job.Name,
		Action:      action,
		Status:      "running",
		Version:     targetVersion,
		Context:     targetContext,
		LogPath:     logPath,
		TriggeredBy: triggeredBy,
	}

	if err := m.db.CreateExecution(execution); err != nil {
		_ = logFile.Close()
		return nil, fmt.Errorf("failed to save execution: %w", err)
	}

	// Build CLI command
	var cmdParts []string
	if action == "deploy" {
		cmdParts = append(cmdParts, "/opt/talend/scripts/jobcon_ctl.sh", "deploy",
			"--job", fmt.Sprintf("%q", job.ArtifactID),
			"--version", fmt.Sprintf("%q", targetVersion),
			"--nexus-url", fmt.Sprintf("%q", nexusURL),
			"--keep", fmt.Sprintf("%d", job.RetentionRuns),
		)
	} else {
		// Run action
		cmdParts = append(cmdParts, "/opt/talend/scripts/jobcon_ctl.sh", "run",
			"--job", fmt.Sprintf("%q", job.ArtifactID),
			"--version", fmt.Sprintf("%q", targetVersion),
			"--nexus-url", fmt.Sprintf("%q", nexusURL),
			"--context", fmt.Sprintf("%q", targetContext),
		)

		if len(params) > 0 {
			cmdParts = append(cmdParts, "--params")
			for k, v := range params {
				cmdParts = append(cmdParts, fmt.Sprintf("--context_param %s=%q", k, v))
			}
		}
	}

	nexusUser, nexusPass := m.GetNexusCredentials()
	if nexusUser != "" && nexusPass != "" {
		cmdParts = append(cmdParts,
			"--nexus-user", fmt.Sprintf("%q", nexusUser),
			"--nexus-pass", fmt.Sprintf("%q", nexusPass),
		)
	}

	remoteCommand := strings.Join(cmdParts, " ")

	// Context for execution lifecycle
	execCtx, cancel := context.WithCancel(context.Background())

	m.mu.Lock()
	m.active[executionID] = &ActiveExecution{
		Execution:   execution,
		Broadcaster: broadcaster,
		Cancel:      cancel,
	}
	m.mu.Unlock()

	// Launch background runner
	go func() {
		defer func() {
			multiWriter.Flush()
			_ = logFile.Close()
			finalPath, _ := m.storage.FinalizeLog(logPath)

			m.mu.Lock()
			delete(m.active, executionID)
			m.mu.Unlock()

			// Delay closing broadcaster slightly so in-flight SSE streams read final lines
			time.Sleep(1 * time.Second)
			broadcaster.Close()

			// Clean retention
			m.storage.CleanRetention(m.db, jobID, job.RetentionRuns)
			_ = finalPath
		}()

		startTime := time.Now()
		exitCode, runErr := m.sshRunner.RunCommand(execCtx, server, remoteCommand, multiWriter)
		duration := time.Since(startTime).Milliseconds()

		status := "success"
		if errors.Is(runErr, context.Canceled) || exitCode == 130 {
			status = "aborted"
		} else if runErr != nil || exitCode != 0 {
			status = "failed"
		}

		_ = m.db.UpdateExecutionStatus(executionID, status, &exitCode, &duration)

		// If this was a successful deploy, set active_version on the job
		if action == "deploy" && status == "success" {
			_ = m.db.UpdateJobActiveVersion(jobID, targetVersion)
		}

		log.Printf("[Execution %s] Finished with status %s (exit code %d, duration %dms)",
			executionID, status, exitCode, duration)
	}()

	return execution, nil
}

// AbortExecution cancels a running execution
func (m *ExecutionManager) AbortExecution(executionID string) error {
	m.mu.RLock()
	active, exists := m.active[executionID]
	m.mu.RUnlock()

	if !exists {
		return ErrExecutionNotFound
	}

	active.Cancel()
	return nil
}

// GetBroadcaster returns the active LogBroadcaster for an execution, or nil if not running
func (m *ExecutionManager) GetBroadcaster(executionID string) *LogBroadcaster {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if active, exists := m.active[executionID]; exists {
		return active.Broadcaster
	}
	return nil
}
