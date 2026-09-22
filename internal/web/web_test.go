package web

import (
	"jobcon/internal/auth"
	"jobcon/internal/config"
	"jobcon/internal/db"
	"jobcon/internal/nexus"
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
	syncer := nexus.NewSyncer(database, cfg, func() *nexus.Client { return nexus.NewClient("", "", "") })

	webHandler, err := NewWebHandler(database, execManager, logStore, authMW, localAuth, sessions, cfg, syncer)
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

	// 3. Test /jobs and /dashboard routes with and without filters
	jobURLs := []string{
		"/jobs",
		"/jobs?search=Alpha",
		"/jobs?group_id=group.sales",
		"/dashboard",
		"/dashboard?search=Alpha",
	}
	for _, u := range jobURLs {
		r := httptest.NewRequest("GET", u, nil)
		r.AddCookie(&http.Cookie{Name: "jobcon_session", Value: token})
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("expected 200 OK for %s, got %d", u, w.Code)
		}
	}
}

func TestWebServerSidepanelAndCRUD(t *testing.T) {
	tmpDir := t.TempDir()
	database, err := db.Open(filepath.Join(tmpDir, "test_web_server.db"))
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

	// Admin user
	adminUser := &db.User{
		ID:          "admin-1",
		Username:    "admin",
		DisplayName: "Administrator",
		Role:        auth.RoleAdmin,
		IsActive:    true,
	}
	_ = database.CreateUser(adminUser)
	token := sessions.CreateSession(adminUser)

	// 1. GET /settings/servers (empty)
	req := httptest.NewRequest("GET", "/settings/servers", nil)
	req.AddCookie(&http.Cookie{Name: "jobcon_session", Value: token})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for /settings/servers, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "serverSidepanel") {
		t.Errorf("expected serverSidepanel in HTML")
	}
	if !strings.Contains(body, "editServerModal") {
		t.Errorf("expected editServerModal in HTML")
	}
	if !strings.Contains(body, "deleteServerModal") {
		t.Errorf("expected deleteServerModal in HTML")
	}

	// 2. Create Server 1 via POST /web/servers/create
	form := url.Values{
		"id":            {"srv-web-1"},
		"name":          {"Production Node 1"},
		"host":          {"10.0.1.1"},
		"port":          {"22"},
		"user":          {"talend"},
		"ssh_key_path":  {"/root/.ssh/id_rsa"},
		"jobs_dir":      {"/custom/talend/jobs"},
		"scripts_dir":   {"/custom/talend/scripts"},
		"keep_releases": {"4"},
	}
	req = httptest.NewRequest("POST", "/web/servers/create", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "jobcon_session", Value: token})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d", rec.Code)
	}

	srv1, err := database.GetServer("srv-web-1")
	if err != nil {
		t.Fatalf("failed to get srv-web-1: %v", err)
	}
	if srv1.JobsDir != "/custom/talend/jobs" || srv1.ScriptsDir != "/custom/talend/scripts" || srv1.KeepReleases != 4 {
		t.Errorf("expected custom dirs and keep_releases=4, got jobs=%s scripts=%s keep_releases=%d", srv1.JobsDir, srv1.ScriptsDir, srv1.KeepReleases)
	}

	// Create Server 2
	srv2 := &db.Server{
		ID:         "srv-web-2",
		Name:       "Production Node 2",
		Host:       "10.0.1.2",
		Port:       22,
		User:       "talend",
		SSHKeyPath: "/root/.ssh/id_rsa",
	}
	_ = database.CreateServer(srv2)

	// 3. Update Server 1 via POST /web/servers/srv-web-1/update
	updateForm := url.Values{
		"name":          {"Production Node 1 Renamed"},
		"host":          {"10.0.1.100"},
		"port":          {"2222"},
		"user":          {"talend_admin"},
		"ssh_key_path":  {"/root/.ssh/id_ed25519"},
		"jobs_dir":      {"/opt/talend/jobs"},
		"scripts_dir":   {"/opt/talend/scripts"},
		"keep_releases": {"2"},
	}
	req = httptest.NewRequest("POST", "/web/servers/srv-web-1/update", strings.NewReader(updateForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "jobcon_session", Value: token})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect for update, got %d", rec.Code)
	}

	srv1Updated, _ := database.GetServer("srv-web-1")
	if srv1Updated.Name != "Production Node 1 Renamed" || srv1Updated.Port != 2222 || srv1Updated.User != "talend_admin" || srv1Updated.KeepReleases != 2 {
		t.Errorf("server 1 update failed: %+v", srv1Updated)
	}

	// 4. Create Job on Server 1
	job := &db.Job{
		ID:            "job-web-1",
		Name:          "Web Test Job",
		ServerID:      "srv-web-1",
		GroupID:       "org.test",
		ArtifactID:    "web_job",
		ActiveVersion: "1.0",
		NexusRepo:     "releases",
	}
	_ = database.CreateJob(job)

	// 5. Delete Server 1 without target_server_id -> fails with error redirect
	delForm := url.Values{}
	req = httptest.NewRequest("POST", "/web/servers/srv-web-1/delete", strings.NewReader(delForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "jobcon_session", Value: token})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "error=") {
		t.Errorf("expected error in redirect location when deleting server with jobs, got %s", loc)
	}

	// 6. Delete Server 1 with target_server_id=srv-web-2 -> succeeds, jobs migrated
	delFormReassign := url.Values{
		"target_server_id": {"srv-web-2"},
	}
	req = httptest.NewRequest("POST", "/web/servers/srv-web-1/delete", strings.NewReader(delFormReassign.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "jobcon_session", Value: token})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d", rec.Code)
	}
	locSuccess := rec.Header().Get("Location")
	if !strings.Contains(locSuccess, "success=") {
		t.Errorf("expected success in redirect location, got %s", locSuccess)
	}

	// Verify Server 1 is deleted
	if _, err := database.GetServer("srv-web-1"); err != db.ErrNotFound {
		t.Errorf("expected srv-web-1 to be deleted, got %v", err)
	}

	// Verify Job is now assigned to Server 2
	jMoved, _ := database.GetJob("job-web-1")
	if jMoved.ServerID != "srv-web-2" {
		t.Errorf("expected job on srv-web-2, got %s", jMoved.ServerID)
	}
}

func TestWebLanguageSwitcher(t *testing.T) {
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

	user := &db.User{ID: "u_lang", Username: "languser", Role: "admin", DisplayName: "Lang User"}
	_ = database.CreateUser(user)
	token := sessions.CreateSession(user)

	// 1. POST /web/set-language with lang=de
	form := url.Values{"lang": {"de"}}
	req := httptest.NewRequest("POST", "/web/set-language", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "jobcon_session", Value: token})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d", rec.Code)
	}

	cookieFound := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == "jobcon_lang" && c.Value == "de" {
			cookieFound = true
			break
		}
	}
	if !cookieFound {
		t.Errorf("expected jobcon_lang=de cookie in response headers")
	}

	pref, err := database.GetUserPreference("u_lang", "language", "en")
	if err != nil || pref != "de" {
		t.Errorf("expected user preference 'de', got %s (err: %v)", pref, err)
	}

	// 2. GET /web/set-language?lang=en
	req = httptest.NewRequest("GET", "/web/set-language?lang=en", nil)
	req.AddCookie(&http.Cookie{Name: "jobcon_session", Value: token})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d", rec.Code)
	}
	prefEn, _ := database.GetUserPreference("u_lang", "language", "de")
	if prefEn != "en" {
		t.Errorf("expected user preference 'en', got %s", prefEn)
	}

	// 3. Render dashboard with lang cookie
	req = httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: "jobcon_session", Value: token})
	req.AddCookie(&http.Cookie{Name: "jobcon_lang", Value: "de"})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK rendering with german language, got %d", rec.Code)
	}
}

func TestWebBulkJobActions(t *testing.T) {
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

	user := &db.User{ID: "admin_bulk", Username: "admin", Role: "admin"}
	token := sessions.CreateSession(user)

	srv := &db.Server{ID: "srv_bulk", Name: "Server Bulk", Host: "127.0.0.1", User: "talend"}
	_ = database.CreateServer(srv)

	j1 := &db.Job{ID: "job_b1", Name: "Job B1", ServerID: "srv_bulk", GroupID: "g", ArtifactID: "a1", ActiveVersion: "1.0", NexusRepo: "releases"}
	j2 := &db.Job{ID: "job_b2", Name: "Job B2", ServerID: "srv_bulk", GroupID: "g", ArtifactID: "a2", ActiveVersion: "1.0", NexusRepo: "releases"}
	_ = database.CreateJob(j1)
	_ = database.CreateJob(j2)

	// 1. Bulk Run
	runForm := url.Values{"job_ids": {"job_b1,job_b2"}}
	req := httptest.NewRequest("POST", "/web/jobs/bulk/run", strings.NewReader(runForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "jobcon_session", Value: token})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect after bulk run, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/executions" {
		t.Errorf("expected redirect to /executions, got %s", loc)
	}

	// 2. Bulk Deploy
	deployForm := url.Values{
		"job_ids": {"job_b1,job_b2"},
		"version": {"2.0.0"},
	}
	req = httptest.NewRequest("POST", "/web/jobs/bulk/deploy", strings.NewReader(deployForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "jobcon_session", Value: token})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect after bulk deploy, got %d", rec.Code)
	}

	// 3. Bulk Undeploy
	undeployForm := url.Values{"job_ids": {"job_b1,job_b2"}}
	req = httptest.NewRequest("POST", "/web/jobs/bulk/undeploy", strings.NewReader(undeployForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "jobcon_session", Value: token})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect after bulk undeploy, got %d", rec.Code)
	}
}

func TestWebJobDeleteWithUndeployServer(t *testing.T) {
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

	user := &db.User{ID: "admin_del", Username: "admin", Role: "admin"}
	token := sessions.CreateSession(user)

	srv := &db.Server{ID: "srv_del", Name: "Server Del", Host: "127.0.0.1", User: "talend"}
	_ = database.CreateServer(srv)

	j := &db.Job{
		ID:              "job_del_1",
		Name:            "Job To Delete",
		ServerID:        "srv_del",
		GroupID:         "g",
		ArtifactID:      "a",
		ActiveVersion:   "1.0",
		NexusRepo:       "releases",
		IsDeployed:      true,
		DeployedVersion: "1.0",
	}
	_ = database.CreateJob(j)

	delForm := url.Values{"undeploy_server": {"true"}}
	req := httptest.NewRequest("POST", "/web/jobs/job_del_1/delete", strings.NewReader(delForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "jobcon_session", Value: token})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect after delete, got %d", rec.Code)
	}

	if _, err := database.GetJob("job_del_1"); err != db.ErrNotFound {
		t.Errorf("expected job_del_1 to be deleted from database, got %v", err)
	}
}

func TestWebJobAndServerEnvFile(t *testing.T) {
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

	user := &db.User{ID: "admin_env", Username: "admin", Role: "admin"}
	token := sessions.CreateSession(user)

	// 1. Create Server with env_file
	srvForm := url.Values{
		"id":       {"srv_env_1"},
		"name":     {"Server with Env"},
		"host":     {"10.0.0.5"},
		"user":     {"talend"},
		"env_file": {"/opt/talend/.server.env"},
	}
	req := httptest.NewRequest("POST", "/web/servers/create", strings.NewReader(srvForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "jobcon_session", Value: token})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect after server create, got %d", rec.Code)
	}

	srv, err := database.GetServer("srv_env_1")
	if err != nil || srv.EnvFile != "/opt/talend/.server.env" {
		t.Errorf("expected server env_file '/opt/talend/.server.env', got '%s'", srv.EnvFile)
	}

	// 2. Create Job with env_file
	jobForm := url.Values{
		"id":             {"job_env_1"},
		"name":           {"Job with Env"},
		"server_id":      {"srv_env_1"},
		"group_id":       {"g"},
		"artifact_id":    {"a"},
		"active_version": {"1.0"},
		"nexus_repo":     {"releases"},
		"env_file":       {"/opt/talend/jobs/.job.env"},
	}
	req = httptest.NewRequest("POST", "/web/jobs/create", strings.NewReader(jobForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "jobcon_session", Value: token})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect after job create, got %d", rec.Code)
	}

	job, err := database.GetJob("job_env_1")
	if err != nil || job.EnvFile != "/opt/talend/jobs/.job.env" {
		t.Errorf("expected job env_file '/opt/talend/jobs/.job.env', got '%s'", job.EnvFile)
	}

	// 3. Update Job env_file
	jobUpdateForm := url.Values{
		"name":           {"Job with Env Updated"},
		"server_id":      {"srv_env_1"},
		"group_id":       {"g"},
		"artifact_id":    {"a"},
		"active_version": {"1.0"},
		"nexus_repo":     {"releases"},
		"env_file":       {"/opt/talend/jobs/.job_v2.env"},
	}
	req = httptest.NewRequest("POST", "/web/jobs/job_env_1/update", strings.NewReader(jobUpdateForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "jobcon_session", Value: token})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect after job update, got %d", rec.Code)
	}

	jobUpdated, _ := database.GetJob("job_env_1")
	if jobUpdated.EnvFile != "/opt/talend/jobs/.job_v2.env" {
		t.Errorf("expected updated job env_file '/opt/talend/jobs/.job_v2.env', got '%s'", jobUpdated.EnvFile)
	}
}

func TestHighDensityUIAndNexusSyncAndConcurrencySettings(t *testing.T) {
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
	syncer := nexus.NewSyncer(database, cfg, func() *nexus.Client { return nexus.NewClient("", "", "") })

	webHandler, err := NewWebHandler(database, execManager, logStore, authMW, localAuth, sessions, cfg, syncer)
	if err != nil {
		t.Fatalf("failed to create web handler: %v", err)
	}

	mux := http.NewServeMux()
	webHandler.RegisterRoutes(mux)

	user := &db.User{ID: "admin_u", Username: "admin", Role: "admin", DisplayName: "Maik Opitz"}
	sessionToken := sessions.CreateSession(user)

	// 1. Add server and running job
	server := &db.Server{ID: "srv_hd_1", Name: "TESTSERVER-01", Host: "127.0.0.1", Port: 22, User: "talend", Status: "online"}
	_ = database.CreateServer(server)

	job := &db.Job{ID: "job_running_1", Name: "Echo Test", ServerID: "srv_hd_1", GroupID: "com.opitzhome.test", ArtifactID: "echo_test", ActiveVersion: "1.0.0", NexusRepo: "releases"}
	_ = database.CreateJob(job)

	exec := &db.Execution{ID: "exec_run_1", JobID: "job_running_1", Status: "running", TriggeredBy: "web:admin", LogPath: "/tmp/fake.log"}
	_ = database.CreateExecution(exec)

	// 2. GET / (Dashboard)
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: "jobcon_session", Value: sessionToken})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for dashboard, got %d", rec.Code)
	}

	html := rec.Body.String()
	// Verify Operational High-Density Status Strip
	if !strings.Contains(html, "Jobs:") {
		t.Errorf("expected 'Jobs:' in status strip")
	}
	if !strings.Contains(html, "JobServer:") {
		t.Errorf("expected 'JobServer:' in status strip")
	}
	if !strings.Contains(html, "online") {
		t.Errorf("expected 'online' in status strip")
	}
	if !strings.Contains(html, "Laufend:") {
		t.Errorf("expected 'Laufend:' in status strip")
	}
	if !strings.Contains(html, "Nexus Sync:") {
		t.Errorf("expected 'Nexus Sync:' in status strip")
	}
	if !strings.Contains(html, "table-scroll-container") {
		t.Errorf("expected 'table-scroll-container' for sticky headers")
	}
	if !strings.Contains(html, "bulkConcurrencyInput") {
		t.Errorf("expected 'bulkConcurrencyInput' for parallel concurrency override")
	}
	if !strings.Contains(html, "Parallel:") {
		t.Errorf("expected 'Parallel:' label in bulk bar")
	}
	// Verify running job indicator
	if !strings.Contains(html, "animate-ping") {
		t.Errorf("expected 'animate-ping' pulsing dot for running job")
	}
	if !strings.Contains(html, "Live-Log") {
		t.Errorf("expected 'Live-Log' link for running job")
	}
	if !strings.Contains(html, "RUNNING") {
		t.Errorf("expected 'RUNNING' badge for running job")
	}

	// 3. GET /settings/system
	req = httptest.NewRequest("GET", "/settings/system", nil)
	req.AddCookie(&http.Cookie{Name: "jobcon_session", Value: sessionToken})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for settings system, got %d", rec.Code)
	}
	settingsHTML := rec.Body.String()
	if !strings.Contains(settingsHTML, "Nexus Synchronisation &amp; Lokaler Cache") {
		t.Errorf("expected Nexus Synchronisation card in settings")
	}
	if !strings.Contains(settingsHTML, "Job-Ausführung &amp; Warteschlange") {
		t.Errorf("expected Job-Ausführung & Warteschlange card in settings")
	}
	if !strings.Contains(settingsHTML, "max_concurrent_jobs") {
		t.Errorf("expected max_concurrent_jobs input in settings")
	}

	// 4. POST /web/settings/concurrency
	concForm := url.Values{
		"max_concurrent_jobs": {"5"},
	}
	req = httptest.NewRequest("POST", "/web/settings/concurrency", strings.NewReader(concForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "jobcon_session", Value: sessionToken})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("expected 303 redirect after concurrency update, got %d", rec.Code)
	}

	storedConc, err := database.GetSetting("max_concurrent_jobs", "2")
	if err != nil || storedConc != "5" {
		t.Errorf("expected stored max_concurrent_jobs='5', got '%s', err: %v", storedConc, err)
	}

	// 5. POST /web/settings/nexus/interval
	intvForm := url.Values{
		"interval_minutes": {"15"},
	}
	req = httptest.NewRequest("POST", "/web/settings/nexus/interval", strings.NewReader(intvForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "jobcon_session", Value: sessionToken})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("expected 303 redirect after interval update, got %d", rec.Code)
	}

	if syncer.GetIntervalMinutes() != 15 {
		t.Errorf("expected syncer interval 15, got %d", syncer.GetIntervalMinutes())
	}
}

func TestWebLDAPSettingsAndUserManagement(t *testing.T) {
	tmpDir := t.TempDir()
	database, err := db.Open(filepath.Join(tmpDir, "test_ldap_web.db"))
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
	multiAuth := auth.NewMultiAuthenticator(database, cfg)
	sessions := auth.NewSessionManager(1 * time.Hour)
	authMW := auth.NewMiddleware(multiAuth, database, sessions)

	webHandler, err := NewWebHandler(database, execManager, logStore, authMW, multiAuth, sessions, cfg)
	if err != nil {
		t.Fatalf("failed to create web handler: %v", err)
	}

	mux := http.NewServeMux()
	webHandler.RegisterRoutes(mux)

	// Create initial local admin
	adminUser := &db.User{
		ID:           "admin_1",
		Username:     "admin",
		Role:         "admin",
		AuthSource:   "local",
		IsActive:     true,
		DisplayName:  "Administrator",
		PasswordHash: "fakehash",
	}
	_ = database.CreateUser(adminUser)
	sessionToken := sessions.CreateSession(adminUser)

	// 1. GET /settings/ldap
	req := httptest.NewRequest("GET", "/settings/ldap", nil)
	req.AddCookie(&http.Cookie{Name: "jobcon_session", Value: sessionToken})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected GET /settings/ldap to return 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "LDAP / Active Directory") {
		t.Errorf("expected page to contain 'LDAP / Active Directory'")
	}
	if !strings.Contains(body, "sAMAccountName") {
		t.Errorf("expected page to contain attribute 'sAMAccountName'")
	}

	// 2. POST /web/settings/ldap (Save settings)
	ldapForm := url.Values{
		"ldap_enabled":               {"true"},
		"ldap_protocol":              {"ldaps"},
		"ldap_host":                  {"ad.example.local"},
		"ldap_port":                  {"636"},
		"ldap_insecure_skip_verify":  {"true"},
		"ldap_user_bind_template":    {"%s@example.local"},
		"ldap_base_dn":               {"OU=Users,DC=example,DC=local"},
		"ldap_attr_username":         {"sAMAccountName"},
		"ldap_attr_first_name":       {"givenName"},
		"ldap_attr_last_name":        {"sn"},
		"ldap_attr_email":            {"mail"},
	}
	req = httptest.NewRequest("POST", "/web/settings/ldap", strings.NewReader(ldapForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "jobcon_session", Value: sessionToken})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected POST /web/settings/ldap to redirect, got %d", rec.Code)
	}

	// Verify saved settings in DB
	savedHost, _ := database.GetSetting("ldap_host", "")
	if savedHost != "ad.example.local" {
		t.Errorf("expected saved ldap_host='ad.example.local', got %q", savedHost)
	}
	savedPort, _ := database.GetSetting("ldap_port", "")
	if savedPort != "636" {
		t.Errorf("expected saved ldap_port='636', got %q", savedPort)
	}
	savedSSL, _ := database.GetSetting("ldap_use_ssl", "")
	if savedSSL != "true" {
		t.Errorf("expected saved ldap_use_ssl='true', got %q", savedSSL)
	}

	// 3. POST /web/users/create (Create an LDAP user)
	createLDAPForm := url.Values{
		"auth_source":  {"ldap"},
		"username":     {"johndoe"},
		"display_name": {"John Doe"},
		"email":        {"johndoe@example.local"},
		"role":         {"operator"},
	}
	req = httptest.NewRequest("POST", "/web/users/create", strings.NewReader(createLDAPForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "jobcon_session", Value: sessionToken})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected POST /web/users/create to redirect, got %d", rec.Code)
	}

	createdLDAPUser, err := database.GetUserByUsername("johndoe")
	if err != nil {
		t.Fatalf("failed to retrieve created LDAP user: %v", err)
	}
	if createdLDAPUser.AuthSource != "ldap" {
		t.Errorf("expected auth_source 'ldap', got %s", createdLDAPUser.AuthSource)
	}
	if createdLDAPUser.Role != "operator" {
		t.Errorf("expected role 'operator', got %s", createdLDAPUser.Role)
	}

	// 4. Protection test: try to delete the only local admin
	req = httptest.NewRequest("POST", "/web/users/admin_1/delete", nil)
	req.AddCookie(&http.Cookie{Name: "jobcon_session", Value: sessionToken})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", rec.Code)
	}
	location := rec.Header().Get("Location")
	if !strings.Contains(location, "error=") {
		t.Errorf("expected redirect location to contain error, got %s", location)
	}

	// Verify admin_1 is still in DB
	checkAdmin, err := database.GetUserByID("admin_1")
	if err != nil || checkAdmin == nil {
		t.Fatalf("last local admin was erroneously deleted!")
	}

	// 5. Protection test: try to demote or deactivate the only local admin
	demoteForm := url.Values{
		"display_name": {"Administrator"},
		"email":        {"admin@local"},
		"role":         {"viewer"},
		"is_active":    {"true"},
	}
	req = httptest.NewRequest("POST", "/web/users/admin_1/update", strings.NewReader(demoteForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "jobcon_session", Value: sessionToken})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	location = rec.Header().Get("Location")
	if !strings.Contains(location, "error=") {
		t.Errorf("expected demote redirect location to contain error, got %s", location)
	}

	// 6. Delete LDAP user (should succeed without error)
	req = httptest.NewRequest("POST", "/web/users/"+createdLDAPUser.ID+"/delete", nil)
	req.AddCookie(&http.Cookie{Name: "jobcon_session", Value: sessionToken})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	location = rec.Header().Get("Location")
	if !strings.Contains(location, "success=") {
		t.Errorf("expected successful deletion of LDAP user, got %s", location)
	}
}





