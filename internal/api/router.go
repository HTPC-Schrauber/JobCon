package api

import (
	"encoding/json"
	"jobcon/internal/auth"
	"jobcon/internal/config"
	"jobcon/internal/db"
	"jobcon/internal/nexus"
	"jobcon/internal/runner"
	"jobcon/internal/storage"
	"log"
	"net/http"
)

type API struct {
	db       *db.DB
	storage  *storage.LogStorage
	runner   *runner.ExecutionManager
	ssh      *runner.SSHRunner
	authMW   *auth.Middleware
	cfg      *config.Config
	syncer   *nexus.Syncer
}

func NewAPI(database *db.DB, storage *storage.LogStorage, runner *runner.ExecutionManager, ssh *runner.SSHRunner, authMW *auth.Middleware, cfg *config.Config, syncer *nexus.Syncer) *API {
	if ssh != nil && database != nil {
		ssh.SetDB(database)
	}
	return &API{
		db:      database,
		storage: storage,
		runner:  runner,
		ssh:     ssh,
		authMW:  authMW,
		cfg:     cfg,
		syncer:  syncer,
	}
}

// RegisterRoutes sets up all API endpoints on the provided mux
func (a *API) RegisterRoutes(mux *http.ServeMux) {
	// Health check (unauthenticated)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		a.jsonResponse(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// Helper wrappers for auth & roles
	authWrap := func(h http.HandlerFunc) http.Handler {
		return a.authMW.RequireAuth(h)
	}
	roleWrap := func(role string, h http.HandlerFunc) http.Handler {
		return a.authMW.RequireAuth(a.authMW.RequireRole(role)(h))
	}

	// Jobs
	mux.Handle("GET /api/v1/jobs", authWrap(a.handleListJobs))
	mux.Handle("POST /api/v1/jobs", roleWrap(auth.RoleAdmin, a.handleCreateJob))
	mux.Handle("GET /api/v1/jobs/{id}", authWrap(a.handleGetJob))
	mux.Handle("PUT /api/v1/jobs/{id}", roleWrap(auth.RoleAdmin, a.handleUpdateJob))
	mux.Handle("DELETE /api/v1/jobs/{id}", roleWrap(auth.RoleAdmin, a.handleDeleteJob))
	mux.Handle("GET /api/v1/jobs/{id}/artifact", authWrap(a.handleGetJobArtifact))
	mux.Handle("POST /api/v1/jobs/{id}/deploy", roleWrap(auth.RoleOperator, a.handleDeployJob))
	mux.Handle("POST /api/v1/jobs/{id}/undeploy", roleWrap(auth.RoleOperator, a.handleUndeployJob))
	mux.Handle("POST /api/v1/jobs/{id}/run", roleWrap(auth.RoleOperator, a.handleRunJob))
	mux.Handle("POST /api/v1/jobs/bulk/run", roleWrap(auth.RoleOperator, a.handleBulkRunJobs))
	mux.Handle("POST /api/v1/jobs/bulk/deploy", roleWrap(auth.RoleOperator, a.handleBulkDeployJobs))
	mux.Handle("POST /api/v1/jobs/bulk/undeploy", roleWrap(auth.RoleOperator, a.handleBulkUndeployJobs))

	// Executions
	mux.Handle("GET /api/v1/executions", authWrap(a.handleListExecutions))
	mux.Handle("GET /api/v1/executions/{id}", authWrap(a.handleGetExecution))
	mux.Handle("GET /api/v1/executions/{id}/logs", authWrap(a.handleGetExecutionLogs))
	mux.Handle("POST /api/v1/executions/{id}/abort", roleWrap(auth.RoleOperator, a.handleAbortExecution))

	// Servers
	mux.Handle("GET /api/v1/servers", roleWrap(auth.RoleAdmin, a.handleListServers))
	mux.Handle("POST /api/v1/servers", roleWrap(auth.RoleAdmin, a.handleCreateServer))
	mux.Handle("GET /api/v1/servers/{id}", roleWrap(auth.RoleAdmin, a.handleGetServer))
	mux.Handle("PUT /api/v1/servers/{id}", roleWrap(auth.RoleAdmin, a.handleUpdateServer))
	mux.Handle("DELETE /api/v1/servers/{id}", roleWrap(auth.RoleAdmin, a.handleDeleteServer))
	mux.Handle("POST /api/v1/servers/{id}/test", roleWrap(auth.RoleAdmin, a.handleTestServer))
	mux.Handle("POST /api/v1/servers/{id}/setup", roleWrap(auth.RoleAdmin, a.handleSetupServer))
	mux.Handle("POST /api/v1/servers/{id}/hostkey/reset", roleWrap(auth.RoleAdmin, a.handleResetServerHostKey))
	mux.Handle("GET /api/v1/servers/{id}/usage", roleWrap(auth.RoleAdmin, a.handleServerUsage))

	// Users (Admin only)
	mux.Handle("GET /api/v1/users", roleWrap(auth.RoleAdmin, a.handleListUsers))
	mux.Handle("POST /api/v1/users", roleWrap(auth.RoleAdmin, a.handleCreateUser))
	mux.Handle("PUT /api/v1/users/{id}", roleWrap(auth.RoleAdmin, a.handleUpdateUser))
	mux.Handle("PUT /api/v1/users/{id}/password", roleWrap(auth.RoleAdmin, a.handleUpdateUserPassword))
	mux.Handle("DELETE /api/v1/users/{id}", roleWrap(auth.RoleAdmin, a.handleDeleteUser))

	// API Tokens (Admin only)
	mux.Handle("GET /api/v1/tokens", roleWrap(auth.RoleAdmin, a.handleListTokens))
	mux.Handle("POST /api/v1/tokens", roleWrap(auth.RoleAdmin, a.handleCreateToken))
	mux.Handle("DELETE /api/v1/tokens/{id}", roleWrap(auth.RoleAdmin, a.handleDeleteToken))

	// Settings & Storage
	mux.Handle("GET /api/v1/settings", roleWrap(auth.RoleAdmin, a.handleGetSettings))
	mux.Handle("POST /api/v1/settings", roleWrap(auth.RoleAdmin, a.handleSaveSettings))
	mux.Handle("POST /api/v1/storage/clean", roleWrap(auth.RoleAdmin, a.handleCleanStorage))

	// Nexus Integration
	mux.Handle("GET /api/v1/nexus/repositories", authWrap(a.handleGetNexusRepositories))
	mux.Handle("POST /api/v1/nexus/test", roleWrap(auth.RoleAdmin, a.handleTestNexus))
	mux.Handle("GET /api/v1/nexus/search", authWrap(a.handleSearchNexus))
	mux.Handle("POST /api/v1/nexus/sync", roleWrap(auth.RoleOperator, a.handleTriggerNexusSync))
	mux.Handle("GET /api/v1/nexus/sync/status", authWrap(a.handleGetNexusSyncStatus))

	// User Preferences
	mux.Handle("GET /api/v1/user/preferences", authWrap(a.handleGetUserPreferences))
	mux.Handle("POST /api/v1/user/preferences", authWrap(a.handleSetUserPreference))
}

func (a *API) jsonResponse(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if data != nil {
		if err := json.NewEncoder(w).Encode(data); err != nil {
			log.Printf("[API] Failed to encode JSON response: %v", err)
		}
	}
}

func (a *API) jsonError(w http.ResponseWriter, status int, message string) {
	a.jsonResponse(w, status, map[string]string{"error": message})
}
