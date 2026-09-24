package api

import (
	"context"
	"encoding/json"
	"jobcon/internal/auth"
	"jobcon/internal/config"
	"jobcon/internal/db"
	"jobcon/internal/nexus"
	"net/http"
	"strings"
)

type NexusTestRequest struct {
	BaseURL  string `json:"base_url"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func (a *API) getNexusClient() *nexus.Client {
	baseURL, _ := a.db.GetSetting("nexus_base_url", a.cfg.Nexus.BaseURL)
	username, _ := a.db.GetSetting("nexus_username", a.cfg.Nexus.Username)
	password, _ := a.db.GetEncryptedSetting("nexus_password", a.cfg.Nexus.Password)
	return nexus.NewClient(baseURL, username, password)
}

func (a *API) handleGetNexusRepositories(w http.ResponseWriter, r *http.Request) {
	reposJSON, err := a.db.GetSetting("nexus_repositories", "")
	if err == nil && reposJSON != "" {
		var repos []config.NexusRepository
		if err := json.Unmarshal([]byte(reposJSON), &repos); err == nil && len(repos) > 0 {
			a.jsonResponse(w, http.StatusOK, repos)
			return
		}
	}

	// Fallback to config.yaml repositories
	a.jsonResponse(w, http.StatusOK, a.cfg.Nexus.Repositories)
}

func (a *API) handleTestNexus(w http.ResponseWriter, r *http.Request) {
	var req NexusTestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		a.jsonResponse(w, http.StatusBadRequest, map[string]any{
			"success": false,
			"message": "Ungültiger Request-Body",
		})
		return
	}

	client := a.getNexusClient()
	if req.BaseURL != "" {
		req.BaseURL = strings.TrimSpace(req.BaseURL)
		if !nexus.SafeNexusURLRegex.MatchString(req.BaseURL) {
			a.jsonResponse(w, http.StatusOK, map[string]any{
				"success": false,
				"message": "Ungültige Basis-URL. Erlaubt sind nur gültige HTTP- oder HTTPS-URLs.",
			})
			return
		}
		if err := nexus.ValidateNexusURL(req.BaseURL); err != nil {
			a.jsonResponse(w, http.StatusOK, map[string]any{
				"success": false,
				"message": err.Error(),
			})
			return
		}
		pass := req.Password
		if pass == "" && req.Username != "" {
			savedUser, _ := a.db.GetSetting("nexus_username", a.cfg.Nexus.Username)
			if req.Username == savedUser {
				pass, _ = a.db.GetEncryptedSetting("nexus_password", a.cfg.Nexus.Password)
			}
		}
		client = nexus.NewClient(req.BaseURL, req.Username, pass)
	}

	ok, msg, err := client.TestConnection(r.Context())
	if err != nil {
		a.jsonResponse(w, http.StatusOK, map[string]any{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	a.jsonResponse(w, http.StatusOK, map[string]any{
		"success": ok,
		"message": msg,
	})
}

func (a *API) handleSearchNexus(w http.ResponseWriter, r *http.Request) {
	repo := r.URL.Query().Get("repo")
	group := r.URL.Query().Get("group")
	name := r.URL.Query().Get("name")
	if name == "" {
		name = r.URL.Query().Get("artifact")
	}
	query := r.URL.Query().Get("query")

	if repo == "" {
		// Use default repository from settings or config
		reposJSON, _ := a.db.GetSetting("nexus_repositories", "")
		if reposJSON != "" {
			var repos []config.NexusRepository
			if err := json.Unmarshal([]byte(reposJSON), &repos); err == nil && len(repos) > 0 {
				repo = repos[0].ID
			}
		}
		if repo == "" && len(a.cfg.Nexus.Repositories) > 0 {
			repo = a.cfg.Nexus.Repositories[0].ID
		}
	}

	tree, err := a.db.SearchNexusArtifacts(repo, group, name, query)
	if err != nil {
		a.jsonError(w, http.StatusInternalServerError, "Nexus-Suche fehlgeschlagen: "+err.Error())
		return
	}

	if tree == nil {
		tree = []db.GroupNode{}
	}
	a.jsonResponse(w, http.StatusOK, tree)
}

func (a *API) handleTriggerNexusSync(w http.ResponseWriter, r *http.Request) {
	if a.syncer == nil {
		a.jsonError(w, http.StatusBadRequest, "Nexus syncer is not configured")
		return
	}

	go func() {
		_ = a.syncer.Sync(context.Background())
	}()

	a.jsonResponse(w, http.StatusAccepted, map[string]any{
		"status":  "started",
		"message": "Nexus-Synchronisation gestartet",
	})
}

func (a *API) handleGetNexusSyncStatus(w http.ResponseWriter, r *http.Request) {
	if a.syncer == nil {
		a.jsonResponse(w, http.StatusOK, nexus.SyncStatus{Status: "idle"})
		return
	}
	a.jsonResponse(w, http.StatusOK, a.syncer.GetStatus())
}

func (a *API) handleCleanStorage(w http.ResponseWriter, r *http.Request) {
	purged := a.storage.CleanAllRetention(a.db)
	a.jsonResponse(w, http.StatusOK, map[string]any{
		"message":      "Retention cleanup completed",
		"purged_count": purged,
	})
}

func (a *API) handleGetUserPreferences(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		a.jsonError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	prefs, err := a.db.GetAllUserPreferences(user.ID)
	if err != nil {
		a.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if prefs == nil {
		prefs = make(map[string]string)
	}
	a.jsonResponse(w, http.StatusOK, prefs)
}

func (a *API) handleSetUserPreference(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		a.jsonError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req map[string]string
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		a.jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	for k, v := range req {
		if err := a.db.SetUserPreference(user.ID, k, v); err != nil {
			a.jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	a.jsonResponse(w, http.StatusOK, map[string]string{"status": "saved"})
}
