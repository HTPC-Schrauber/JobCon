package api

import (
	"encoding/json"
	"errors"
	"jobcon/internal/db"
	"net/http"
)

func (a *API) handleListServers(w http.ResponseWriter, r *http.Request) {
	servers, err := a.db.ListServers()
	if err != nil {
		a.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if servers == nil {
		servers = []db.Server{}
	}
	a.jsonResponse(w, http.StatusOK, servers)
}

func (a *API) handleGetServer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	server, err := a.db.GetServer(id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			a.jsonError(w, http.StatusNotFound, "server not found")
			return
		}
		a.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResponse(w, http.StatusOK, server)
}

func (a *API) handleCreateServer(w http.ResponseWriter, r *http.Request) {
	var server db.Server
	if err := json.NewDecoder(r.Body).Decode(&server); err != nil {
		a.jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if server.ID == "" || server.Name == "" || server.Host == "" {
		a.jsonError(w, http.StatusBadRequest, "missing required fields (id, name, host)")
		return
	}

	if err := a.db.CreateServer(&server); err != nil {
		a.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResponse(w, http.StatusCreated, server)
}

func (a *API) handleUpdateServer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var server db.Server
	if err := json.NewDecoder(r.Body).Decode(&server); err != nil {
		a.jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	server.ID = id

	if err := a.db.UpdateServer(&server); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			a.jsonError(w, http.StatusNotFound, "server not found")
			return
		}
		a.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResponse(w, http.StatusOK, server)
}

func (a *API) handleDeleteServer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// Target server can be supplied via query param or json body
	targetServerID := r.URL.Query().Get("target_server_id")
	if targetServerID == "" && r.Body != nil {
		var req struct {
			TargetServerID string `json:"target_server_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		targetServerID = req.TargetServerID
	}

	if targetServerID != "" {
		if err := a.db.DeleteServerWithJobReassignment(id, targetServerID); err != nil {
			if errors.Is(err, db.ErrNotFound) {
				a.jsonError(w, http.StatusNotFound, "server not found")
				return
			}
			a.jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		a.jsonResponse(w, http.StatusOK, map[string]string{
			"message":          "server deleted and jobs reassigned",
			"target_server_id": targetServerID,
		})
		return
	}

	// If no target server is provided, check if jobs exist
	jobs, err := a.db.GetJobsByServerID(id)
	if err != nil {
		a.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(jobs) > 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":     "server is still in use by configured jobs",
			"job_count": len(jobs),
			"jobs":      jobs,
		})
		return
	}

	if err := a.db.DeleteServer(id); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			a.jsonError(w, http.StatusNotFound, "server not found")
			return
		}
		if errors.Is(err, db.ErrServerInUse) {
			a.jsonError(w, http.StatusConflict, err.Error())
			return
		}
		a.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResponse(w, http.StatusOK, map[string]string{"message": "server deleted"})
}

func (a *API) handleServerUsage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	server, err := a.db.GetServer(id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			a.jsonError(w, http.StatusNotFound, "server not found")
			return
		}
		a.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	jobs, err := a.db.GetJobsByServerID(id)
	if err != nil {
		a.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if jobs == nil {
		jobs = []db.Job{}
	}

	allServers, err := a.db.ListServers()
	if err != nil {
		a.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var otherServers []db.Server
	for _, s := range allServers {
		if s.ID != id {
			otherServers = append(otherServers, s)
		}
	}
	if otherServers == nil {
		otherServers = []db.Server{}
	}

	a.jsonResponse(w, http.StatusOK, map[string]any{
		"server":        server,
		"job_count":     len(jobs),
		"jobs":          jobs,
		"other_servers": otherServers,
	})
}

func (a *API) handleSetupServer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	server, err := a.db.GetServer(id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			a.jsonError(w, http.StatusNotFound, "server not found")
			return
		}
		a.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	res, err := a.ssh.SetupServer(r.Context(), server)
	if err != nil {
		a.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if res.Success {
		_ = a.db.UpdateServerStatus(id, "online")
	}

	a.jsonResponse(w, http.StatusOK, res)
}

func (a *API) handleTestServer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	server, err := a.db.GetServer(id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			a.jsonError(w, http.StatusNotFound, "server not found")
			return
		}
		a.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	res, _ := a.ssh.TestConnection(r.Context(), server)
	if res.Success {
		_ = a.db.UpdateServerStatus(id, "online")
	} else {
		_ = a.db.UpdateServerStatus(id, "offline")
	}

	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if res.Success {
			w.Write([]byte(`<span class="badge badge-success">Online</span>`))
		} else {
			errMsg := res.ErrorMessage
			if errMsg == "" {
				errMsg = "Verbindung fehlgeschlagen"
			}
			w.Write([]byte(`<span class="badge badge-danger" title="` + errMsg + `">Offline</span>`))
		}
		return
	}

	a.jsonResponse(w, http.StatusOK, res)
}

func (a *API) handleResetServerHostKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := a.db.GetServer(id); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			a.jsonError(w, http.StatusNotFound, "server not found")
			return
		}
		a.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if err := a.db.ResetServerHostKey(id); err != nil {
		a.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	a.jsonResponse(w, http.StatusOK, map[string]string{
		"message":   "host key reset successfully",
		"server_id": id,
	})
}
