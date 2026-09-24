package runner

import (
	"context"
	"errors"
	"fmt"
	"jobcon/internal/config"
	"jobcon/internal/db"
	"jobcon/internal/storage"
	"log"
	"strconv"
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
	baseURL = strings.TrimSuffix(baseURL, "/repository")

	repo := job.NexusRepo
	if repo == "" {
		repo = "releases"
	}

	// Nexus 3 Maven repository artifacts are hosted at:
	// Format: {base_url}/repository/{repo}/{group_path}/{artifact_id}/{version}/{artifact_id}-{version}.zip
	return fmt.Sprintf("%s/repository/%s/%s/%s/%s/%s-%s.zip",
		baseURL, repo, groupPath, job.ArtifactID, version, job.ArtifactID, version)
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
	if !validIdentifierRegex.MatchString(jobID) {
		return nil, fmt.Errorf("ungültige Job-ID: %q", jobID)
	}

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

	// Direct regex guards for CodeQL static taint tracking sanitizer recognition
	if action != "undeploy" && targetVersion != "" && !validIdentifierRegex.MatchString(targetVersion) {
		return nil, fmt.Errorf("ungültiges Versionsformat: %q", targetVersion)
	}
	if targetContext != "" && !validIdentifierRegex.MatchString(targetContext) {
		return nil, fmt.Errorf("ungültiger Kontextname: %q", targetContext)
	}
	if !validIdentifierRegex.MatchString(job.ArtifactID) {
		return nil, fmt.Errorf("ungültige Artifact-ID: %q", job.ArtifactID)
	}
	for k, v := range params {
		if !validIdentifierRegex.MatchString(k) {
			return nil, fmt.Errorf("ungültiger Parameter-Schlüssel: %q", k)
		}
		if err := ValidateParamValue(v); err != nil {
			return nil, err
		}
	}
	envFile := job.EnvFile
	if envFile == "" {
		envFile = server.EnvFile
	}
	if err := ValidateEnvFile(envFile); err != nil {
		return nil, err
	}
	if err := ValidatePath(server.JobsDir); err != nil {
		return nil, err
	}
	if err := ValidatePath(server.ScriptsDir); err != nil {
		return nil, err
	}

	executionID := fmt.Sprintf("exec_%s", uuid.New().String()[:8])

	// Create DB execution record in running state
	execution := &db.Execution{
		ID:          executionID,
		JobID:       jobID,
		JobName:     job.Name,
		Action:      action,
		Status:      "running",
		Version:     targetVersion,
		Context:     targetContext,
		TriggeredBy: triggeredBy,
	}

	if err := m.db.CreateExecution(execution); err != nil {
		return nil, fmt.Errorf("failed to save execution: %w", err)
	}

	go m.executeJobCommand(server, job, execution, targetVersion, action, targetContext, params)

	return execution, nil
}

// StartBulkRunQueue queues and executes a list of jobs with controlled concurrency.
// If concurrency <= 0, the setting 'max_concurrent_jobs' is used (default 2).
func (m *ExecutionManager) StartBulkRunQueue(
	ctx context.Context,
	jobIDs []string,
	concurrency int,
	triggeredBy string,
) ([]*db.Execution, error) {
	if concurrency <= 0 {
		concurrencyStr, _ := m.db.GetSetting("max_concurrent_jobs", "2")
		if c, err := strconv.Atoi(concurrencyStr); err == nil && c > 0 {
			concurrency = c
		} else {
			concurrency = 2
		}
	}

	type queueItem struct {
		job    *db.Job
		server *db.Server
		exec   *db.Execution
	}

	var items []queueItem
	var execs []*db.Execution

	for _, id := range jobIDs {
		job, err := m.db.GetJob(id)
		if err != nil {
			log.Printf("[BulkRunQueue] Skipping job %s: %v", id, err)
			continue
		}
		server, err := m.db.GetServer(job.ServerID)
		if err != nil {
			log.Printf("[BulkRunQueue] Skipping job %s (server %s not found): %v", id, job.ServerID, err)
			continue
		}

		targetVersion := job.ActiveVersion
		targetContext := job.DefaultContext

		if err := ValidateVersion(targetVersion); err != nil {
			log.Printf("[BulkRunQueue] Skipping job %s (invalid version %q): %v", id, targetVersion, err)
			continue
		}
		if err := ValidateContext(targetContext); err != nil {
			log.Printf("[BulkRunQueue] Skipping job %s (invalid context %q): %v", id, targetContext, err)
			continue
		}
		if err := ValidateVersion(job.ArtifactID); err != nil {
			log.Printf("[BulkRunQueue] Skipping job %s (invalid artifact ID %q): %v", id, job.ArtifactID, err)
			continue
		}

		executionID := fmt.Sprintf("exec_%s", uuid.New().String()[:8])
		execution := &db.Execution{
			ID:          executionID,
			JobID:       job.ID,
			JobName:     job.Name,
			Action:      "run",
			Status:      "pending",
			Version:     targetVersion,
			Context:     targetContext,
			TriggeredBy: triggeredBy,
		}

		if err := m.db.CreateExecution(execution); err != nil {
			log.Printf("[BulkRunQueue] Failed to create pending execution for job %s: %v", job.ID, err)
			continue
		}

		items = append(items, queueItem{job: job, server: server, exec: execution})
		execs = append(execs, execution)
	}

	if len(items) == 0 {
		return nil, errors.New("keine gültigen Jobs für den Start gefunden")
	}

	// Launch background worker pool
	go func(queue []queueItem, limit int) {
		sem := make(chan struct{}, limit)
		var wg sync.WaitGroup

		for _, item := range queue {
			sem <- struct{}{}
			wg.Add(1)

			go func(qItem queueItem) {
				defer func() {
					<-sem
					wg.Done()
				}()

				_ = m.db.SetExecutionRunning(qItem.exec.ID)
				m.executeJobCommand(qItem.server, qItem.job, qItem.exec, qItem.exec.Version, "run", qItem.exec.Context, nil)
			}(item)
		}

		wg.Wait()
		log.Printf("[BulkRunQueue] Finished batch execution of %d jobs (concurrency limit: %d)", len(queue), limit)
	}(items, concurrency)

	return execs, nil
}

func (m *ExecutionManager) executeJobCommand(
	server *db.Server,
	job *db.Job,
	execution *db.Execution,
	targetVersion string,
	action string,
	targetContext string,
	params map[string]string,
) {
	if m.storage == nil {
		return
	}

	// Direct regex guards for CodeQL taint analysis barrier recognition
	if targetVersion != "" && !validIdentifierRegex.MatchString(targetVersion) {
		log.Printf("[Execution %s] Aborting: invalid targetVersion %q", execution.ID, targetVersion)
		failCode := 1
		failDur := int64(0)
		_ = m.db.UpdateExecutionStatus(execution.ID, "failed", &failCode, &failDur)
		return
	}
	if targetContext != "" && !validIdentifierRegex.MatchString(targetContext) {
		log.Printf("[Execution %s] Aborting: invalid targetContext %q", execution.ID, targetContext)
		failCode := 1
		failDur := int64(0)
		_ = m.db.UpdateExecutionStatus(execution.ID, "failed", &failCode, &failDur)
		return
	}
	if !validIdentifierRegex.MatchString(job.ArtifactID) {
		log.Printf("[Execution %s] Aborting: invalid job.ArtifactID %q", execution.ID, job.ArtifactID)
		failCode := 1
		failDur := int64(0)
		_ = m.db.UpdateExecutionStatus(execution.ID, "failed", &failCode, &failDur)
		return
	}
	for k := range params {
		if !validIdentifierRegex.MatchString(k) {
			log.Printf("[Execution %s] Aborting: invalid param key %q", execution.ID, k)
			failCode := 1
			failDur := int64(0)
			_ = m.db.UpdateExecutionStatus(execution.ID, "failed", &failCode, &failDur)
			return
		}
	}

	logFile, logPath, err := m.storage.CreateLogWriter(execution.ID)
	if err != nil {
		log.Printf("[Execution %s] Failed to create log file: %v", execution.ID, err)
		failCode := 1
		failDur := int64(0)
		_ = m.db.UpdateExecutionStatus(execution.ID, "failed", &failCode, &failDur)
		return
	}

	execution.LogPath = logPath
	_, _ = m.db.Exec(`UPDATE executions SET log_path = ? WHERE id = ?`, logPath, execution.ID)

	broadcaster := NewLogBroadcaster(5000)
	multiWriter := NewMultiWriter(logFile, broadcaster)

	scriptsDir := server.ScriptsDir
	if scriptsDir == "" {
		scriptsDir = "/opt/talend/scripts"
	}
	jobsDir := server.JobsDir
	if jobsDir == "" {
		jobsDir = "/opt/talend/jobs"
	}
	ctlScript := fmt.Sprintf("%s/jobcon_ctl.sh", strings.TrimRight(scriptsDir, "/"))
	keepReleases := server.KeepReleases
	if keepReleases <= 0 {
		keepReleases = 3
	}

	envFile := job.EnvFile
	if envFile == "" {
		envFile = server.EnvFile
	}
	nexusURL := m.BuildNexusURL(job, targetVersion)

	var cmdParts []string
	if action == "deploy" {
		cmdParts = append(cmdParts, ShellQuote(ctlScript), "deploy",
			"--job", ShellQuote(job.ArtifactID),
			"--version", ShellQuote(targetVersion),
			"--nexus-url", ShellQuote(nexusURL),
			"--jobs-dir", ShellQuote(jobsDir),
			"--keep", fmt.Sprintf("%d", keepReleases),
		)
	} else if action == "undeploy" {
		cmdParts = append(cmdParts, ShellQuote(ctlScript), "undeploy",
			"--job", ShellQuote(job.ArtifactID),
			"--jobs-dir", ShellQuote(jobsDir),
		)
	} else {
		cmdParts = append(cmdParts, ShellQuote(ctlScript), "run",
			"--job", ShellQuote(job.ArtifactID),
			"--version", ShellQuote(targetVersion),
			"--nexus-url", ShellQuote(nexusURL),
			"--jobs-dir", ShellQuote(jobsDir),
			"--context", ShellQuote(targetContext),
		)

		if envFile != "" {
			cmdParts = append(cmdParts, "--env-file", ShellQuote(envFile))
		}

		if len(params) > 0 {
			cmdParts = append(cmdParts, "--params")
			for k, v := range params {
				cmdParts = append(cmdParts, "--context_param", ShellQuote(fmt.Sprintf("%s=%s", k, v)))
			}
		}
	}

	nexusUser, nexusPass := m.GetNexusCredentials()
	if nexusUser != "" && nexusPass != "" {
		cmdParts = append(cmdParts,
			"--nexus-user", ShellQuote(nexusUser),
			"--nexus-pass", ShellQuote(nexusPass),
		)
	}

	remoteCommand := strings.Join(cmdParts, " ")
	execCtx, cancel := context.WithCancel(context.Background())

	m.mu.Lock()
	m.active[execution.ID] = &ActiveExecution{
		Execution:   execution,
		Broadcaster: broadcaster,
		Cancel:      cancel,
	}
	m.mu.Unlock()

	defer func() {
		multiWriter.Flush()
		_ = logFile.Close()
		finalPath, _ := m.storage.FinalizeLog(logPath)

		m.mu.Lock()
		delete(m.active, execution.ID)
		m.mu.Unlock()

		time.Sleep(1 * time.Second)
		broadcaster.Close()

		m.storage.CleanRetention(m.db, job.ID, job.RetentionRuns)
		_ = finalPath
	}()

	startTime := time.Now()

	fmt.Fprintf(multiWriter, "[JobCon] ========================================================\n")
	fmt.Fprintf(multiWriter, "[JobCon] Execution ID:  %s\n", execution.ID)
	fmt.Fprintf(multiWriter, "[JobCon] Action:        %s\n", strings.ToUpper(action))
	fmt.Fprintf(multiWriter, "[JobCon] Job:           %s (%s)\n", job.Name, job.ID)
	fmt.Fprintf(multiWriter, "[JobCon] Target Server: %s (%s:%d)\n", server.Name, server.Host, server.Port)
	if targetVersion != "" {
		fmt.Fprintf(multiWriter, "[JobCon] Version:       %s\n", targetVersion)
	}
	if targetContext != "" {
		fmt.Fprintf(multiWriter, "[JobCon] Context:       %s\n", targetContext)
	}
	fmt.Fprintf(multiWriter, "[JobCon] Triggered By:  %s\n", execution.TriggeredBy)
	fmt.Fprintf(multiWriter, "[JobCon] Timestamp:     %s\n", startTime.Format("2006-01-02 15:04:05 MST"))
	fmt.Fprintf(multiWriter, "[JobCon] ========================================================\n\n")

	exitCode, runErr := m.sshRunner.RunCommand(execCtx, server, remoteCommand, multiWriter)
	duration := time.Since(startTime).Milliseconds()

	status := "success"
	if errors.Is(runErr, context.Canceled) || exitCode == 130 {
		status = "aborted"
	} else if runErr != nil || exitCode != 0 {
		status = "failed"
	}

	_ = m.db.UpdateExecutionStatus(execution.ID, status, &exitCode, &duration)

	if action == "deploy" && status == "success" {
		_ = m.db.UpdateJobActiveVersion(job.ID, targetVersion)
		_ = m.db.SetJobDeployed(job.ID, true, targetVersion)
	} else if action == "run" && (status == "success" || (runErr == nil && exitCode != 10 && exitCode != 11 && exitCode != 12 && exitCode != 20 && exitCode != 21)) {
		if targetVersion != "" {
			_ = m.db.UpdateJobActiveVersion(job.ID, targetVersion)
		}
		_ = m.db.SetJobDeployed(job.ID, true, targetVersion)
	} else if action == "undeploy" && status == "success" {
		_ = m.db.SetJobDeployed(job.ID, false, "")
	}

	fmt.Fprintf(multiWriter, "\n[JobCon] ========================================================\n")
	if runErr != nil {
		fmt.Fprintf(multiWriter, "[JobCon] Finished:      %s\n", strings.ToUpper(status))
		fmt.Fprintf(multiWriter, "[JobCon] Exit Code:     %d\n", exitCode)
		fmt.Fprintf(multiWriter, "[JobCon] Duration:      %d ms\n", duration)
		fmt.Fprintf(multiWriter, "[JobCon] Error Details: %v\n", runErr)
		log.Printf("[Execution %s] Finished with status %s (exit code %d, duration %dms): %v",
			execution.ID, status, exitCode, duration, runErr)
	} else {
		fmt.Fprintf(multiWriter, "[JobCon] Finished:      %s\n", strings.ToUpper(status))
		fmt.Fprintf(multiWriter, "[JobCon] Exit Code:     %d\n", exitCode)
		fmt.Fprintf(multiWriter, "[JobCon] Duration:      %d ms\n", duration)
		log.Printf("[Execution %s] Finished with status %s (exit code %d, duration %dms)",
			execution.ID, status, exitCode, duration)
	}
	fmt.Fprintf(multiWriter, "[JobCon] ========================================================\n")
}

// UndeployJobSync executes undeploy synchronously on the remote execution server
func (m *ExecutionManager) UndeployJobSync(ctx context.Context, jobID string) error {
	job, err := m.db.GetJob(jobID)
	if err != nil {
		return err
	}
	server, err := m.db.GetServer(job.ServerID)
	if err != nil {
		return err
	}
	scriptsDir := server.ScriptsDir
	if scriptsDir == "" {
		scriptsDir = "/opt/talend/scripts"
	}
	jobsDir := server.JobsDir
	if jobsDir == "" {
		jobsDir = "/opt/talend/jobs"
	}
	if err := ValidateVersion(job.ArtifactID); err != nil {
		return fmt.Errorf("ungültige Artifact-ID: %w", err)
	}
	if err := ValidatePath(jobsDir); err != nil {
		return err
	}
	if err := ValidatePath(scriptsDir); err != nil {
		return err
	}
	ctlScript := fmt.Sprintf("%s/jobcon_ctl.sh", strings.TrimRight(scriptsDir, "/"))
	remoteCommand := fmt.Sprintf("%s undeploy --job %s --jobs-dir %s", ShellQuote(ctlScript), ShellQuote(job.ArtifactID), ShellQuote(jobsDir))
	_, err = m.sshRunner.RunCommand(ctx, server, remoteCommand, nil)
	if err == nil {
		_ = m.db.SetJobDeployed(jobID, false, "")
	}
	return err
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
