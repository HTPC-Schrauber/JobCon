package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"jobcon/internal/auth"
	"jobcon/internal/db"
	"jobcon/internal/runner"
	"net/http"
	"time"
)

type DeployRequest struct {
	Version string `json:"version"`
}

type RunRequest struct {
	Version string            `json:"version"`
	Context string            `json:"context"`
	Params  map[string]string `json:"params"`
}

func (a *API) handleListJobs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Has("page") || q.Has("page_size") || q.Has("search") || q.Has("group_id") || q.Has("sort_by") || q.Has("group_by") {
		var page, pageSize int
		if p := q.Get("page"); p != "" {
			fmt.Sscanf(p, "%d", &page)
		}
		if ps := q.Get("page_size"); ps != "" {
			fmt.Sscanf(ps, "%d", &pageSize)
		}

		filter := db.JobFilter{
			Search:    q.Get("search"),
			GroupID:   q.Get("group_id"),
			ServerID:  q.Get("server_id"),
			SortBy:    q.Get("sort_by"),
			SortOrder: q.Get("sort_order"),
			GroupBy:   q.Get("group_by"),
			Page:      page,
			PageSize:  pageSize,
		}

		res, err := a.db.ListJobsPaged(filter)
		if err != nil {
			a.jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		a.jsonResponse(w, http.StatusOK, res)
		return
	}

	jobs, err := a.db.ListJobs()
	if err != nil {
		a.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if jobs == nil {
		jobs = []db.Job{}
	}
	a.jsonResponse(w, http.StatusOK, jobs)
}

func (a *API) handleGetJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	job, err := a.db.GetJob(id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			a.jsonError(w, http.StatusNotFound, "job not found")
			return
		}
		a.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResponse(w, http.StatusOK, job)
}

func (a *API) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	var job db.Job
	if err := json.NewDecoder(r.Body).Decode(&job); err != nil {
		a.jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if job.ID == "" || job.Name == "" || job.ServerID == "" || job.GroupID == "" || job.ArtifactID == "" || job.ActiveVersion == "" || job.NexusRepo == "" {
		a.jsonError(w, http.StatusBadRequest, "missing required fields (id, name, server_id, group_id, artifact_id, active_version, nexus_repo)")
		return
	}

	if err := a.db.CreateJob(&job); err != nil {
		a.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResponse(w, http.StatusCreated, job)
}

func (a *API) handleUpdateJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var job db.Job
	if err := json.NewDecoder(r.Body).Decode(&job); err != nil {
		a.jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	job.ID = id

	if err := a.db.UpdateJob(&job); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			a.jsonError(w, http.StatusNotFound, "job not found")
			return
		}
		a.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResponse(w, http.StatusOK, job)
}

func (a *API) handleDeleteJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.db.DeleteJob(id); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			a.jsonError(w, http.StatusNotFound, "job not found")
			return
		}
		a.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResponse(w, http.StatusOK, map[string]string{"message": "job deleted"})
}

func (a *API) handleGetJobArtifact(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	job, err := a.db.GetJob(id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			a.jsonError(w, http.StatusNotFound, "job not found")
			return
		}
		a.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	version := r.URL.Query().Get("version")
	if version == "" {
		version = job.ActiveVersion
	}

	nexusURL := a.runner.BuildNexusURL(job, version)

	a.jsonResponse(w, http.StatusOK, map[string]string{
		"job_id":          job.ID,
		"name":            job.Name,
		"group_id":        job.GroupID,
		"artifact_id":     job.ArtifactID,
		"version":         version,
		"nexus_repo":      job.NexusRepo,
		"download_url":    nexusURL,
		"default_context": job.DefaultContext,
	})
}

func (a *API) handleDeployJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req DeployRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	user := auth.UserFromContext(r.Context())
	triggeredBy := "unknown"
	if user != nil {
		triggeredBy = user.Username
	}

	exec, err := a.runner.StartExecution(r.Context(), id, "deploy", req.Version, "", nil, triggeredBy)
	if err != nil {
		if errors.Is(err, runner.ErrJobAlreadyRunning) {
			a.jsonError(w, http.StatusConflict, err.Error())
			return
		}
		a.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	a.jsonResponse(w, http.StatusAccepted, exec)
}

func (a *API) handleRunJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req RunRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	user := auth.UserFromContext(r.Context())
	triggeredBy := "unknown"
	if user != nil {
		triggeredBy = user.Username
	}

	exec, err := a.runner.StartExecution(r.Context(), id, "run", req.Version, req.Context, req.Params, triggeredBy)
	if err != nil {
		if errors.Is(err, runner.ErrJobAlreadyRunning) {
			a.jsonError(w, http.StatusConflict, err.Error())
			return
		}
		a.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Synchronous blocking mode for Jenkins CI/CD
	if r.URL.Query().Get("wait") == "true" {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		timeout := time.After(2 * time.Hour) // Max run wait
		for {
			select {
			case <-timeout:
				a.jsonError(w, http.StatusGatewayTimeout, "execution timed out waiting for completion")
				return
			case <-r.Context().Done():
				return
			case <-ticker.C:
				currentExec, err := a.db.GetExecution(exec.ID)
				if err == nil && currentExec.Status != "running" && currentExec.Status != "pending" {
					status := http.StatusOK
					if currentExec.Status == "failed" {
						status = http.StatusInternalServerError
					}
					a.jsonResponse(w, status, currentExec)
					return
				}
			}
		}
	}

	a.jsonResponse(w, http.StatusAccepted, exec)
}
