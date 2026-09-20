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
	if err := a.db.DeleteServer(id); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			a.jsonError(w, http.StatusNotFound, "server not found")
			return
		}
		a.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResponse(w, http.StatusOK, map[string]string{"message": "server deleted"})
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

	a.jsonResponse(w, http.StatusOK, res)
}
