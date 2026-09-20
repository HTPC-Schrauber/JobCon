package web

import (
	"jobcon/internal/auth"
	"jobcon/internal/config"
	"jobcon/internal/db"
	"jobcon/internal/runner"
	"jobcon/internal/storage"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWebHandlerDashboardAndAuth(t *testing.T) {
	tmpDir := t.TempDir()
	database, err := db.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	logStore, err := storage.NewLogStorage(filepath.Join(tmpDir, "logs"), true)
	if err != nil {
		t.Fatalf("failed to init log storage: %v", err)
	}

	cfg := config.DefaultConfig()
	sshRunner := runner.NewSSHRunner("/tmp/key", 5, 5)
	execManager := runner.NewExecutionManager(database, logStore, sshRunner, &cfg.Nexus)
	localAuth := auth.NewLocalAuthenticator(database)
	sessions := auth.NewSessionManager(1 * time.Hour)
	authMW := auth.NewMiddleware(localAuth, database, sessions)

	webHandler, err := NewWebHandler(database, execManager, logStore, authMW, localAuth, sessions, cfg)
	if err != nil {
		t.Fatalf("failed to create web handler: %v", err)
	}

	mux := http.NewServeMux()
	webHandler.RegisterRoutes(mux)

	// 1. Unauthenticated request to / should redirect to /login
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("expected 303 redirect to /login, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Errorf("expected Location /login, got %s", loc)
	}

	// 2. GET /login should render 200 OK
	req = httptest.NewRequest("GET", "/login", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 OK for /login, got %d", rec.Code)
	}

	// 3. Static asset GET /static/style.css should return 200
	req = httptest.NewRequest("GET", "/static/style.css", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 OK for /static/style.css, got %d", rec.Code)
	}

	// 4. Authenticated request to /
	user := &db.User{ID: "u1", Username: "admin", Role: "admin", DisplayName: "Admin"}
	sessionToken := sessions.CreateSession(user)
	req = httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: "jobcon_session", Value: sessionToken})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	t.Logf("Authenticated / response: Status=%d, Body length=%d, Body=%s", rec.Code, rec.Body.Len(), rec.Body.String())
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 OK for /dashboard, got %d", rec.Code)
	}
	if rec.Body.Len() == 0 {
		t.Errorf("expected non-empty body for dashboard, got 0 bytes")
	}

	// 5. Executions page GET /executions
	req = httptest.NewRequest("GET", "/executions", nil)
	req.AddCookie(&http.Cookie{Name: "jobcon_session", Value: sessionToken})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 OK for /executions, got %d", rec.Code)
	}

	// 6. Settings System page GET /settings/system
	req = httptest.NewRequest("GET", "/settings/system", nil)
	req.AddCookie(&http.Cookie{Name: "jobcon_session", Value: sessionToken})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 OK for /settings/system, got %d", rec.Code)
	}
}

func TestWebJobCRUDAndSettings(t *testing.T) {
	tmpDir := t.TempDir()
	database, err := db.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	logStore, _ := storage.NewLogStorage(filepath.Join(tmpDir, "logs"), true)
	cfg := config.DefaultConfig()
	sshRunner := runner.NewSSHRunner("/tmp/key", 5, 5)
	execManager := runner.NewExecutionManager(database, logStore, sshRunner, &cfg.Nexus)
	localAuth := auth.NewLocalAuthenticator(database)
	sessions := auth.NewSessionManager(1 * time.Hour)
	authMW := auth.NewMiddleware(localAuth, database, sessions)

	webHandler, err := NewWebHandler(database, execManager, logStore, authMW, localAuth, sessions, cfg)
	if err != nil {
		t.Fatalf("failed to create web handler: %v", err)
	}

	mux := http.NewServeMux()
	webHandler.RegisterRoutes(mux)

	user := &db.User{ID: "admin_u", Username: "admin", Role: "admin", DisplayName: "Admin"}
	token := sessions.CreateSession(user)

	// Create test server
	_ = database.CreateServer(&db.Server{ID: "srv1", Name: "Server 1", Host: "10.0.0.1", User: "talend"})

	// 1. Create Job via POST /web/jobs/create
	form := url.Values{
		"id":             {"job_test_1"},
		"name":           {"Initial Job Name"},
		"server_id":      {"srv1"},
		"group_id":       {"de.company.etl"},
		"artifact_id":    {"InitialJob"},
		"active_version": {"1.0.0"},
		"nexus_repo":     {"releases"},
	}
	req := httptest.NewRequest("POST", "/web/jobs/create", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "jobcon_session", Value: token})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect 303 after job create, got %d", rec.Code)
	}

	j, err := database.GetJob("job_test_1")
	if err != nil || j.Name != "Initial Job Name" {
		t.Fatalf("job was not created correctly: %v", err)
	}

	// 2. Update Job via POST /web/jobs/job_test_1/update
	updateForm := url.Values{
		"id":               {"job_test_1"},
		"name":             {"Updated Job Name"},
		"server_id":        {"srv1"},
		"group_id":         {"de.company.etl.v2"},
		"artifact_id":      {"UpdatedJob"},
		"active_version":   {"2.0.0"},
		"nexus_repo":       {"snapshots"},
		"default_context":  {"Production"},
		"retention_runs":   {"15"},
		"allow_concurrent": {"1"},
	}
	req = httptest.NewRequest("POST", "/web/jobs/job_test_1/update", strings.NewReader(updateForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "jobcon_session", Value: token})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect 303 after job update, got %d", rec.Code)
	}

	jUpdated, err := database.GetJob("job_test_1")
	if err != nil {
		t.Fatalf("failed to retrieve updated job: %v", err)
	}
	if jUpdated.Name != "Updated Job Name" || jUpdated.ActiveVersion != "2.0.0" || !jUpdated.AllowConcurrent || jUpdated.RetentionRuns != 15 {
		t.Errorf("job updates not applied properly: %+v", jUpdated)
	}

	// 3. Settings: Update Nexus credentials via POST /web/settings/nexus
	nexusForm := url.Values{
		"nexus_base_url": {"https://nexus.test.internal/repo"},
		"nexus_username": {"nexus_user_test"},
		"nexus_password": {"secret123"},
	}
	req = httptest.NewRequest("POST", "/web/settings/nexus", strings.NewReader(nexusForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "jobcon_session", Value: token})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect 303 after nexus settings update, got %d", rec.Code)
	}

	urlSetting, _ := database.GetSetting("nexus_base_url", "")
	if urlSetting != "https://nexus.test.internal/repo" {
		t.Errorf("expected updated nexus_base_url, got %s", urlSetting)
	}
	userSetting, _ := database.GetSetting("nexus_username", "")
	if userSetting != "nexus_user_test" {
		t.Errorf("expected updated nexus_username, got %s", userSetting)
	}
	passSetting, _ := database.GetSetting("nexus_password", "")
	if passSetting != "secret123" {
		t.Errorf("expected updated nexus_password, got %s", passSetting)
	}

	// 3b. Settings: Update Nexus to anonymous (with nexus_anonymous checkbox)
	anonNexusForm := url.Values{
		"nexus_base_url":  {"https://nexus.test.internal/repo"},
		"nexus_anonymous": {"true"},
		"nexus_username":  {"old_user"},
		"nexus_password":  {"old_pass"},
	}
	req = httptest.NewRequest("POST", "/web/settings/nexus", strings.NewReader(anonNexusForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "jobcon_session", Value: token})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect 303 after anonymous nexus update, got %d", rec.Code)
	}

	anonSetting, _ := database.GetSetting("nexus_anonymous", "")
	if anonSetting != "true" {
		t.Errorf("expected nexus_anonymous = true, got %q", anonSetting)
	}
	userSetting, _ = database.GetSetting("nexus_username", "not-empty")
	if userSetting != "" {
		t.Errorf("expected empty nexus_username for anonymous access, got %q", userSetting)
	}
	passSetting, _ = database.GetSetting("nexus_password", "not-empty")
	if passSetting != "" {
		t.Errorf("expected cleared nexus_password for anonymous access, got %q", passSetting)
	}

	// Verify settings page renders with anonymous checkbox checked
	req = httptest.NewRequest("GET", "/settings/system", nil)
	req.AddCookie(&http.Cookie{Name: "jobcon_session", Value: token})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on /settings/system, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "id=\"nexusAnonymous\"") || !strings.Contains(rec.Body.String(), "checked") {
		t.Errorf("expected settings page to contain checked nexusAnonymous checkbox")
	}

	// 3c. Settings: Empty Base URL validation
	invalidNexusForm := url.Values{
		"nexus_base_url": {""},
		"nexus_username": {""},
	}
	req = httptest.NewRequest("POST", "/web/settings/nexus", strings.NewReader(invalidNexusForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "jobcon_session", Value: token})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect 303 after invalid nexus update, got %d", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("Location"), "error=") {
		t.Errorf("expected error redirect for empty nexus_base_url, got %s", rec.Header().Get("Location"))
	}

	// 4. Settings: Add Nexus Repository via POST /web/settings/nexus/repo/add
	repoAddForm := url.Values{
		"repo_id":    {"custom_releases"},
		"repo_label": {"Custom Releases (Internal)"},
	}
	req = httptest.NewRequest("POST", "/web/settings/nexus/repo/add", strings.NewReader(repoAddForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "jobcon_session", Value: token})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect 303 after repo add, got %d", rec.Code)
	}

	repos := webHandler.getNexusRepositories()
	found := false
	for _, r := range repos {
		if r.ID == "custom_releases" && r.Label == "Custom Releases (Internal)" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected custom_releases in repositories, got %+v", repos)
	}

	// 5. Delete Job via POST /web/jobs/job_test_1/delete
	req = httptest.NewRequest("POST", "/web/jobs/job_test_1/delete", nil)
	req.AddCookie(&http.Cookie{Name: "jobcon_session", Value: token})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect 303 after job delete, got %d", rec.Code)
	}

	_, err = database.GetJob("job_test_1")
	if err == nil {
		t.Errorf("expected job_test_1 to be deleted, but still exists")
	}
}

func TestDashboardGroupingAndColumnFilters(t *testing.T) {
	tmpDir := t.TempDir()
	database, err := db.Open(filepath.Join(tmpDir, "test.db"))
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	logStore, _ := storage.NewLogStorage(filepath.Join(tmpDir, "logs"), true)
	cfg := config.DefaultConfig()
	sshRunner := runner.NewSSHRunner("/tmp/key", 5, 5)
	execManager := runner.NewExecutionManager(database, logStore, sshRunner, &cfg.Nexus)
	localAuth := auth.NewLocalAuthenticator(database)
	sessions := auth.NewSessionManager(1 * time.Hour)
	authMW := auth.NewMiddleware(localAuth, database, sessions)

	webHandler, err := NewWebHandler(database, execManager, logStore, authMW, localAuth, sessions, cfg)
	if err != nil {
		t.Fatalf("failed to create web handler: %v", err)
	}

	mux := http.NewServeMux()
	webHandler.RegisterRoutes(mux)

	user := &db.User{ID: "viewer_u", Username: "viewer", Role: "viewer", DisplayName: "Viewer"}
	token := sessions.CreateSession(user)

	_ = database.CreateServer(&db.Server{ID: "srv1", Name: "Server 1", Host: "10.0.0.1", User: "talend"})
	_ = database.CreateJob(&db.Job{ID: "j1", Name: "Job Alpha", ServerID: "srv1", GroupID: "group.sales", ArtifactID: "sales_alpha", ActiveVersion: "1.0", NexusRepo: "releases"})
	_ = database.CreateJob(&db.Job{ID: "j2", Name: "Job Beta", ServerID: "srv1", GroupID: "group.billing", ArtifactID: "billing_beta", ActiveVersion: "1.0", NexusRepo: "releases"})

	// 1. GET /?group_by=group_id
	req := httptest.NewRequest("GET", "/?group_by=group_id", nil)
	req.AddCookie(&http.Cookie{Name: "jobcon_session", Value: token})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}
	body := rec.Body.String()

	// Check grouping checkbox is present and checked
	if !strings.Contains(body, `id="groupByCheckbox" checked`) {
		t.Errorf("expected groupByCheckbox to be checked in HTML")
	}

	// Check column filter row
	if !strings.Contains(body, `class="table-filter-row"`) {
		t.Errorf("expected table-filter-row in HTML")
	}
	if !strings.Contains(body, `id="colSearchInput"`) {
		t.Errorf("expected colSearchInput in HTML")
	}
	if !strings.Contains(body, `name="group_id"`) {
		t.Errorf("expected group_id filter in HTML")
	}
	if !strings.Contains(body, `name="server_id"`) {
		t.Errorf("expected server_id filter in HTML")
	}

	// Check group header rows
	if !strings.Contains(body, `group-header-row`) {
		t.Errorf("expected group-header-row in HTML")
	}
	if !strings.Contains(body, `group.billing`) || !strings.Contains(body, `group.sales`) {
		t.Errorf("expected groups billing and sales in HTML")
	}

	// 2. GET /?search=Alpha
	req = httptest.NewRequest("GET", "/?search=Alpha", nil)
	req.AddCookie(&http.Cookie{Name: "jobcon_session", Value: token})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}
	bodySearch := rec.Body.String()
	if !strings.Contains(bodySearch, "Job Alpha") {
		t.Errorf("expected Job Alpha in search results")
	}
	if strings.Contains(bodySearch, "Job Beta") {
		t.Errorf("expected Job Beta to be filtered out")
	}
	if !strings.Contains(bodySearch, "Reset") {
		t.Errorf("expected Reset button when filter is active")
	}
}


