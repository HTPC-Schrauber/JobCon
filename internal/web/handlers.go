package web

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"jobcon/internal/auth"
	"jobcon/internal/config"
	"jobcon/internal/db"
	"jobcon/internal/runner"
	"jobcon/internal/storage"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

//go:embed templates/* static/*
var contentFS embed.FS

type WebHandler struct {
	db        *db.DB
	runner    *runner.ExecutionManager
	storage   *storage.LogStorage
	authMW    *auth.Middleware
	localAuth *auth.LocalAuthenticator
	sessions  *auth.SessionManager
	cfg       *config.Config
	templates map[string]*template.Template
}

func NewWebHandler(
	database *db.DB,
	runner *runner.ExecutionManager,
	logStorage *storage.LogStorage,
	authMW *auth.Middleware,
	localAuth *auth.LocalAuthenticator,
	sessions *auth.SessionManager,
	cfg *config.Config,
) (*WebHandler, error) {
	templates := make(map[string]*template.Template)

	funcMap := template.FuncMap{
		"add": func(a, b int) int { return a + b },
		"sub": func(a, b int) int { return a - b },
	}

	// Login template (standalone)
	loginTmpl, err := template.New("login.html").Funcs(funcMap).ParseFS(contentFS, "templates/login.html")
	if err != nil {
		return nil, fmt.Errorf("failed to parse login.html: %w", err)
	}
	templates["login.html"] = loginTmpl

	// Page templates paired with layout.html
	pages := []string{
		"dashboard.html",
		"execution.html",
		"executions_page.html",
		"settings_servers.html",
		"settings_users.html",
		"settings_system.html",
	}

	for _, page := range pages {
		tmpl, err := template.New("layout.html").Funcs(funcMap).ParseFS(contentFS, "templates/layout.html", "templates/"+page)
		if err != nil {
			return nil, fmt.Errorf("failed to parse template %s: %w", page, err)
		}
		templates[page] = tmpl
	}

	return &WebHandler{
		db:        database,
		runner:    runner,
		storage:   logStorage,
		authMW:    authMW,
		localAuth: localAuth,
		sessions:  sessions,
		cfg:       cfg,
		templates: templates,
	}, nil
}

func (h *WebHandler) render(w http.ResponseWriter, page string, data any) {
	tmpl, ok := h.templates[page]
	if !ok {
		log.Printf("[Web] Template not found: %s", page)
		http.Error(w, "Template not found: "+page, http.StatusInternalServerError)
		return
	}

	templateName := "layout.html"
	if page == "login.html" {
		templateName = "login.html"
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, templateName, data); err != nil {
		log.Printf("[Web] Template execution error for %s: %v", page, err)
		http.Error(w, "Internal Server Error: "+err.Error(), http.StatusInternalServerError)
	}
}

func (h *WebHandler) RegisterRoutes(mux *http.ServeMux) {
	// Static assets
	staticFS, err := fs.Sub(contentFS, "static")
	if err != nil {
		log.Fatalf("failed to create static sub fs: %v", err)
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	// Auth routes
	mux.HandleFunc("GET /login", h.handleLoginPage)
	mux.HandleFunc("POST /login", h.handleLoginSubmit)
	mux.HandleFunc("POST /logout", h.handleLogout)

	// Protected routes
	authWrap := func(fn http.HandlerFunc) http.Handler {
		return h.authMW.RequireAuth(fn)
	}
	adminWrap := func(fn http.HandlerFunc) http.Handler {
		return h.authMW.RequireAuth(h.authMW.RequireRole(auth.RoleAdmin)(fn))
	}

	// Pages
	mux.Handle("GET /{$}", authWrap(h.handleDashboard))
	mux.Handle("GET /jobs", authWrap(h.handleDashboard))
	mux.Handle("GET /jobs/{$}", authWrap(h.handleDashboard))
	mux.Handle("GET /dashboard", authWrap(h.handleDashboard))
	mux.Handle("GET /dashboard/{$}", authWrap(h.handleDashboard))
	mux.Handle("GET /executions", authWrap(h.handleExecutionsPage))
	mux.Handle("GET /executions/{id}", authWrap(h.handleExecutionPage))

	// Settings
	mux.Handle("GET /settings/servers", adminWrap(h.handleSettingsServers))
	mux.Handle("GET /settings/users", adminWrap(h.handleSettingsUsers))
	mux.Handle("GET /settings/system", adminWrap(h.handleSettingsSystem))

	// Form actions - Jobs
	mux.Handle("POST /web/jobs/run", authWrap(h.handleWebJobRun))
	mux.Handle("POST /web/jobs/deploy", authWrap(h.handleWebJobDeploy))
	mux.Handle("POST /web/jobs/create", adminWrap(h.handleWebJobCreate))
	mux.Handle("POST /web/jobs/{id}/update", adminWrap(h.handleWebJobUpdate))
	mux.Handle("POST /web/jobs/{id}/delete", adminWrap(h.handleWebJobDelete))

	// Form actions - Servers, Users, Tokens
	mux.Handle("POST /web/servers/create", adminWrap(h.handleWebServerCreate))
	mux.Handle("POST /web/servers/{id}/update", adminWrap(h.handleWebServerUpdate))
	mux.Handle("POST /web/servers/{id}/delete", adminWrap(h.handleWebServerDelete))
	mux.Handle("POST /web/users/create", adminWrap(h.handleWebUserCreate))
	mux.Handle("POST /web/users/{id}/password", adminWrap(h.handleWebUserPassword))
	mux.Handle("POST /web/tokens/create", adminWrap(h.handleWebTokenCreate))
	mux.Handle("POST /web/tokens/delete", adminWrap(h.handleWebTokenDelete))

	// Form actions - Settings (Nexus & Retention)
	mux.Handle("POST /web/settings/nexus", adminWrap(h.handleWebSettingsNexus))
	mux.Handle("POST /web/settings/nexus/repo/add", adminWrap(h.handleWebNexusRepoAdd))
	mux.Handle("POST /web/settings/nexus/repo/delete", adminWrap(h.handleWebNexusRepoDelete))
	mux.Handle("POST /web/settings/retention/clean", adminWrap(h.handleWebSettingsCleanRetention))
}

// -----------------------------------------------------------------------------
// PAGES
// -----------------------------------------------------------------------------

func (h *WebHandler) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if u := auth.UserFromContext(r.Context()); u != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	h.render(w, "login.html", map[string]any{
		"Error": r.URL.Query().Get("error"),
	})
}

func (h *WebHandler) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	username := r.FormValue("username")
	password := r.FormValue("password")

	user, err := h.localAuth.Authenticate(r.Context(), username, password)
	if err != nil {
		http.Redirect(w, r, "/login?error=Ungültiger+Benutzername+oder+Passwort", http.StatusSeeOther)
		return
	}

	sessionToken := h.sessions.CreateSession(user)
	http.SetCookie(w, &http.Cookie{
		Name:     "jobcon_session",
		Value:    sessionToken,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400, // 24 hours
	})

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *WebHandler) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("jobcon_session"); err == nil {
		h.sessions.DestroySession(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "jobcon_session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (h *WebHandler) handleDashboard(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	q := r.URL.Query()

	// Default page size (from user preferences if available)
	defaultPageSize := 25
	if user != nil {
		if pref, err := h.db.GetUserPreference(user.ID, "job_list_page_size", "25"); err == nil {
			if ps, err2 := strconv.Atoi(pref); err2 == nil && ps > 0 {
				defaultPageSize = ps
			}
		}
	}

	pageSize := defaultPageSize
	if psStr := q.Get("page_size"); psStr != "" {
		if ps, err := strconv.Atoi(psStr); err == nil && ps > 0 {
			pageSize = ps
			if user != nil {
				_ = h.db.SetUserPreference(user.ID, "job_list_page_size", psStr)
			}
		}
	}

	page := 1
	if pStr := q.Get("page"); pStr != "" {
		if p, err := strconv.Atoi(pStr); err == nil && p > 0 {
			page = p
		}
	}

	sortBy := "name"
	if user != nil {
		sortBy, _ = h.db.GetUserPreference(user.ID, "job_list_sort_by", "name")
	}
	if s := q.Get("sort_by"); s != "" {
		sortBy = s
		if user != nil {
			_ = h.db.SetUserPreference(user.ID, "job_list_sort_by", s)
		}
	}

	sortOrder := "asc"
	if user != nil {
		sortOrder, _ = h.db.GetUserPreference(user.ID, "job_list_sort_order", "asc")
	}
	if so := q.Get("sort_order"); so != "" {
		sortOrder = so
		if user != nil {
			_ = h.db.SetUserPreference(user.ID, "job_list_sort_order", so)
		}
	}

	groupBy := ""
	if user != nil {
		groupBy, _ = h.db.GetUserPreference(user.ID, "job_list_group_by", "")
	}
	if q.Has("group_by") {
		groupBy = q.Get("group_by")
		if user != nil {
			_ = h.db.SetUserPreference(user.ID, "job_list_group_by", groupBy)
		}
	}

	filter := db.JobFilter{
		Search:    q.Get("search"),
		GroupID:   q.Get("group_id"),
		ServerID:  q.Get("server_id"),
		Page:      page,
		PageSize:  pageSize,
		SortBy:    sortBy,
		SortOrder: sortOrder,
		GroupBy:   groupBy,
	}

	pagedResult, err := h.db.ListJobsPaged(filter)
	if err != nil {
		log.Printf("[Web] ListJobsPaged error: %v", err)
		http.Error(w, "Fehler beim Laden der Jobs: "+err.Error(), http.StatusInternalServerError)
		return
	}

	distinctGroups, _ := h.db.GetDistinctGroups()
	servers, _ := h.db.ListServers()
	nexusRepos := h.getNexusRepositories()

	data := map[string]any{
		"Title":          "Talend Jobs",
		"CurrentTab":     "dashboard",
		"User":           user,
		"Jobs":           pagedResult.Jobs,
		"TotalCount":     pagedResult.TotalCount,
		"CurrentPage":    pagedResult.CurrentPage,
		"TotalPages":     pagedResult.TotalPages,
		"PageSize":       pagedResult.PageSize,
		"Search":         filter.Search,
		"GroupID":        filter.GroupID,
		"ServerID":       filter.ServerID,
		"SortBy":         filter.SortBy,
		"SortOrder":      filter.SortOrder,
		"GroupBy":        groupBy,
		"DistinctGroups": distinctGroups,
		"Servers":        servers,
		"NexusRepos":     nexusRepos,
	}
	h.render(w, "dashboard.html", data)
}

func (h *WebHandler) handleExecutionsPage(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	status := r.URL.Query().Get("status")
	execs, err := h.db.ListExecutionsFiltered(status, 100)
	if err != nil {
		log.Printf("[Web] ListExecutionsFiltered error: %v", err)
	}

	data := map[string]any{
		"Title":        "Ausführungshistorie",
		"CurrentTab":   "executions",
		"User":         user,
		"Executions":   execs,
		"FilterStatus": status,
	}
	h.render(w, "executions_page.html", data)
}

func (h *WebHandler) handleExecutionPage(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id := r.PathValue("id")
	exec, err := h.db.GetExecution(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	data := map[string]any{
		"Title":      "Execution " + exec.ID,
		"CurrentTab": "executions",
		"User":       user,
		"Exec":       exec,
	}
	h.render(w, "execution.html", data)
}

func (h *WebHandler) handleSettingsServers(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	servers, _ := h.db.ListServers()

	data := map[string]any{
		"Title":      "Execution Server",
		"CurrentTab": "settings",
		"User":       user,
		"Servers":    servers,
		"ErrorMsg":   r.URL.Query().Get("error"),
		"SuccessMsg": r.URL.Query().Get("success"),
	}
	h.render(w, "settings_servers.html", data)
}

func (h *WebHandler) handleSettingsUsers(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	users, _ := h.db.ListUsers()

	data := map[string]any{
		"Title":      "Benutzerverwaltung",
		"CurrentTab": "settings",
		"User":       user,
		"Users":      users,
	}
	h.render(w, "settings_users.html", data)
}

func (h *WebHandler) handleSettingsSystem(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	tokens, _ := h.db.ListAPITokens()

	baseURL, _ := h.db.GetSetting("nexus_base_url", h.cfg.Nexus.BaseURL)
	nexusUser, _ := h.db.GetSetting("nexus_username", h.cfg.Nexus.Username)
	nexusPass, _ := h.db.GetSetting("nexus_password", h.cfg.Nexus.Password)
	anonSetting, _ := h.db.GetSetting("nexus_anonymous", "")

	isAnonymous := false
	if anonSetting == "true" {
		isAnonymous = true
	} else if anonSetting == "false" {
		isAnonymous = false
	} else if nexusUser == "" {
		isAnonymous = true
	}

	repos := h.getNexusRepositories()

	data := map[string]any{
		"Title":             "System & Tokens",
		"CurrentTab":        "settings",
		"User":              user,
		"Tokens":            tokens,
		"NexusBaseURL":      baseURL,
		"NexusUser":         nexusUser,
		"NexusAnonymous":    isAnonymous,
		"HasPassword":       nexusPass != "",
		"NexusRepositories": repos,
		"SuccessMsg":        r.URL.Query().Get("success"),
		"ErrorMsg":          r.URL.Query().Get("error"),
		"NewTokenRaw":       r.URL.Query().Get("token"),
	}
	h.render(w, "settings_system.html", data)
}

// -----------------------------------------------------------------------------
// FORM ACTIONS
// -----------------------------------------------------------------------------

func (h *WebHandler) handleWebJobRun(w http.ResponseWriter, r *http.Request) {
	jobID := r.FormValue("job_id")
	version := r.FormValue("version")
	contextName := r.FormValue("context")
	paramsRaw := r.FormValue("params")

	params := make(map[string]string)
	lines := strings.Split(paramsRaw, "\n")
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" || !strings.Contains(l, "=") {
			continue
		}
		parts := strings.SplitN(l, "=", 2)
		params[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}

	user := auth.UserFromContext(r.Context())
	triggeredBy := "web:unknown"
	if user != nil {
		triggeredBy = "web:" + user.Username
	}

	exec, err := h.runner.StartExecution(r.Context(), jobID, "run", version, contextName, params, triggeredBy)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/executions/"+exec.ID, http.StatusSeeOther)
}

func (h *WebHandler) handleWebJobDeploy(w http.ResponseWriter, r *http.Request) {
	jobID := r.FormValue("job_id")
	version := r.FormValue("version")

	user := auth.UserFromContext(r.Context())
	triggeredBy := "web:unknown"
	if user != nil {
		triggeredBy = "web:" + user.Username
	}

	exec, err := h.runner.StartExecution(r.Context(), jobID, "deploy", version, "", nil, triggeredBy)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/executions/"+exec.ID, http.StatusSeeOther)
}

func (h *WebHandler) handleWebJobCreate(w http.ResponseWriter, r *http.Request) {
	allowConcurrent := r.FormValue("allow_concurrent") == "1"
	job := &db.Job{
		ID:              r.FormValue("id"),
		Name:            r.FormValue("name"),
		ServerID:        r.FormValue("server_id"),
		GroupID:         r.FormValue("group_id"),
		ArtifactID:      r.FormValue("artifact_id"),
		ActiveVersion:   r.FormValue("active_version"),
		NexusRepo:       r.FormValue("nexus_repo"),
		DefaultContext:  "Default",
		AllowConcurrent: allowConcurrent,
		RetentionRuns:   10,
	}

	if err := h.db.CreateJob(job); err != nil {
		http.Error(w, "Fehler beim Anlegen: "+err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *WebHandler) handleWebJobUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		id = r.FormValue("id")
	}

	allowConcurrent := r.FormValue("allow_concurrent") == "1"
	retentionRuns := 10
	if rr, err := strconv.Atoi(r.FormValue("retention_runs")); err == nil && rr > 0 {
		retentionRuns = rr
	}

	job := &db.Job{
		ID:              id,
		Name:            r.FormValue("name"),
		ServerID:        r.FormValue("server_id"),
		GroupID:         r.FormValue("group_id"),
		ArtifactID:      r.FormValue("artifact_id"),
		ActiveVersion:   r.FormValue("active_version"),
		NexusRepo:       r.FormValue("nexus_repo"),
		DefaultContext:  r.FormValue("default_context"),
		AllowConcurrent: allowConcurrent,
		RetentionRuns:   retentionRuns,
	}

	if err := h.db.UpdateJob(job); err != nil {
		http.Error(w, "Fehler beim Aktualisieren: "+err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *WebHandler) handleWebJobDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		id = r.FormValue("id")
	}
	if err := h.db.DeleteJob(id); err != nil {
		http.Error(w, "Fehler beim Löschen: "+err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *WebHandler) handleWebServerCreate(w http.ResponseWriter, r *http.Request) {
	jobsDir := strings.TrimSpace(r.FormValue("jobs_dir"))
	if jobsDir == "" {
		jobsDir = "/opt/talend/jobs"
	}
	scriptsDir := strings.TrimSpace(r.FormValue("scripts_dir"))
	if scriptsDir == "" {
		scriptsDir = "/opt/talend/scripts"
	}
	server := &db.Server{
		ID:         r.FormValue("id"),
		Name:       r.FormValue("name"),
		Host:       r.FormValue("host"),
		User:       r.FormValue("user"),
		SSHKeyPath: r.FormValue("ssh_key_path"),
		JobsDir:    jobsDir,
		ScriptsDir: scriptsDir,
	}
	var port int
	if _, err := fmt.Sscanf(r.FormValue("port"), "%d", &port); err == nil && port > 0 {
		server.Port = port
	}
	keepReleases := 3
	if _, err := fmt.Sscanf(r.FormValue("keep_releases"), "%d", &keepReleases); err != nil || keepReleases <= 0 {
		keepReleases = 3
	}
	server.KeepReleases = keepReleases

	if err := h.db.CreateServer(server); err != nil {
		http.Redirect(w, r, "/settings/servers?error="+url.QueryEscape("Fehler beim Anlegen: "+err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/settings/servers?success="+url.QueryEscape("Execution Server erfolgreich angelegt."), http.StatusSeeOther)
}

func (h *WebHandler) handleWebServerUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	jobsDir := strings.TrimSpace(r.FormValue("jobs_dir"))
	if jobsDir == "" {
		jobsDir = "/opt/talend/jobs"
	}
	scriptsDir := strings.TrimSpace(r.FormValue("scripts_dir"))
	if scriptsDir == "" {
		scriptsDir = "/opt/talend/scripts"
	}
	server := &db.Server{
		ID:         id,
		Name:       r.FormValue("name"),
		Host:       r.FormValue("host"),
		User:       r.FormValue("user"),
		SSHKeyPath: r.FormValue("ssh_key_path"),
		JobsDir:    jobsDir,
		ScriptsDir: scriptsDir,
	}
	var port int
	if _, err := fmt.Sscanf(r.FormValue("port"), "%d", &port); err == nil && port > 0 {
		server.Port = port
	} else {
		server.Port = 22
	}
	keepReleases := 3
	if _, err := fmt.Sscanf(r.FormValue("keep_releases"), "%d", &keepReleases); err != nil || keepReleases <= 0 {
		keepReleases = 3
	}
	server.KeepReleases = keepReleases

	if err := h.db.UpdateServer(server); err != nil {
		http.Redirect(w, r, "/settings/servers?error="+url.QueryEscape("Fehler beim Aktualisieren: "+err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/settings/servers?success="+url.QueryEscape("Execution Server erfolgreich aktualisiert."), http.StatusSeeOther)
}

func (h *WebHandler) handleWebServerDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	targetServerID := strings.TrimSpace(r.FormValue("target_server_id"))

	jobs, err := h.db.GetJobsByServerID(id)
	if err != nil {
		http.Redirect(w, r, "/settings/servers?error="+url.QueryEscape("Fehler beim Abrufen der Jobs: "+err.Error()), http.StatusSeeOther)
		return
	}

	if len(jobs) > 0 {
		if targetServerID == "" {
			http.Redirect(w, r, "/settings/servers?error="+url.QueryEscape(fmt.Sprintf("Server wird noch von %d Job(s) verwendet. Bitte wählen Sie einen alternativen Zielserver aus.", len(jobs))), http.StatusSeeOther)
			return
		}
		if err := h.db.DeleteServerWithJobReassignment(id, targetServerID); err != nil {
			http.Redirect(w, r, "/settings/servers?error="+url.QueryEscape("Fehler bei der Job-Migration / Löschung: "+err.Error()), http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/settings/servers?success="+url.QueryEscape(fmt.Sprintf("Execution Server erfolgreich gelöscht und %d Job(s) auf Zielserver umgestellt.", len(jobs))), http.StatusSeeOther)
		return
	}

	if err := h.db.DeleteServer(id); err != nil {
		http.Redirect(w, r, "/settings/servers?error="+url.QueryEscape("Fehler beim Löschen: "+err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/settings/servers?success="+url.QueryEscape("Execution Server erfolgreich gelöscht."), http.StatusSeeOther)
}

func (h *WebHandler) handleWebUserCreate(w http.ResponseWriter, r *http.Request) {
	password := r.FormValue("password")
	hash, err := auth.HashPassword(password)
	if err != nil {
		http.Error(w, "Passwort-Hash fehlgeschlagen", http.StatusInternalServerError)
		return
	}

	user := &db.User{
		ID:           uuid.New().String(),
		Username:     r.FormValue("username"),
		PasswordHash: hash,
		DisplayName:  r.FormValue("display_name"),
		Email:        r.FormValue("email"),
		Role:         r.FormValue("role"),
		AuthSource:   "local",
		IsActive:     true,
	}

	if err := h.db.CreateUser(user); err != nil {
		http.Error(w, "Fehler beim Anlegen: "+err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/settings/users", http.StatusSeeOther)
}

func (h *WebHandler) handleWebUserPassword(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	password := r.FormValue("password")
	hash, err := auth.HashPassword(password)
	if err != nil {
		http.Error(w, "Passwort-Hash fehlgeschlagen", http.StatusInternalServerError)
		return
	}

	if err := h.db.UpdateUserPassword(id, hash); err != nil {
		http.Error(w, "Fehler beim Aktualisieren: "+err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/settings/users", http.StatusSeeOther)
}

func (h *WebHandler) handleWebTokenCreate(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	role := r.FormValue("role")

	rawSecret, err := auth.GenerateRandomPassword(32)
	if err != nil {
		http.Error(w, "Token-Generierung fehlgeschlagen", http.StatusInternalServerError)
		return
	}

	fullToken := fmt.Sprintf("jobcon_%s", rawSecret)
	hash := sha256.Sum256([]byte(fullToken))
	tokenHash := hex.EncodeToString(hash[:])

	token := &db.APIToken{
		ID:        uuid.New().String(),
		Name:      name,
		TokenHash: tokenHash,
		Role:      role,
	}

	if err := h.db.CreateAPIToken(token); err != nil {
		http.Error(w, "Fehler beim Anlegen: "+err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/settings/system?token="+fullToken, http.StatusSeeOther)
}

func (h *WebHandler) handleWebTokenDelete(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("id")
	_ = h.db.DeleteAPIToken(id)
	http.Redirect(w, r, "/settings/system", http.StatusSeeOther)
}

func (h *WebHandler) handleWebSettingsNexus(w http.ResponseWriter, r *http.Request) {
	baseURL := strings.TrimSpace(r.FormValue("nexus_base_url"))
	nexusUser := strings.TrimSpace(r.FormValue("nexus_username"))
	nexusPass := r.FormValue("nexus_password")
	isAnon := r.FormValue("nexus_anonymous") == "true" || r.FormValue("nexus_anonymous") == "on"

	if baseURL == "" {
		http.Redirect(w, r, "/settings/system?error=Nexus+Basis-URL+darf+nicht+leer+sein", http.StatusSeeOther)
		return
	}

	_ = h.db.SetSetting("nexus_base_url", baseURL)
	if isAnon {
		_ = h.db.SetSetting("nexus_anonymous", "true")
		_ = h.db.SetSetting("nexus_username", "")
		_ = h.db.SetSetting("nexus_password", "")
	} else {
		_ = h.db.SetSetting("nexus_anonymous", "false")
		_ = h.db.SetSetting("nexus_username", nexusUser)
		if strings.TrimSpace(nexusPass) != "" {
			_ = h.db.SetSetting("nexus_password", nexusPass)
		}
	}

	http.Redirect(w, r, "/settings/system?success=Nexus-Einstellungen+erfolgreich+gespeichert", http.StatusSeeOther)
}

func (h *WebHandler) handleWebNexusRepoAdd(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.FormValue("repo_id"))
	label := strings.TrimSpace(r.FormValue("repo_label"))
	if id == "" {
		http.Redirect(w, r, "/settings/system?error=Repository-ID+darf+nicht+leer+sein", http.StatusSeeOther)
		return
	}
	if label == "" {
		label = id
	}

	repos := h.getNexusRepositories()
	exists := false
	for i, repo := range repos {
		if repo.ID == id {
			repos[i].Label = label
			exists = true
			break
		}
	}
	if !exists {
		repos = append(repos, config.NexusRepository{ID: id, Label: label})
	}

	bytes, _ := json.Marshal(repos)
	_ = h.db.SetSetting("nexus_repositories", string(bytes))

	http.Redirect(w, r, "/settings/system?success=Repository+erfolgreich+hinzugefügt", http.StatusSeeOther)
}

func (h *WebHandler) handleWebNexusRepoDelete(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.FormValue("repo_id"))
	repos := h.getNexusRepositories()
	var updated []config.NexusRepository
	for _, repo := range repos {
		if repo.ID != id {
			updated = append(updated, repo)
		}
	}
	bytes, _ := json.Marshal(updated)
	_ = h.db.SetSetting("nexus_repositories", string(bytes))

	http.Redirect(w, r, "/settings/system?success=Repository+erfolgreich+entfernt", http.StatusSeeOther)
}

func (h *WebHandler) handleWebSettingsCleanRetention(w http.ResponseWriter, r *http.Request) {
	purged := h.storage.CleanAllRetention(h.db)
	msg := fmt.Sprintf("%d+alte+Logs+erfolgreich+bereinigt", purged)
	http.Redirect(w, r, "/settings/system?success="+msg, http.StatusSeeOther)
}

func (h *WebHandler) getNexusRepositories() []config.NexusRepository {
	reposJSON, err := h.db.GetSetting("nexus_repositories", "")
	if err == nil && reposJSON != "" {
		var repos []config.NexusRepository
		if err := json.Unmarshal([]byte(reposJSON), &repos); err == nil && len(repos) > 0 {
			return repos
		}
	}
	return h.cfg.Nexus.Repositories
}
