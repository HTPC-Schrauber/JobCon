package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"jobcon/internal/auth"
	"jobcon/internal/config"
	"jobcon/internal/db"
	"jobcon/internal/i18n"
	"jobcon/internal/nexus"
	"jobcon/internal/runner"
	"jobcon/internal/services"
	"jobcon/internal/storage"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var safeIdentifierRe = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

//go:embed templates/* static/*
var contentFS embed.FS

type WebHandler struct {
	db            *db.DB
	runner        *runner.ExecutionManager
	jobSvc        *services.JobService
	userSvc       *services.UserService
	serverSvc     *services.ServerService
	execSvc       *services.ExecutionService
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
	jobSvc := services.NewJobService(database, runner)
	userSvc := services.NewUserService(database)
	serverSvc := services.NewServerService(database)
	execSvc := services.NewExecutionService(database, runner)

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
		jobSvc:        jobSvc,
		userSvc:       userSvc,
		serverSvc:     serverSvc,
		execSvc:       execSvc,
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

// -----------------------------------------------------------------------------
// FORM ACTIONS
// -----------------------------------------------------------------------------

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
