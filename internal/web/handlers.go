package web

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"jobcon/internal/auth"
	"jobcon/internal/config"
	"jobcon/internal/db"
	"jobcon/internal/i18n"
	"jobcon/internal/nexus"
	"jobcon/internal/runner"
	"jobcon/internal/storage"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

var safeIdentifierRe = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

//go:embed templates/* static/*
var contentFS embed.FS

type WebHandler struct {
	db            *db.DB
	runner        *runner.ExecutionManager
	storage       *storage.LogStorage
	authMW        *auth.Middleware
	authenticator auth.Authenticator
	ldapService   *auth.LDAPService
	sessions      *auth.SessionManager
	cfg           *config.Config
	templates     map[string]*template.Template
	i18nMgr       *i18n.Manager
	syncer        *nexus.Syncer
}

func formatTimeAgo(t *time.Time) string {
	if t == nil {
		return "noch nie"
	}
	d := time.Since(*t)
	if d < 1*time.Minute {
		return "gerade eben"
	} else if d < 60*time.Minute {
		return fmt.Sprintf("vor %d Min.", int(d.Minutes()))
	} else if d < 24*time.Hour {
		return fmt.Sprintf("vor %d Std.", int(d.Hours()))
	} else {
		return fmt.Sprintf("vor %d Tagen", int(d.Hours()/24))
	}
}

func NewWebHandler(
	database *db.DB,
	runner *runner.ExecutionManager,
	logStorage *storage.LogStorage,
	authMW *auth.Middleware,
	authenticator auth.Authenticator,
	sessions *auth.SessionManager,
	cfg *config.Config,
	syncer ...*nexus.Syncer,
) (*WebHandler, error) {
	templates := make(map[string]*template.Template)

	funcMap := template.FuncMap{
		"add":       func(a, b int) int { return a + b },
		"sub":       func(a, b int) int { return a - b },
		"mul":       func(a, b int) int { return a * b },
		"div":       func(a, b int) int { return a / b },
		"contains":  strings.Contains,
		"hasPrefix": strings.HasPrefix,
		"timeAgo":   func(t *time.Time) string { return formatTimeAgo(t) },
	}

	for _, tmpl := range []string{
		"dashboard.html",
		"execution.html",
		"executions_page.html",
		"settings_servers.html",
		"settings_users.html",
		"settings_ldap.html",
		"settings_system.html",
	} {
		t, err := template.New(tmpl).Funcs(funcMap).ParseFS(contentFS, "templates/layout.html", "templates/"+tmpl)
		if err != nil {
			return nil, fmt.Errorf("failed to parse template %q: %w", tmpl, err)
		}
		templates[tmpl] = t
	}

	loginTmpl, err := template.New("login.html").Funcs(funcMap).ParseFS(contentFS, "templates/login.html")
	if err != nil {
		return nil, fmt.Errorf("failed to parse template login.html: %w", err)
	}
	templates["login.html"] = loginTmpl

	var nexusSyncer *nexus.Syncer
	if len(syncer) > 0 {
		nexusSyncer = syncer[0]
	}

	var ldapSvc *auth.LDAPService
	if ma, ok := authenticator.(*auth.MultiAuthenticator); ok {
		ldapSvc = ma.LDAPService()
	} else {
		ldapSvc = auth.NewLDAPService(database, cfg)
	}

	return &WebHandler{
		db:            database,
		runner:        runner,
		storage:       logStorage,
		authMW:        authMW,
		authenticator: authenticator,
		ldapService:   ldapSvc,
		sessions:      sessions,
		cfg:           cfg,
		templates:     templates,
		syncer:        nexusSyncer,
		i18nMgr: func() *i18n.Manager {
			if cfg != nil && (cfg.I18n.LocalesDir != "" || cfg.I18n.DefaultLanguage != "") {
				mgr := i18n.NewManager(cfg.I18n.LocalesDir)
				if cfg.I18n.DefaultLanguage != "" {
					mgr.SetDefaultLanguage(cfg.I18n.DefaultLanguage)
				}
				return mgr
			}
			return i18n.GetDefaultManager()
		}(),
	}, nil
}

func (h *WebHandler) getRequestLang(r *http.Request) string {
	defaultLang := "en"
	if h.cfg != nil && h.cfg.I18n.DefaultLanguage != "" {
		defaultLang = h.cfg.I18n.DefaultLanguage
	}
	if r == nil {
		return defaultLang
	}
	if u := auth.UserFromContext(r.Context()); u != nil {
		if lang, err := h.db.GetUserPreference(u.ID, "language", ""); err == nil && lang != "" {
			return lang
		}
	}
	if cookie, err := r.Cookie("jobcon_lang"); err == nil && cookie.Value != "" {
		return cookie.Value
	}
	accept := r.Header.Get("Accept-Language")
	if strings.HasPrefix(strings.ToLower(accept), "de") {
		return "de"
	}
	return defaultLang
}

func (h *WebHandler) render(w http.ResponseWriter, r *http.Request, page string, data map[string]any) {
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

	if data == nil {
		data = make(map[string]any)
	}

	lang := h.getRequestLang(r)
	data["EnvName"] = h.cfg.Environment.Name
	data["EnvColor"] = h.cfg.Environment.EffectiveColor()
	data["EnvTextColor"] = h.cfg.Environment.EffectiveTextColor()
	data["CurrentLang"] = lang
	data["Languages"] = h.i18nMgr.GetAvailableLanguages()
	data["I18nJSON"] = template.JS(h.i18nMgr.GetJSON(lang))
	data["I18n"] = h.i18nMgr.GetLocalizer(lang)
	data["T"] = func(key string, args ...any) string {
		return h.i18nMgr.T(lang, key, args...)
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

	// Auth & Language routes
	mux.HandleFunc("GET /login", h.handleLoginPage)
	mux.HandleFunc("POST /login", h.handleLoginSubmit)
	mux.HandleFunc("POST /logout", h.handleLogout)
	mux.HandleFunc("POST /web/set-language", h.handleSetLanguage)
	mux.HandleFunc("GET /web/set-language", h.handleSetLanguage)

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
	mux.Handle("GET /settings/ldap", adminWrap(h.handleSettingsLDAP))
	mux.Handle("GET /settings/system", adminWrap(h.handleSettingsSystem))

	// Form actions - Jobs
	mux.Handle("POST /web/jobs/run", authWrap(h.handleWebJobRun))
	mux.Handle("POST /web/jobs/deploy", authWrap(h.handleWebJobDeploy))
	mux.Handle("POST /web/jobs/{id}/undeploy", authWrap(h.handleWebJobUndeploy))
	mux.Handle("POST /web/jobs/bulk/run", authWrap(h.handleWebBulkRun))
	mux.Handle("POST /web/jobs/bulk/deploy", authWrap(h.handleWebBulkDeploy))
	mux.Handle("POST /web/jobs/bulk/undeploy", authWrap(h.handleWebBulkUndeploy))
	mux.Handle("POST /web/jobs/create", adminWrap(h.handleWebJobCreate))
	mux.Handle("POST /web/jobs/{id}/update", adminWrap(h.handleWebJobUpdate))
	mux.Handle("POST /web/jobs/{id}/delete", adminWrap(h.handleWebJobDelete))

	// Form actions - Servers, Users, Tokens, LDAP
	mux.Handle("POST /web/servers/create", adminWrap(h.handleWebServerCreate))
	mux.Handle("POST /web/servers/{id}/update", adminWrap(h.handleWebServerUpdate))
	mux.Handle("POST /web/servers/{id}/delete", adminWrap(h.handleWebServerDelete))
	mux.Handle("POST /web/users/create", adminWrap(h.handleWebUserCreate))
	mux.Handle("POST /web/users/{id}/update", adminWrap(h.handleWebUserUpdate))
	mux.Handle("POST /web/users/{id}/delete", adminWrap(h.handleWebUserDelete))
	mux.Handle("POST /web/users/{id}/password", adminWrap(h.handleWebUserPassword))
	mux.Handle("GET /web/users/ldap-lookup", adminWrap(h.handleWebUserLDAPLookup))
	mux.Handle("POST /web/tokens/create", adminWrap(h.handleWebTokenCreate))
	mux.Handle("POST /web/tokens/delete", adminWrap(h.handleWebTokenDelete))
	mux.Handle("POST /web/settings/ldap", adminWrap(h.handleWebSettingsLDAP))
	mux.Handle("POST /web/settings/ldap/test", adminWrap(h.handleWebSettingsLDAPTest))
	mux.Handle("POST /web/settings/ldap/test-user", adminWrap(h.handleWebSettingsLDAPTestUser))

	// Form actions - Settings (Nexus & Retention & Concurrency)
	mux.Handle("POST /web/settings/nexus", adminWrap(h.handleWebSettingsNexus))
	mux.Handle("POST /web/settings/nexus/repo/add", adminWrap(h.handleWebNexusRepoAdd))
	mux.Handle("POST /web/settings/nexus/repo/delete", adminWrap(h.handleWebNexusRepoDelete))
	mux.Handle("POST /web/settings/nexus/sync", adminWrap(h.handleWebNexusSync))
	mux.Handle("POST /web/settings/nexus/interval", adminWrap(h.handleWebNexusInterval))
	mux.Handle("POST /web/settings/concurrency", adminWrap(h.handleWebConcurrency))
	mux.Handle("POST /web/settings/retention/clean", adminWrap(h.handleWebSettingsCleanRetention))
}


// -----------------------------------------------------------------------------
// PAGES
// -----------------------------------------------------------------------------

// isCookieSecure determines if the Secure flag should be set on cookies.
// It returns true if native TLS is enabled, if the incoming request has TLS,
// if a reverse proxy reports HTTPS (via X-Forwarded-Proto or Forwarded),
// or if the configured BaseURL specifies HTTPS.
func (h *WebHandler) isCookieSecure(r *http.Request) bool {
	if h.cfg != nil {
		if h.cfg.TLS.Enabled {
			return true
		}
		if strings.HasPrefix(strings.ToLower(h.cfg.Server.BaseURL), "https://") {
			return true
		}
	}
	if r != nil {
		if r.TLS != nil {
			return true
		}
		if proto := r.Header.Get("X-Forwarded-Proto"); strings.EqualFold(proto, "https") {
			return true
		}
		if fwd := r.Header.Get("Forwarded"); strings.Contains(strings.ToLower(fwd), "proto=https") {
			return true
		}
	}
	return false
}

func (h *WebHandler) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if u := auth.UserFromContext(r.Context()); u != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	h.render(w, r, "login.html", map[string]any{
		"Error": r.URL.Query().Get("error"),
	})
}

func (h *WebHandler) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	username := r.FormValue("username")
	password := r.FormValue("password")

	user, err := h.authenticator.Authenticate(r.Context(), username, password)
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
		Secure:   h.isCookieSecure(r),
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
		Secure:   h.isCookieSecure(r),
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
	onlineServers, _ := h.db.CountOnlineServers()
	runningCount, _ := h.db.CountActiveExecutions()
	syncStatus := h.getNexusSyncStatus()
	defaultConcurrency := h.getMaxConcurrentJobs()

	data := map[string]any{
		"Title":                    "Talend Jobs",
		"CurrentTab":               "dashboard",
		"User":                     user,
		"Jobs":                     pagedResult.Jobs,
		"TotalCount":               pagedResult.TotalCount,
		"CurrentPage":              pagedResult.CurrentPage,
		"TotalPages":               pagedResult.TotalPages,
		"PageSize":                 pagedResult.PageSize,
		"Search":                   filter.Search,
		"GroupID":                  filter.GroupID,
		"ServerID":                 filter.ServerID,
		"SortBy":                   filter.SortBy,
		"SortOrder":                filter.SortOrder,
		"GroupBy":                  groupBy,
		"DistinctGroups":           distinctGroups,
		"Servers":                  servers,
		"OnlineServersCount":       onlineServers,
		"TotalServersCount":        len(servers),
		"RunningCount":             runningCount,
		"NexusRepos":               nexusRepos,
		"NexusSyncStatus":          syncStatus,
		"NexusSyncTimeAgo":         formatTimeAgo(syncStatus.LastSyncedAt),
		"DefaultMaxConcurrentJobs": defaultConcurrency,
	}
	h.render(w, r, "dashboard.html", data)
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
	h.render(w, r, "executions_page.html", data)
}

func (h *WebHandler) handleExecutionPage(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id := r.PathValue("id")
	exec, err := h.db.GetExecution(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	from := r.URL.Query().Get("from")
	if from == "" {
		ref := r.Header.Get("Referer")
		if strings.Contains(ref, "/executions") {
			from = "executions"
		} else {
			from = "jobs"
		}
	}

	backURL := "/"
	backLabelKey := "executions.back_to_jobs"
	if from == "executions" {
		backURL = "/executions"
		backLabelKey = "executions.back_to_executions"
	}

	data := map[string]any{
		"Title":        "Execution " + exec.ID,
		"CurrentTab":   "executions",
		"User":         user,
		"Exec":         exec,
		"From":         from,
		"BackURL":      backURL,
		"BackLabelKey": backLabelKey,
	}
	h.render(w, r, "execution.html", data)
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
	h.render(w, r, "settings_servers.html", data)
}

func (h *WebHandler) handleSettingsUsers(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	users, _ := h.db.ListUsers()
	activeLocalAdmins, _ := h.db.CountActiveLocalAdmins()

	data := map[string]any{
		"Title":             "Benutzerverwaltung",
		"CurrentTab":        "settings",
		"User":              user,
		"Users":             users,
		"ActiveLocalAdmins": activeLocalAdmins,
		"SuccessMsg":        r.URL.Query().Get("success"),
		"ErrorMsg":          r.URL.Query().Get("error"),
	}
	h.render(w, r, "settings_users.html", data)
}

func (h *WebHandler) handleSettingsLDAP(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	ldapCfg := h.ldapService.GetEffectiveConfig()

	data := map[string]any{
		"Title":           "LDAP / Active Directory",
		"CurrentTab":      "settings",
		"User":            user,
		"LDAPConfig":      ldapCfg,
		"HasBindPassword": ldapCfg.BindPassword != "",
		"SuccessMsg":      r.URL.Query().Get("success"),
		"ErrorMsg":        r.URL.Query().Get("error"),
	}
	h.render(w, r, "settings_ldap.html", data)
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
	syncStatus := h.getNexusSyncStatus()
	concurrency := h.getMaxConcurrentJobs()
	artifactCount, _ := h.db.GetNexusArtifactCount()

	data := map[string]any{
		"Title":              "System & Tokens",
		"CurrentTab":         "settings",
		"User":               user,
		"Tokens":             tokens,
		"NexusBaseURL":       baseURL,
		"NexusUser":          nexusUser,
		"NexusAnonymous":     isAnonymous,
		"HasPassword":        nexusPass != "",
		"NexusRepositories":  repos,
		"NexusSyncStatus":    syncStatus,
		"NexusSyncTimeAgo":   formatTimeAgo(syncStatus.LastSyncedAt),
		"NexusSyncInterval":  syncStatus.IntervalMinutes,
		"NexusArtifactCount": artifactCount,
		"MaxConcurrentJobs":  concurrency,
		"SuccessMsg":         r.URL.Query().Get("success"),
		"ErrorMsg":           r.URL.Query().Get("error"),
		"NewTokenRaw":        strings.TrimSpace(r.URL.Query().Get("token")),
	}
	h.render(w, r, "settings_system.html", data)
}


// -----------------------------------------------------------------------------
// FORM ACTIONS
// -----------------------------------------------------------------------------

func (h *WebHandler) handleSetLanguage(w http.ResponseWriter, r *http.Request) {
	lang := strings.TrimSpace(r.FormValue("lang"))
	if lang == "" {
		lang = r.URL.Query().Get("lang")
	}
	if lang == "" {
		lang = "en"
	}

	u := auth.UserFromContext(r.Context())
	if u == nil {
		u, _ = h.authMW.AuthenticateRequest(r)
	}
	if u != nil {
		_ = h.db.SetUserPreference(u.ID, "language", lang)
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "jobcon_lang",
		Value:    lang,
		Path:     "/",
		Secure:   h.isCookieSecure(r),
		MaxAge:   365 * 86400,
		SameSite: http.SameSiteLaxMode,
	})

	referer := r.Referer()
	if referer == "" {
		referer = "/"
	}
	http.Redirect(w, r, referer, http.StatusSeeOther)
}

func (h *WebHandler) handleWebJobRun(w http.ResponseWriter, r *http.Request) {
	jobID := r.FormValue("job_id")
	if !safeIdentifierRe.MatchString(jobID) {
		http.Error(w, "Ungültige Job-ID", http.StatusBadRequest)
		return
	}
	version := r.FormValue("version")
	contextName := r.FormValue("context")
	paramsRaw := r.FormValue("params")

	if version != "" && !safeIdentifierRe.MatchString(version) {
		http.Error(w, "Ungültiges Versionsformat", http.StatusBadRequest)
		return
	}
	if contextName != "" && !safeIdentifierRe.MatchString(contextName) {
		http.Error(w, "Ungültiger Kontextname", http.StatusBadRequest)
		return
	}

	params := make(map[string]string)
	lines := strings.Split(paramsRaw, "\n")
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" || !strings.Contains(l, "=") {
			continue
		}
		parts := strings.SplitN(l, "=", 2)
		k := strings.TrimSpace(parts[0])
		v := strings.TrimSpace(parts[1])
		if !safeIdentifierRe.MatchString(k) {
			http.Error(w, "Ungültiger Parameter-Schlüssel: "+k, http.StatusBadRequest)
			return
		}
		params[k] = v
	}

	user := auth.UserFromContext(r.Context())
	triggeredBy := "web:unknown"
	if user != nil {
		triggeredBy = "web:" + user.Username
	}

	exec, err := h.runner.StartExecution(r.Context(), jobID, "run", version, contextName, params, triggeredBy)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	http.Redirect(w, r, "/executions/"+exec.ID+"?from=jobs", http.StatusSeeOther)
}

func (h *WebHandler) handleWebJobDeploy(w http.ResponseWriter, r *http.Request) {
	jobID := r.FormValue("job_id")
	if !safeIdentifierRe.MatchString(jobID) {
		http.Error(w, "Ungültige Job-ID", http.StatusBadRequest)
		return
	}
	version := r.FormValue("version")

	if version != "" && !safeIdentifierRe.MatchString(version) {
		http.Error(w, "Ungültiges Versionsformat", http.StatusBadRequest)
		return
	}

	user := auth.UserFromContext(r.Context())
	triggeredBy := "web:unknown"
	if user != nil {
		triggeredBy = "web:" + user.Username
	}

	exec, err := h.runner.StartExecution(r.Context(), jobID, "deploy", version, "", nil, triggeredBy)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	http.Redirect(w, r, "/executions/"+exec.ID+"?from=jobs", http.StatusSeeOther)
}

func (h *WebHandler) handleWebJobUndeploy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		id = r.FormValue("job_id")
	}
	if !safeIdentifierRe.MatchString(id) {
		http.Error(w, "Ungültige Job-ID", http.StatusBadRequest)
		return
	}
	job, err := h.db.GetJob(id)
	if err != nil {
		http.Error(w, "Job nicht gefunden: "+err.Error(), http.StatusNotFound)
		return
	}

	user := auth.UserFromContext(r.Context())
	triggeredBy := "web:unknown"
	if user != nil {
		triggeredBy = "web:" + user.Username
	}

	exec, err := h.runner.StartExecution(r.Context(), job.ID, "undeploy", "", "", nil, triggeredBy)
	if err != nil {
		http.Error(w, "Fehler beim Undeploy: "+err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/executions/"+exec.ID+"?from=jobs", http.StatusSeeOther)
}

func (h *WebHandler) handleWebBulkRun(w http.ResponseWriter, r *http.Request) {
	jobIDs := parseJobIDs(r)
	if len(jobIDs) == 0 {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	user := auth.UserFromContext(r.Context())
	triggeredBy := "web:unknown"
	if user != nil {
		triggeredBy = "web:" + user.Username
	}

	concurrency := 0
	if cStr := r.FormValue("concurrency"); cStr != "" {
		if c, err := strconv.Atoi(cStr); err == nil && c > 0 {
			if c > runner.MaxBulkConcurrency {
				http.Error(w, fmt.Sprintf("Ungültige Parallelität: maximal %d erlaubt", runner.MaxBulkConcurrency), http.StatusBadRequest)
				return
			}
			concurrency = c
		}
	}

	_, err := h.runner.StartBulkRunQueue(r.Context(), jobIDs, concurrency, triggeredBy)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	http.Redirect(w, r, "/executions", http.StatusSeeOther)
}

func (h *WebHandler) handleWebBulkDeploy(w http.ResponseWriter, r *http.Request) {
	jobIDs := parseJobIDs(r)
	if len(jobIDs) == 0 {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	version := strings.TrimSpace(r.FormValue("version"))
	if version != "" && !safeIdentifierRe.MatchString(version) {
		http.Error(w, "Ungültiges Versionsformat", http.StatusBadRequest)
		return
	}

	user := auth.UserFromContext(r.Context())
	triggeredBy := "web:unknown"
	if user != nil {
		triggeredBy = "web:" + user.Username
	}

	for _, id := range jobIDs {
		job, err := h.db.GetJob(id)
		if err != nil {
			continue
		}
		targetVersion := version
		if targetVersion == "" {
			targetVersion = job.ActiveVersion
		}
		_, _ = h.runner.StartExecution(r.Context(), job.ID, "deploy", targetVersion, "", nil, triggeredBy)
	}

	http.Redirect(w, r, "/executions", http.StatusSeeOther)
}

func (h *WebHandler) handleWebBulkUndeploy(w http.ResponseWriter, r *http.Request) {
	jobIDs := parseJobIDs(r)
	if len(jobIDs) == 0 {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	user := auth.UserFromContext(r.Context())
	triggeredBy := "web:unknown"
	if user != nil {
		triggeredBy = "web:" + user.Username
	}

	for _, id := range jobIDs {
		job, err := h.db.GetJob(id)
		if err != nil {
			continue
		}
		_, _ = h.runner.StartExecution(r.Context(), job.ID, "undeploy", "", "", nil, triggeredBy)
	}

	http.Redirect(w, r, "/executions", http.StatusSeeOther)
}

func parseJobIDs(r *http.Request) []string {
	raw := r.FormValue("job_ids")
	if raw == "" {
		return r.Form["job_ids"]
	}
	parts := strings.Split(raw, ",")
	var ids []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			ids = append(ids, p)
		}
	}
	return ids
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
		EnvFile:         strings.TrimSpace(r.FormValue("env_file")),
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
		EnvFile:         strings.TrimSpace(r.FormValue("env_file")),
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
	undeployVal := strings.ToLower(r.FormValue("undeploy_server"))
	if undeployVal == "true" || undeployVal == "1" || undeployVal == "yes" {
		job, err := h.db.GetJob(id)
		if err == nil && job != nil && job.IsDeployed {
			_ = h.runner.UndeployJobSync(r.Context(), id)
		}
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
		EnvFile:    strings.TrimSpace(r.FormValue("env_file")),
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
		EnvFile:    strings.TrimSpace(r.FormValue("env_file")),
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
	authSource := strings.TrimSpace(r.FormValue("auth_source"))
	if authSource == "" {
		authSource = "local"
	}
	username := strings.TrimSpace(r.FormValue("username"))
	displayName := strings.TrimSpace(r.FormValue("display_name"))
	email := strings.TrimSpace(r.FormValue("email"))
	role := strings.TrimSpace(r.FormValue("role"))
	if role == "" {
		role = auth.RoleViewer
	}

	if username == "" {
		http.Redirect(w, r, "/settings/users?error="+url.QueryEscape("Benutzername darf nicht leer sein"), http.StatusSeeOther)
		return
	}

	var hash string
	if authSource == "local" {
		password := r.FormValue("password")
		if password == "" {
			http.Redirect(w, r, "/settings/users?error="+url.QueryEscape("Passwort für lokalen Benutzer erforderlich"), http.StatusSeeOther)
			return
		}
		var err error
		hash, err = auth.HashPassword(password)
		if err != nil {
			http.Redirect(w, r, "/settings/users?error="+url.QueryEscape("Passwort-Hash fehlgeschlagen"), http.StatusSeeOther)
			return
		}
	} else {
		hash = ""
		// If display name is empty, attempt to resolve via LDAP lookup
		if displayName == "" {
			if info, err := h.ldapService.LookupUser(username); err == nil && info != nil {
				displayName = info.DisplayName
				if email == "" {
					email = info.Email
				}
			}
		}
		if displayName == "" {
			displayName = username
		}
	}

	user := &db.User{
		ID:           uuid.New().String(),
		Username:     username,
		PasswordHash: hash,
		DisplayName:  displayName,
		Email:        email,
		Role:         role,
		AuthSource:   authSource,
		IsActive:     true,
	}

	if err := h.db.CreateUser(user); err != nil {
		http.Redirect(w, r, "/settings/users?error="+url.QueryEscape("Fehler beim Anlegen: "+err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/settings/users?success="+url.QueryEscape("Benutzer erfolgreich angelegt."), http.StatusSeeOther)
}

func (h *WebHandler) handleWebUserUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	user, err := h.db.GetUserByID(id)
	if err != nil {
		http.Redirect(w, r, "/settings/users?error="+url.QueryEscape("Benutzer nicht gefunden"), http.StatusSeeOther)
		return
	}

	displayName := strings.TrimSpace(r.FormValue("display_name"))
	email := strings.TrimSpace(r.FormValue("email"))
	role := strings.TrimSpace(r.FormValue("role"))
	isActiveStr := r.FormValue("is_active")

	if displayName != "" {
		user.DisplayName = displayName
	}
	user.Email = email
	if role != "" {
		user.Role = role
	}
	if isActiveStr != "" {
		user.IsActive = (isActiveStr == "true" || isActiveStr == "1")
	}

	if err := h.db.UpdateUser(user); err != nil {
		if errors.Is(err, db.ErrLastAdminProtection) {
			http.Redirect(w, r, "/settings/users?error="+url.QueryEscape("Der letzte aktive lokale Administrator darf nicht deaktiviert oder herabgestuft werden, um ein Aussperren zu verhindern."), http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/settings/users?error="+url.QueryEscape("Fehler beim Aktualisieren: "+err.Error()), http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/settings/users?success="+url.QueryEscape("Benutzer erfolgreich aktualisiert."), http.StatusSeeOther)
}

func (h *WebHandler) handleWebUserDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.db.DeleteUser(id); err != nil {
		if errors.Is(err, db.ErrLastAdminProtection) {
			http.Redirect(w, r, "/settings/users?error="+url.QueryEscape("Der letzte aktive lokale Administrator darf nicht gelöscht werden, um ein Aussperren zu verhindern."), http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/settings/users?error="+url.QueryEscape("Fehler beim Löschen: "+err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/settings/users?success="+url.QueryEscape("Benutzer erfolgreich gelöscht."), http.StatusSeeOther)
}

func (h *WebHandler) handleWebUserPassword(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	password := r.FormValue("password")
	if password == "" {
		http.Redirect(w, r, "/settings/users?error="+url.QueryEscape("Passwort darf nicht leer sein"), http.StatusSeeOther)
		return
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		http.Redirect(w, r, "/settings/users?error="+url.QueryEscape("Passwort-Hash fehlgeschlagen"), http.StatusSeeOther)
		return
	}

	if err := h.db.UpdateUserPassword(id, hash); err != nil {
		http.Redirect(w, r, "/settings/users?error="+url.QueryEscape("Fehler beim Aktualisieren: "+err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/settings/users?success="+url.QueryEscape("Passwort erfolgreich aktualisiert."), http.StatusSeeOther)
}

func (h *WebHandler) handleWebUserLDAPLookup(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.URL.Query().Get("username"))
	w.Header().Set("Content-Type", "application/json")
	if username == "" {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "Bitte Loginnamen angeben"})
		return
	}

	info, err := h.ldapService.LookupUser(username)
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "user": info})
}

func (h *WebHandler) handleWebSettingsLDAP(w http.ResponseWriter, r *http.Request) {
	enabled := r.FormValue("ldap_enabled") == "true" || r.FormValue("ldap_enabled") == "on"
	proto := r.FormValue("ldap_protocol")
	useSSL := proto == "ldaps"
	host := strings.TrimSpace(r.FormValue("ldap_host"))
	portStr := strings.TrimSpace(r.FormValue("ldap_port"))
	insecure := r.FormValue("ldap_insecure_skip_verify") == "true" || r.FormValue("ldap_insecure_skip_verify") == "on"
	bindDN := strings.TrimSpace(r.FormValue("ldap_bind_dn"))
	bindPass := r.FormValue("ldap_bind_password")
	userBindTpl := strings.TrimSpace(r.FormValue("ldap_user_bind_template"))
	baseDN := strings.TrimSpace(r.FormValue("ldap_base_dn"))
	userFilter := strings.TrimSpace(r.FormValue("ldap_user_filter"))
	attrUser := strings.TrimSpace(r.FormValue("ldap_attr_username"))
	attrFirst := strings.TrimSpace(r.FormValue("ldap_attr_first_name"))
	attrLast := strings.TrimSpace(r.FormValue("ldap_attr_last_name"))
	attrMail := strings.TrimSpace(r.FormValue("ldap_attr_email"))

	if attrUser == "" {
		attrUser = "sAMAccountName"
	}
	if attrFirst == "" {
		attrFirst = "givenName"
	}
	if attrLast == "" {
		attrLast = "sn"
	}
	if attrMail == "" {
		attrMail = "mail"
	}

	if enabled {
		_ = h.db.SetSetting("ldap_enabled", "true")
	} else {
		_ = h.db.SetSetting("ldap_enabled", "false")
	}

	_ = h.db.SetSetting("ldap_host", host)
	_ = h.db.SetSetting("ldap_port", portStr)
	if useSSL {
		_ = h.db.SetSetting("ldap_use_ssl", "true")
	} else {
		_ = h.db.SetSetting("ldap_use_ssl", "false")
	}
	if insecure {
		_ = h.db.SetSetting("ldap_insecure_skip_verify", "true")
	} else {
		_ = h.db.SetSetting("ldap_insecure_skip_verify", "false")
	}

	_ = h.db.SetSetting("ldap_bind_dn", bindDN)
	if strings.TrimSpace(bindPass) != "" {
		_ = h.db.SetSetting("ldap_bind_password", bindPass)
	}
	_ = h.db.SetSetting("ldap_user_bind_template", userBindTpl)
	_ = h.db.SetSetting("ldap_base_dn", baseDN)
	_ = h.db.SetSetting("ldap_user_filter", userFilter)
	_ = h.db.SetSetting("ldap_attr_username", attrUser)
	_ = h.db.SetSetting("ldap_attr_first_name", attrFirst)
	_ = h.db.SetSetting("ldap_attr_last_name", attrLast)
	_ = h.db.SetSetting("ldap_attr_email", attrMail)

	http.Redirect(w, r, "/settings/ldap?success="+url.QueryEscape("LDAP / Active Directory Einstellungen erfolgreich gespeichert."), http.StatusSeeOther)
}

func (h *WebHandler) handleWebSettingsLDAPTest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	proto := r.FormValue("ldap_protocol")
	useSSL := proto == "ldaps"
	host := strings.TrimSpace(r.FormValue("ldap_host"))
	portStr := strings.TrimSpace(r.FormValue("ldap_port"))
	port, _ := strconv.Atoi(portStr)
	if port <= 0 {
		if useSSL {
			port = 636
		} else {
			port = 389
		}
	}
	insecure := r.FormValue("ldap_insecure_skip_verify") == "true" || r.FormValue("ldap_insecure_skip_verify") == "on"
	bindDN := strings.TrimSpace(r.FormValue("ldap_bind_dn"))
	bindPass := r.FormValue("ldap_bind_password")
	if bindPass == "" {
		// Use existing password if not re-entered
		existingCfg := h.ldapService.GetEffectiveConfig()
		bindPass = existingCfg.BindPassword
	}
	baseDN := strings.TrimSpace(r.FormValue("ldap_base_dn"))

	testCfg := config.LDAPConfig{
		Host:               host,
		Port:               port,
		UseSSL:             useSSL,
		InsecureSkipVerify: insecure,
		BindDN:             bindDN,
		BindPassword:       bindPass,
		BaseDN:             baseDN,
	}

	if err := h.ldapService.TestConnection(testCfg); err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
		return
	}

	msg := fmt.Sprintf("Verbindung zu %s:%d erfolgreich aufgebaut", host, port)
	if bindDN != "" {
		msg += " und Service Account gebunden."
	} else {
		msg += " (TCP/TLS Handshake erfolgreich)."
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "message": msg})
}

func (h *WebHandler) handleWebSettingsLDAPTestUser(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	username := strings.TrimSpace(r.FormValue("test_username"))
	password := r.FormValue("test_password")

	if username == "" {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "Bitte Test-Benutzername eingeben"})
		return
	}

	var info *auth.LDAPUserInfo
	var err error

	if password != "" {
		// Test direct or service bind with password
		info, err = h.ldapService.AuthenticateLDAP(username, password)
	} else {
		// Lookup without password
		info, err = h.ldapService.LookupUser(username)
	}

	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "user": info})
}

func (h *WebHandler) handleWebTokenCreate(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	role := r.FormValue("role")

	fullToken, err := auth.GenerateAPIToken(40)
	if err != nil {
		http.Error(w, "Token-Generierung fehlgeschlagen", http.StatusInternalServerError)
		return
	}

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

	http.Redirect(w, r, "/settings/system?token="+url.QueryEscape(fullToken), http.StatusSeeOther)
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
	if !nexus.SafeNexusURLRegex.MatchString(baseURL) {
		http.Redirect(w, r, "/settings/system?error=Ungültige+Nexus+Basis-URL.+Erlaubt+sind+nur+HTTP-+oder+HTTPS-URLs.", http.StatusSeeOther)
		return
	}
	if err := nexus.ValidateNexusURL(baseURL); err != nil {
		http.Redirect(w, r, "/settings/system?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
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

func (h *WebHandler) getMaxConcurrentJobs() int {
	cStr, err := h.db.GetSetting("max_concurrent_jobs", strconv.Itoa(runner.DefaultBulkConcurrency))
	if err == nil && cStr != "" {
		if c, err2 := strconv.Atoi(cStr); err2 == nil && c > 0 && c <= runner.MaxBulkConcurrency {
			return c
		}
	}
	return runner.DefaultBulkConcurrency
}

func (h *WebHandler) getNexusSyncStatus() nexus.SyncStatus {
	if h.syncer != nil {
		return h.syncer.GetStatus()
	}
	return nexus.SyncStatus{Status: "idle", IntervalMinutes: 60}
}

func (h *WebHandler) handleWebNexusSync(w http.ResponseWriter, r *http.Request) {
	if h.syncer != nil {
		go func() {
			_ = h.syncer.Sync(context.Background())
		}()
	}
	referer := r.Referer()
	if referer == "" {
		referer = "/settings/system"
	}
	http.Redirect(w, r, referer, http.StatusSeeOther)
}

func (h *WebHandler) handleWebNexusInterval(w http.ResponseWriter, r *http.Request) {
	intvStr := strings.TrimSpace(r.FormValue("interval_minutes"))
	if intvStr == "" {
		intvStr = strings.TrimSpace(r.FormValue("interval"))
	}
	if intv, err := strconv.Atoi(intvStr); err == nil && intv >= 0 {
		if h.syncer != nil {
			_ = h.syncer.SetIntervalMinutes(intv)
		} else {
			_ = h.db.SetSetting("nexus_sync_interval_minutes", strconv.Itoa(intv))
		}
	}
	http.Redirect(w, r, "/settings/system?success=Intervall+erfolgreich+gespeichert", http.StatusSeeOther)
}

func (h *WebHandler) handleWebConcurrency(w http.ResponseWriter, r *http.Request) {
	conStr := strings.TrimSpace(r.FormValue("max_concurrent_jobs"))
	if c, err := strconv.Atoi(conStr); err == nil && c > 0 && c <= runner.MaxBulkConcurrency {
		_ = h.db.SetSetting("max_concurrent_jobs", strconv.Itoa(c))
	}
	http.Redirect(w, r, "/settings/system?success=Gleichzeitige+Jobs+erfolgreich+gespeichert", http.StatusSeeOther)
}

