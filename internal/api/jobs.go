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

type CreateJobRequest struct {
	db.Job
	Force bool `json:"force"`
}

type DeployRequest struct {
	Version string `json:"version"`
	Force   bool   `json:"force"`
}

type RunRequest struct {
	Version string            `json:"version"`
	Context string            `json:"context"`
	Params  map[string]string `json:"params"`
}

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

		filter := db.JobFilter{
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
	var req CreateJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		a.jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	job := req.Job
	if job.ID == "" || job.Name == "" || job.ServerID == "" || job.GroupID == "" || job.ArtifactID == "" || job.ActiveVersion == "" || job.NexusRepo == "" {
		a.jsonError(w, http.StatusBadRequest, "missing required fields (id, name, server_id, group_id, artifact_id, active_version, nexus_repo)")
		return
	}

	isForce := r.URL.Query().Get("force") == "true" || r.URL.Query().Get("force") == "1" || req.Force
	if !isForce {
		existing, err := a.db.GetJobsByArtifact(job.ArtifactID)
		if err != nil {
			a.jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if len(existing) > 0 {
			a.jsonError(w, http.StatusConflict, fmt.Sprintf("a job with artifact '%s' already exists (job ID: '%s', name: '%s'). Use force=true to proceed", job.ArtifactID, existing[0].ID, existing[0].Name))
			return
		}
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
	id := r.PathValue("id")
	var req DeployRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	job, err := a.db.GetJob(id)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			a.jsonError(w, http.StatusNotFound, "job not found")
			return
		}
		a.jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	targetVersion := req.Version
	if targetVersion == "" {
		targetVersion = job.ActiveVersion
	}

	isForce := r.URL.Query().Get("force") == "true" || r.URL.Query().Get("force") == "1" || req.Force
	if !isForce {
		if job.IsDeployed && job.DeployedVersion != "" && job.DeployedVersion != targetVersion {
			a.jsonError(w, http.StatusConflict, fmt.Sprintf("another version '%s' of artifact '%s' is already deployed on server. Use force=true to proceed", job.DeployedVersion, job.ArtifactID))
			return
		}

		serverJobs, err := a.db.GetJobsByArtifactOnServer(job.ArtifactID, job.ServerID)
		if err == nil {
			for _, sj := range serverJobs {
				if sj.ID != job.ID && sj.IsDeployed && sj.DeployedVersion != "" && sj.DeployedVersion != targetVersion {
					a.jsonError(w, http.StatusConflict, fmt.Sprintf("artifact '%s' is already deployed on this server by job '%s' with version '%s'. Use force=true to proceed", job.ArtifactID, sj.Name, sj.DeployedVersion))
					return
				}
			}
		}
	}

	user := auth.UserFromContext(r.Context())
	triggeredBy := "unknown"
	if user != nil {
		triggeredBy = user.Username
	}

	exec, err := a.runner.StartExecution(r.Context(), id, "deploy", targetVersion, "", nil, triggeredBy)
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

func (a *API) handleUndeployJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	user := auth.UserFromContext(r.Context())
	triggeredBy := "unknown"
	if user != nil {
		triggeredBy = user.Username
	}

	exec, err := a.runner.StartExecution(r.Context(), id, "undeploy", "", "", nil, triggeredBy)
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

type BulkJobsRequest struct {
	JobIDs []string `json:"job_ids"`
	Force  bool     `json:"force"`
}

type BulkJobResult struct {
	JobID   string        `json:"job_id"`
	Success bool          `json:"success"`
	Exec    *db.Execution `json:"execution,omitempty"`
	Error   string        `json:"error,omitempty"`
}

func (a *API) handleBulkRunJobs(w http.ResponseWriter, r *http.Request) {
	var req BulkJobsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		a.jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	user := auth.UserFromContext(r.Context())
	triggeredBy := "unknown"
	if user != nil {
		triggeredBy = user.Username
	}

	var results []BulkJobResult
	for _, id := range req.JobIDs {
		job, err := a.db.GetJob(id)
		if err != nil {
			results = append(results, BulkJobResult{JobID: id, Success: false, Error: err.Error()})
			continue
		}
		exec, err := a.runner.StartExecution(r.Context(), id, "run", job.ActiveVersion, job.DefaultContext, nil, triggeredBy)
		if err != nil {
			results = append(results, BulkJobResult{JobID: id, Success: false, Error: err.Error()})
		} else {
			results = append(results, BulkJobResult{JobID: id, Success: true, Exec: exec})
		}
	}

	a.jsonResponse(w, http.StatusOK, results)
}

func (a *API) handleBulkDeployJobs(w http.ResponseWriter, r *http.Request) {
	var req BulkJobsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		a.jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	user := auth.UserFromContext(r.Context())
	triggeredBy := "unknown"
	if user != nil {
		triggeredBy = user.Username
	}

	isForce := r.URL.Query().Get("force") == "true" || r.URL.Query().Get("force") == "1" || req.Force

	var results []BulkJobResult
	for _, id := range req.JobIDs {
		job, err := a.db.GetJob(id)
		if err != nil {
			results = append(results, BulkJobResult{JobID: id, Success: false, Error: err.Error()})
			continue
		}

		targetVersion := job.ActiveVersion
		if !isForce {
			var conflictErr string
			if job.IsDeployed && job.DeployedVersion != "" && job.DeployedVersion != targetVersion {
				conflictErr = fmt.Sprintf("another version '%s' of artifact '%s' is already deployed on server. Use force=true to proceed", job.DeployedVersion, job.ArtifactID)
			} else {
				serverJobs, _ := a.db.GetJobsByArtifactOnServer(job.ArtifactID, job.ServerID)
				for _, sj := range serverJobs {
					if sj.ID != job.ID && sj.IsDeployed && sj.DeployedVersion != "" && sj.DeployedVersion != targetVersion {
						conflictErr = fmt.Sprintf("artifact '%s' is already deployed on this server by job '%s' with version '%s'. Use force=true to proceed", job.ArtifactID, sj.Name, sj.DeployedVersion)
						break
					}
				}
			}
			if conflictErr != "" {
				results = append(results, BulkJobResult{JobID: id, Success: false, Error: conflictErr})
				continue
			}
		}

		exec, err := a.runner.StartExecution(r.Context(), id, "deploy", targetVersion, "", nil, triggeredBy)
		if err != nil {
			results = append(results, BulkJobResult{JobID: id, Success: false, Error: err.Error()})
		} else {
			results = append(results, BulkJobResult{JobID: id, Success: true, Exec: exec})
		}
	}

	a.jsonResponse(w, http.StatusOK, results)
}

func (a *API) handleBulkUndeployJobs(w http.ResponseWriter, r *http.Request) {
	var req BulkJobsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		a.jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	user := auth.UserFromContext(r.Context())
	triggeredBy := "unknown"
	if user != nil {
		triggeredBy = user.Username
	}

	var results []BulkJobResult
	for _, id := range req.JobIDs {
		exec, err := a.runner.StartExecution(r.Context(), id, "undeploy", "", "", nil, triggeredBy)
		if err != nil {
			results = append(results, BulkJobResult{JobID: id, Success: false, Error: err.Error()})
		} else {
			results = append(results, BulkJobResult{JobID: id, Success: true, Exec: exec})
		}
	}

	a.jsonResponse(w, http.StatusOK, results)
}
