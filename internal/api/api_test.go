package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"jobcon/internal/auth"
	"jobcon/internal/config"
	"jobcon/internal/db"
	"jobcon/internal/runner"
	"jobcon/internal/storage"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func setupTestAPI(t *testing.T) (*API, *http.ServeMux, *db.DB, string) {
	tmpDir := t.TempDir()
	database, err := db.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}

	logStore, err := storage.NewLogStorage(filepath.Join(tmpDir, "logs"), true)
	if err != nil {
		t.Fatalf("failed to init log storage: %v", err)
	}

	sshRunner := runner.NewSSHRunner("/tmp/key", 5, 5)
	execManager := runner.NewExecutionManager(database, logStore, sshRunner, &config.NexusConfig{
		BaseURL: "https://nexus.test",
	})

	localAuth := auth.NewLocalAuthenticator(database)
	sessions := auth.NewSessionManager(1 * time.Hour)
	authMW := auth.NewMiddleware(localAuth, database, sessions)

	// Create test token
	rawToken := "secret-test-token"
	h := sha256.Sum256([]byte("jobcon_" + rawToken))
	tokenHash := hex.EncodeToString(h[:])
	_ = database.CreateAPIToken(&db.APIToken{
		ID:        "token-01",
		Name:      "test-token",
		TokenHash: tokenHash,
		Role:      auth.RoleAdmin,
	})

	cfg := config.DefaultConfig()
	api := NewAPI(database, logStore, execManager, sshRunner, authMW, cfg)
	mux := http.NewServeMux()
	api.RegisterRoutes(mux)

	return api, mux, database, "jobcon_" + rawToken
}

func TestAPIHealthAndJobs(t *testing.T) {
	_, mux, database, token := setupTestAPI(t)
	defer database.Close()

	// 1. Healthz (no auth)
	req := httptest.NewRequest("GET", "/healthz", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for healthz, got %d", rec.Code)
	}

	// Create a prerequisite server
	_ = database.CreateServer(&db.Server{
		ID:         "srv-01",
		Name:       "Production 01",
		Host:       "192.168.1.50",
		Port:       22,
		User:       "talend",
		SSHKeyPath: "/tmp/key",
	})

	// 2. Create Job via POST /api/v1/jobs (authenticated with Bearer token)
	jobBody := `{"id":"sync_sap","name":"SAP Customer Sync","server_id":"srv-01","group_id":"de.firma.talend","artifact_id":"sync_sap","active_version":"1.0.0","nexus_repo":"releases"}`
	req = httptest.NewRequest("POST", "/api/v1/jobs", bytes.NewBufferString(jobBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for POST /jobs, got %d: %s", rec.Code, rec.Body.String())
	}

	// 3. List Jobs via GET /api/v1/jobs
	req = httptest.NewRequest("GET", "/api/v1/jobs", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for GET /jobs, got %d", rec.Code)
	}

	var jobs []db.Job
	if err := json.Unmarshal(rec.Body.Bytes(), &jobs); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(jobs) != 1 || jobs[0].ID != "sync_sap" {
		t.Errorf("expected 1 job with id sync_sap, got %v", jobs)
	}

	// 4. Artifact URL
	req = httptest.NewRequest("GET", "/api/v1/jobs/sync_sap/artifact", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for artifact endpoint, got %d", rec.Code)
	}
	var artifactRes map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &artifactRes)
	expectedURL := "https://nexus.test/repository/releases/de/firma/talend/sync_sap/1.0.0/sync_sap-1.0.0.zip"
	if artifactRes["download_url"] != expectedURL {
		t.Errorf("expected download_url %q, got %q", expectedURL, artifactRes["download_url"])
	}
}

func TestAPIServerUsageAndDelete(t *testing.T) {
	_, mux, database, token := setupTestAPI(t)
	defer database.Close()

	// 1. Create two servers
	s1 := &db.Server{
		ID:         "srv-api-1",
		Name:       "Node 1",
		Host:       "192.168.1.10",
		Port:       22,
		User:       "talend",
		SSHKeyPath: "/tmp/key1",
		JobsDir:    "/opt/talend/jobs",
		ScriptsDir: "/opt/talend/scripts",
	}
	s2 := &db.Server{
		ID:         "srv-api-2",
		Name:       "Node 2",
		Host:       "192.168.1.20",
		Port:       22,
		User:       "talend",
		SSHKeyPath: "/tmp/key2",
	}
	_ = database.CreateServer(s1)
	_ = database.CreateServer(s2)

	// Create job on s1
	j := &db.Job{
		ID:            "job-api-1",
		Name:          "Job API 1",
		ServerID:      "srv-api-1",
		GroupID:       "com.example",
		ArtifactID:    "job_api",
		ActiveVersion: "1.0.0",
		NexusRepo:     "releases",
	}
	_ = database.CreateJob(j)

	// 2. GET /api/v1/servers/srv-api-1/usage
	req := httptest.NewRequest("GET", "/api/v1/servers/srv-api-1/usage", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for server usage, got %d", rec.Code)
	}
	var usageRes struct {
		JobCount     int         `json:"job_count"`
		Jobs         []db.Job    `json:"jobs"`
		OtherServers []db.Server `json:"other_servers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &usageRes); err != nil {
		t.Fatalf("failed to decode usage response: %v", err)
	}
	if usageRes.JobCount != 1 || len(usageRes.Jobs) != 1 || usageRes.Jobs[0].ID != "job-api-1" {
		t.Errorf("unexpected usage jobs: %+v", usageRes)
	}
	if len(usageRes.OtherServers) != 1 || usageRes.OtherServers[0].ID != "srv-api-2" {
		t.Errorf("unexpected other servers: %+v", usageRes.OtherServers)
	}

	// 3. DELETE /api/v1/servers/srv-api-1 without target_server_id -> 409 Conflict
	req = httptest.NewRequest("DELETE", "/api/v1/servers/srv-api-1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict, got %d: %s", rec.Code, rec.Body.String())
	}

	// 4. DELETE /api/v1/servers/srv-api-1 with target_server_id=srv-api-2 -> 200 OK
	req = httptest.NewRequest("DELETE", "/api/v1/servers/srv-api-1?target_server_id=srv-api-2", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for delete with reassignment, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify job is now on srv-api-2
	updatedJob, _ := database.GetJob("job-api-1")
	if updatedJob.ServerID != "srv-api-2" {
		t.Errorf("expected job on srv-api-2, got %s", updatedJob.ServerID)
	}
}
