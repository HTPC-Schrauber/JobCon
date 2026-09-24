package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"jobcon/internal/auth"
	"jobcon/internal/config"
	"jobcon/internal/db"
	"jobcon/internal/nexus"
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
	syncer := nexus.NewSyncer(database, cfg, func() *nexus.Client { return nexus.NewClient("", "", "") })
	api := NewAPI(database, logStore, execManager, sshRunner, authMW, cfg, syncer)
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

	// 2a. Duplicate artifact creation without force -> 409 Conflict
	dupBody := `{"id":"sync_sap_2","name":"SAP Customer Sync 2","server_id":"srv-01","group_id":"de.firma.talend","artifact_id":"sync_sap","active_version":"1.1.0","nexus_repo":"releases"}`
	req = httptest.NewRequest("POST", "/api/v1/jobs", bytes.NewBufferString(dupBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict for duplicate artifact without force, got %d: %s", rec.Code, rec.Body.String())
	}

	// 2b. Duplicate artifact creation with query force=true -> 201 Created
	req = httptest.NewRequest("POST", "/api/v1/jobs?force=true", bytes.NewBufferString(dupBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for duplicate artifact with force=true, got %d: %s", rec.Code, rec.Body.String())
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
	if len(jobs) != 2 {
		t.Errorf("expected 2 jobs, got %d: %v", len(jobs), jobs)
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

func TestAPIResetServerHostKey(t *testing.T) {
	_, mux, database, token := setupTestAPI(t)
	defer database.Close()

	_ = database.CreateServer(&db.Server{
		ID:         "srv-reset-1",
		Name:       "Reset Host Key Node",
		Host:       "192.168.1.100",
		Port:       22,
		User:       "talend",
		SSHKeyPath: "/tmp/key",
	})
	_ = database.UpdateServerHostKey("srv-reset-1", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGdummy")

	// 1. Reset host key without auth -> 401
	req := httptest.NewRequest("POST", "/api/v1/servers/srv-reset-1/hostkey/reset", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized without token, got %d", rec.Code)
	}

	// 2. Reset host key with token for non-existent server -> 404
	req = httptest.NewRequest("POST", "/api/v1/servers/nonexistent/hostkey/reset", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 Not Found for non-existent server, got %d", rec.Code)
	}

	// 3. Reset host key with token -> 200 OK
	req = httptest.NewRequest("POST", "/api/v1/servers/srv-reset-1/hostkey/reset", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for reset host key, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify in DB that host_key is now empty
	srv, err := database.GetServer("srv-reset-1")
	if err != nil {
		t.Fatalf("failed to get server: %v", err)
	}
	if srv.HostKey != "" {
		t.Errorf("expected empty host_key after reset, got %q", srv.HostKey)
	}
}

func TestAPIDeployConflictAndForce(t *testing.T) {
	_, mux, database, token := setupTestAPI(t)
	defer database.Close()

	_ = database.CreateServer(&db.Server{
		ID:         "srv-dep-1",
		Name:       "Deploy Node",
		Host:       "192.168.1.100",
		Port:       22,
		User:       "talend",
		SSHKeyPath: "/tmp/key",
	})

	j := &db.Job{
		ID:              "job-dep-1",
		Name:            "Deploy Job",
		ServerID:        "srv-dep-1",
		GroupID:         "de.firma",
		ArtifactID:      "art-dep",
		ActiveVersion:   "1.0.0",
		NexusRepo:       "releases",
		AllowConcurrent: true,
		IsDeployed:      true,
		DeployedVersion: "1.0.0",
	}
	_ = database.CreateJob(j)

	// 1. Deploy same version "1.0.0" -> should succeed without force (202 Accepted)
	deployBodySame := `{"version":"1.0.0"}`
	req := httptest.NewRequest("POST", "/api/v1/jobs/job-dep-1/deploy", bytes.NewBufferString(deployBodySame))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted for same version deploy, got %d: %s", rec.Code, rec.Body.String())
	}

	// 2. Deploy different version "2.0.0" without force -> should fail with 409 Conflict
	deployBodyDiff := `{"version":"2.0.0"}`
	req = httptest.NewRequest("POST", "/api/v1/jobs/job-dep-1/deploy", bytes.NewBufferString(deployBodyDiff))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict for different version deploy without force, got %d: %s", rec.Code, rec.Body.String())
	}

	// 3. Deploy different version "2.0.0" with query force=true -> should succeed (202 Accepted)
	req = httptest.NewRequest("POST", "/api/v1/jobs/job-dep-1/deploy?force=true", bytes.NewBufferString(deployBodyDiff))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted for different version deploy with force=true, got %d: %s", rec.Code, rec.Body.String())
	}

	// 4. Deploy different version "2.0.0" with body {"force": true} -> should succeed (202 Accepted)
	deployBodyForce := `{"version":"2.0.0", "force": true}`
	req = httptest.NewRequest("POST", "/api/v1/jobs/job-dep-1/deploy", bytes.NewBufferString(deployBodyForce))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted for different version deploy with json force=true, got %d: %s", rec.Code, rec.Body.String())
	}

	// 5. Another job on the same server with same artifact
	j2 := &db.Job{
		ID:              "job-dep-2",
		Name:            "Deploy Job 2",
		ServerID:        "srv-dep-1",
		GroupID:         "de.firma",
		ArtifactID:      "art-dep",
		ActiveVersion:   "2.0.0",
		NexusRepo:       "releases",
		AllowConcurrent: true,
	}
	_ = database.CreateJob(j2)

	// Deploying j2 with version 3.0.0 while j1 is deployed with 1.0.0 without force -> 409 Conflict
	deployBodyJ2 := `{"version":"3.0.0"}`
	req = httptest.NewRequest("POST", "/api/v1/jobs/job-dep-2/deploy", bytes.NewBufferString(deployBodyJ2))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict for j2 deploy when other job has artifact deployed, got %d: %s", rec.Code, rec.Body.String())
	}

	// Deploying j2 with force=true -> 202 Accepted
	req = httptest.NewRequest("POST", "/api/v1/jobs/job-dep-2/deploy?force=true", bytes.NewBufferString(deployBodyJ2))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted for j2 deploy with force=true, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAPIUsersManagement(t *testing.T) {
	_, mux, database, token := setupTestAPI(t)
	defer database.Close()

	// Create initial local admin
	admin := &db.User{
		ID:           "admin_api_1",
		Username:     "admin_api",
		Role:         "admin",
		AuthSource:   "local",
		IsActive:     true,
		PasswordHash: "fakehash",
		DisplayName:  "API Admin",
	}
	_ = database.CreateUser(admin)

	// 1. Create LDAP user via API
	ldapUserReq := `{"username":"ad_user1","auth_source":"ldap","role":"operator","display_name":"AD User 1","email":"ad1@firma.de"}`
	req := httptest.NewRequest("POST", "/api/v1/users", bytes.NewBufferString(ldapUserReq))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for LDAP user, got %d: %s", rec.Code, rec.Body.String())
	}

	var createdLDAP db.User
	_ = json.NewDecoder(rec.Body).Decode(&createdLDAP)
	if createdLDAP.AuthSource != "ldap" || createdLDAP.Role != "operator" {
		t.Errorf("unexpected created user: %+v", createdLDAP)
	}

	// 2. Attempt to delete last local admin via API -> should return 400 Bad Request
	req = httptest.NewRequest("DELETE", "/api/v1/users/admin_api_1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request when deleting last local admin, got %d: %s", rec.Code, rec.Body.String())
	}

	// 3. Attempt to demote last local admin via API -> should return 400 Bad Request
	demoteReq := `{"role":"viewer","is_active":true}`
	req = httptest.NewRequest("PUT", "/api/v1/users/admin_api_1", bytes.NewBufferString(demoteReq))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request when demoting last local admin, got %d: %s", rec.Code, rec.Body.String())
	}

	// 4. Delete LDAP user via API -> should succeed (200 OK)
	req = httptest.NewRequest("DELETE", "/api/v1/users/"+createdLDAP.ID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 OK when deleting LDAP user, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAPIValidationRejectsMaliciousInputs(t *testing.T) {
	_, mux, database, token := setupTestAPI(t)
	defer database.Close()

	server := &db.Server{ID: "srv_val", Name: "Server", Host: "127.0.0.1", SSHKeyPath: "/k", Status: "online"}
	_ = database.CreateServer(server)
	job := &db.Job{ID: "job_val", Name: "Test Job", ServerID: server.ID, GroupID: "g", ArtifactID: "a", ActiveVersion: "1.0.0"}
	_ = database.CreateJob(job)

	// Deploy with malicious version
	deployReq := `{"version":"1.0.0; reboot"}`
	req := httptest.NewRequest("POST", "/api/v1/jobs/job_val/deploy", bytes.NewBufferString(deployReq))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for malicious deploy version, got %d", rec.Code)
	}

	// Run with malicious context
	runReq := `{"version":"1.0.0","context":"$(whoami)"}`
	req = httptest.NewRequest("POST", "/api/v1/jobs/job_val/run", bytes.NewBufferString(runReq))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for malicious run context, got %d", rec.Code)
	}
}

func TestAPITestNexusSecurity(t *testing.T) {
	_, mux, database, token := setupTestAPI(t)
	defer database.Close()

	// 1. Invalid scheme (ftp://)
	body := `{"base_url":"ftp://evil.com/nexus"}`
	req := httptest.NewRequest("POST", "/api/v1/nexus/test", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}
	var res map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if res["success"] == true {
		t.Errorf("expected success: false for ftp:// base_url, got %v", res)
	}

	// 2. Cloud metadata IP (169.254.169.254)
	body = `{"base_url":"http://169.254.169.254/latest/meta-data"}`
	req = httptest.NewRequest("POST", "/api/v1/nexus/test", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	res = nil
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if res["success"] == true {
		t.Errorf("expected success: false for cloud metadata IP, got %v", res)
	}

	// 3. Valid mock server
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/service/rest/v1/repositories" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("[]"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockServer.Close()

	body = `{"base_url":"` + mockServer.URL + `"}`
	req = httptest.NewRequest("POST", "/api/v1/nexus/test", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	res = nil
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if res["success"] != true {
		t.Errorf("expected success: true for mock nexus server, got %v", res)
	}
}

func TestAPIBulkRunConcurrencyLimits(t *testing.T) {
	_, mux, database, token := setupTestAPI(t)
	defer database.Close()

	server := &db.Server{ID: "srv_bulk", Name: "Server", Host: "127.0.0.1", SSHKeyPath: "/k", Status: "online"}
	_ = database.CreateServer(server)
	job := &db.Job{ID: "job_bulk", Name: "Test Job", ServerID: server.ID, GroupID: "g", ArtifactID: "a", ActiveVersion: "1.0.0"}
	_ = database.CreateJob(job)

	// Concurrency > 100 should return 400 Bad Request
	body := `{"job_ids":["job_bulk"],"concurrency":101}`
	req := httptest.NewRequest("POST", "/api/v1/jobs/bulk/run", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request for concurrency > 100, got %d: %s", rec.Code, rec.Body.String())
	}
}


