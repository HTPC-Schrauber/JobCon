package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"jobcon/internal/auth"
	"jobcon/internal/db"
	"jobcon/internal/domain"
	"jobcon/internal/runner"
	"jobcon/internal/services"
	"net/http"
	"strings"
)

func (a *API) handleListJobs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Has("page") || q.Has("page_size") || q.Has("search") || q.Has("group_id") || q.Has("artifact_id") || q.Has("sort_by") || q.Has("group_by") {
		var page, pageSize int
		if p := q.Get("page"); p != "" {
			fmt.Sscanf(p, "%d", &page)
		}
		if ps := q.Get("page_size"); ps != "" {
			fmt.Sscanf(ps, "%d", &pageSize)
		}

		filter := domain.JobFilter{
			Search:     q.Get("search"),
			GroupID:    q.Get("group_id"),
			ArtifactID: q.Get("artifact_id"),
			ServerID:   q.Get("server_id"),
			SortBy:     q.Get("sort_by"),
			SortOrder:  q.Get("sort_order"),
			GroupBy:    q.Get("group_by"),
			Page:       page,
			PageSize:   pageSize,
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
		jobs = []domain.Job{}
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
	var req services.CreateJobCommand
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		a.jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Force = r.URL.Query().Get("force") == "true" || r.URL.Query().Get("force") == "1" || req.Force
	if err := a.jobSvc.CreateJob(r.Context(), req); err != nil {
		if strings.Contains(err.Error(), "already exists") {
			a.jsonError(w, http.StatusConflict, err.Error())
		} else {
			a.jsonError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	a.jsonResponse(w, http.StatusCreated, req.Job)
}

func (a *API) handleUpdateJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var job domain.Job
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
	if r.URL.Query().Get("undeploy") == "true" {
		_ = a.runner.UndeployJobSync(r.Context(), id)
	}
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
	var req services.DeployJobCommand
	_ = json.NewDecoder(r.Body).Decode(&req)
	req.JobID = r.PathValue("id")

	user := auth.UserFromContext(r.Context())
	if user != nil {
		req.TriggeredBy = user.Username
	}

	req.Force = r.URL.Query().Get("force") == "true" || r.URL.Query().Get("force") == "1" || req.Force

	exec, err := a.jobSvc.DeployJob(r.Context(), req)
	if err != nil {
		if strings.Contains(err.Error(), "already deployed") {
			a.jsonError(w, http.StatusConflict, err.Error())
		} else {
			a.jsonError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	a.jsonResponse(w, http.StatusAccepted, exec)
}

func (a *API) handleRunJob(w http.ResponseWriter, r *http.Request) {
	var req services.RunJobCommand
	_ = json.NewDecoder(r.Body).Decode(&req)
	req.JobID = r.PathValue("id")

	user := auth.UserFromContext(r.Context())
	if user != nil {
		req.TriggeredBy = user.Username
	}

	exec, err := a.jobSvc.RunJob(r.Context(), req)
	if err != nil {
		if errors.Is(err, runner.ErrJobAlreadyRunning) {
			a.jsonError(w, http.StatusConflict, err.Error())
			return
		}
		a.jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.jsonResponse(w, http.StatusAccepted, exec)
}

func (a *API) handleUndeployJob(w http.ResponseWriter, r *http.Request) {
	var req services.UndeployJobCommand
	req.JobID = r.PathValue("id")
	user := auth.UserFromContext(r.Context())
	if user != nil {
		req.TriggeredBy = user.Username
	}
	exec, err := a.jobSvc.UndeployJob(r.Context(), req)
	if err != nil {
		a.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResponse(w, http.StatusAccepted, exec)
}

type BulkJobResult struct {
	JobID   string            `json:"job_id"`
	Success bool              `json:"success"`
	Exec    *domain.Execution `json:"execution,omitempty"`
	Error   string            `json:"error,omitempty"`
}

func (a *API) handleBulkRunJobs(w http.ResponseWriter, r *http.Request) {
	var req services.BulkRunCommand
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		a.jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	user := auth.UserFromContext(r.Context())
	if user != nil {
		req.TriggeredBy = user.Username
	}

	execs, err := a.jobSvc.BulkRun(r.Context(), req)
	if err != nil {
		a.jsonError(w, http.StatusBadRequest, err.Error())
		return
	}

	var results []BulkJobResult
	for _, exec := range execs {
		results = append(results, BulkJobResult{JobID: exec.JobID, Success: true, Exec: exec})
	}
	a.jsonResponse(w, http.StatusOK, results)
}

func (a *API) handleBulkDeployJobs(w http.ResponseWriter, r *http.Request) {
	var req services.BulkDeployCommand
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		a.jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	user := auth.UserFromContext(r.Context())
	if user != nil {
		req.TriggeredBy = user.Username
	}
	execs, err := a.jobSvc.BulkDeploy(r.Context(), req)
	if err != nil {
		a.jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	var results []BulkJobResult
	for _, exec := range execs {
		results = append(results, BulkJobResult{JobID: exec.JobID, Success: true, Exec: exec})
	}
	a.jsonResponse(w, http.StatusOK, results)
}

func (a *API) handleBulkUndeployJobs(w http.ResponseWriter, r *http.Request) {
	var req services.BulkUndeployCommand
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		a.jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	user := auth.UserFromContext(r.Context())
	if user != nil {
		req.TriggeredBy = user.Username
	}
	execs, err := a.jobSvc.BulkUndeploy(r.Context(), req)
	if err != nil {
		a.jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	var results []BulkJobResult
	for _, exec := range execs {
		results = append(results, BulkJobResult{JobID: exec.JobID, Success: true, Exec: exec})
	}
	a.jsonResponse(w, http.StatusOK, results)
}
