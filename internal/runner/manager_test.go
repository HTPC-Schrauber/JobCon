package runner

import (
	"jobcon/internal/config"
	"jobcon/internal/db"
	"jobcon/internal/storage"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildNexusURL(t *testing.T) {
	tmpDir := t.TempDir()
	database, err := db.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	defer database.Close()

	tests := []struct {
		name        string
		dbSetting   string
		cfgBaseURL  string
		job         *db.Job
		version     string
		expectedURL string
	}{
		{
			name:       "standard base URL without /repository",
			dbSetting:  "http://omv.localdomain:8081",
			cfgBaseURL: "http://fallback:8081",
			job: &db.Job{
				GroupID:    "com.opitzhome.jobs",
				ArtifactID: "crm_lead_dispatcher",
				NexusRepo:  "talend-releases",
			},
			version:     "1.0.0",
			expectedURL: "http://omv.localdomain:8081/repository/talend-releases/com/opitzhome/jobs/crm_lead_dispatcher/1.0.0/crm_lead_dispatcher-1.0.0.zip",
		},
		{
			name:       "base URL with /repository already present",
			dbSetting:  "http://omv.localdomain:8081/repository",
			cfgBaseURL: "http://fallback:8081",
			job: &db.Job{
				GroupID:    "com.opitzhome.jobs",
				ArtifactID: "crm_lead_dispatcher",
				NexusRepo:  "talend-releases",
			},
			version:     "1.0.0",
			expectedURL: "http://omv.localdomain:8081/repository/talend-releases/com/opitzhome/jobs/crm_lead_dispatcher/1.0.0/crm_lead_dispatcher-1.0.0.zip",
		},
		{
			name:       "base URL with trailing slash and /repository/",
			dbSetting:  "http://omv.localdomain:8081/repository/",
			cfgBaseURL: "http://fallback:8081",
			job: &db.Job{
				GroupID:    "com.opitzhome.jobs",
				ArtifactID: "crm_lead_dispatcher",
				NexusRepo:  "talend-releases",
			},
			version:     "1.0.0",
			expectedURL: "http://omv.localdomain:8081/repository/talend-releases/com/opitzhome/jobs/crm_lead_dispatcher/1.0.0/crm_lead_dispatcher-1.0.0.zip",
		},
		{
			name:       "fallback to releases when NexusRepo is empty",
			dbSetting:  "http://nexus.intern:8081",
			cfgBaseURL: "http://fallback:8081",
			job: &db.Job{
				GroupID:    "de.firma.talend",
				ArtifactID: "sync_sap",
				NexusRepo:  "",
			},
			version:     "2.1.0",
			expectedURL: "http://nexus.intern:8081/repository/releases/de/firma/talend/sync_sap/2.1.0/sync_sap-2.1.0.zip",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.dbSetting != "" {
				_ = database.SetSetting("nexus_base_url", tt.dbSetting)
			}
			m := NewExecutionManager(database, nil, nil, &config.NexusConfig{
				BaseURL: tt.cfgBaseURL,
			})
			got := m.BuildNexusURL(tt.job, tt.version)
			if got != tt.expectedURL {
				t.Errorf("BuildNexusURL() = %q, expected %q", got, tt.expectedURL)
			}
		})
	}
}

func TestStartBulkRunQueue(t *testing.T) {
	tmpDir := t.TempDir()
	database, err := db.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	defer database.Close()

	_ = database.CreateServer(&db.Server{ID: "srv-1", Name: "Server 1", Host: "127.0.0.1", SSHKeyPath: "/k", Status: "online"})
	_ = database.CreateJob(&db.Job{ID: "job-1", Name: "Job 1", ServerID: "srv-1", GroupID: "g", ArtifactID: "a1", ActiveVersion: "1.0"})
	_ = database.CreateJob(&db.Job{ID: "job-2", Name: "Job 2", ServerID: "srv-1", GroupID: "g", ArtifactID: "a2", ActiveVersion: "1.0"})
	_ = database.CreateJob(&db.Job{ID: "job-3", Name: "Job 3", ServerID: "srv-1", GroupID: "g", ArtifactID: "a3", ActiveVersion: "1.0"})

	m := NewExecutionManager(database, nil, nil, &config.NexusConfig{})

	execs, err := m.StartBulkRunQueue(nil, []string{"job-1", "job-2", "job-3"}, 2, "test-user")
	if err != nil {
		t.Fatalf("StartBulkRunQueue failed: %v", err)
	}
	if len(execs) != 3 {
		t.Fatalf("expected 3 executions, got %d", len(execs))
	}
	for _, e := range execs {
		if e.Status != "pending" && e.Status != "running" && e.Status != "failed" {
			t.Errorf("unexpected execution status: %s", e.Status)
		}
	}

	// Test default concurrency (concurrency <= 0)
	execsDefault, err := m.StartBulkRunQueue(nil, []string{"job-1"}, 0, "test-user")
	if err != nil {
		t.Fatalf("StartBulkRunQueue with default concurrency failed: %v", err)
	}
	if len(execsDefault) != 1 {
		t.Fatalf("expected 1 execution, got %d", len(execsDefault))
	}

	// Test exceeding MaxBulkConcurrency
	_, err = m.StartBulkRunQueue(nil, []string{"job-1"}, MaxBulkConcurrency+1, "test-user")
	if err == nil {
		t.Errorf("expected error when exceeding MaxBulkConcurrency, got nil")
	}
}

func TestExecuteJobCommand_FailedSSHKeyWritesErrorToLog(t *testing.T) {
	tmpDir := t.TempDir()
	database, err := db.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	defer database.Close()

	logsDir := filepath.Join(tmpDir, "logs")
	logStore, err := storage.NewLogStorage(logsDir, true)
	if err != nil {
		t.Fatalf("failed to create log storage: %v", err)
	}

	server := &db.Server{
		ID:         "srv-missing-key",
		Name:       "Test Missing Key Server",
		Host:       "127.0.0.1",
		Port:       22,
		User:       "talend",
		SSHKeyPath: "/path/to/missing/test.key",
		Status:     "online",
	}
	_ = database.CreateServer(server)

	job := &db.Job{
		ID:            "job-deploy-test",
		Name:          "Inventory Test Job",
		ServerID:      server.ID,
		GroupID:       "com.example",
		ArtifactID:    "inventory_update",
		ActiveVersion: "1.0.0",
	}
	_ = database.CreateJob(job)

	sshRunner := NewSSHRunner("", 5, 5)
	manager := NewExecutionManager(database, logStore, sshRunner, &config.NexusConfig{})

	execution := &db.Execution{
		ID:          "exec_test123",
		JobID:       job.ID,
		JobName:     job.Name,
		Action:      "deploy",
		Status:      "running",
		Version:     "1.0.0",
		Context:     "Default",
		TriggeredBy: "admin",
	}
	_ = database.CreateExecution(execution)

	// Execute synchronously
	manager.executeJobCommand(server, job, execution, "1.0.0", "deploy", "Default", nil)

	// Fetch updated execution from DB
	updatedExec, err := database.GetExecution(execution.ID)
	if err != nil {
		t.Fatalf("failed to get execution: %v", err)
	}
	if updatedExec.Status != "failed" {
		t.Errorf("expected status 'failed', got %s", updatedExec.Status)
	}
	if updatedExec.ExitCode == nil || *updatedExec.ExitCode != -1 {
		t.Errorf("expected exit code -1, got %v", updatedExec.ExitCode)
	}

	// Read log content
	logBytes, err := logStore.ReadLog(updatedExec.LogPath)
	if err != nil {
		t.Fatalf("failed to read log: %v", err)
	}
	if len(logBytes) == 0 {
		t.Fatal("expected log to NOT be empty, but got 0 bytes")
	}

	logContent := string(logBytes)
	if !strings.Contains(logContent, "[JobCon] Execution ID:  exec_test123") {
		t.Errorf("expected execution ID in log, got:\n%s", logContent)
	}
	if !strings.Contains(logContent, "Action:        DEPLOY") {
		t.Errorf("expected Action in log, got:\n%s", logContent)
	}
	if !strings.Contains(logContent, "SSH private key file not found at \"/path/to/missing/test.key\"") {
		t.Errorf("expected key error in log, got:\n%s", logContent)
	}
	if !strings.Contains(logContent, "[JobCon] Finished:      FAILED") {
		t.Errorf("expected finished status in log, got:\n%s", logContent)
	}
}
