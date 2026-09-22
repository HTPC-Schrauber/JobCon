package runner

import (
	"jobcon/internal/config"
	"jobcon/internal/db"
	"path/filepath"
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
}
