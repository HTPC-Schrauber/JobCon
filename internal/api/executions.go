package api

import (
	"errors"
	"jobcon/internal/db"
	"jobcon/internal/runner"
	"net/http"
	"strconv"
)

func (a *API) handleListExecutions(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	jobID := r.URL.Query().Get("job_id")
	var execs []db.Execution
	var err error

	if jobID != "" {
		execs, err = a.db.ListExecutionsByJob(jobID, limit)
	} else {
		execs, err = a.db.ListExecutions(limit)
	}

	if err != nil {
		a.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if execs == nil {
		execs = []db.Execution{}
	}
	a.jsonResponse(w, http.StatusOK, execs)
}

func (a *API) handleGetExecution(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	exec, err := a.db.GetExecution(id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			a.jsonError(w, http.StatusNotFound, "execution not found")
			return
		}
		a.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResponse(w, http.StatusOK, exec)
}

func (a *API) handleGetExecutionLogs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	exec, err := a.db.GetExecution(id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			a.jsonError(w, http.StatusNotFound, "execution not found")
			return
		}
		a.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	format := r.URL.Query().Get("format") // "sse" or "raw"

	// If execution is currently active and format is SSE (or default for browser), stream SSE
	broadcaster := a.runner.GetBroadcaster(id)
	if broadcaster != nil && format != "raw" {
		streamLogsSSE(w, r, broadcaster)
		return
	}

	// Execution finished or raw format requested: read from file
	if exec.LogPath == "" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("[JobCon] No log file associated with execution."))
		return
	}

	logBytes, err := a.storage.ReadLog(exec.LogPath)
	if err != nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("[JobCon] Log file not available or purged."))
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(logBytes)
}

func (a *API) handleAbortExecution(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.runner.AbortExecution(id); err != nil {
		if errors.Is(err, runner.ErrExecutionNotFound) {
			a.jsonError(w, http.StatusNotFound, "execution is not running or not found")
			return
		}
		a.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	a.jsonResponse(w, http.StatusOK, map[string]string{"message": "abort signal sent"})
}
