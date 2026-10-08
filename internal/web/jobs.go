package web

import (
	"fmt"
	"jobcon/internal/auth"
	"jobcon/internal/domain"
	"jobcon/internal/runner"
	"jobcon/internal/services"
	"log"
	"net/http"
	"strconv"
	"strings"
)

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

	filter := domain.JobFilter{
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

func (h *WebHandler) handleWebJobRun(w http.ResponseWriter, r *http.Request) {
	jobID := r.FormValue("job_id")
	version := r.FormValue("version")
	contextName := r.FormValue("context")
	paramsRaw := r.FormValue("params")

	params := make(map[string]string)
	lines := strings.Split(paramsRaw, "\n")
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" || !strings.Contains(l, "=") {
			continue
		}
		parts := strings.SplitN(l, "=", 2)
		params[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}

	user := auth.UserFromContext(r.Context())
	triggeredBy := "web:unknown"
	if user != nil {
		triggeredBy = "web:" + user.Username
	}

	exec, err := h.jobSvc.RunJob(r.Context(), services.RunJobCommand{
		JobID:       jobID,
		Version:     version,
		ContextName: contextName,
		Params:      params,
		TriggeredBy: triggeredBy,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	http.Redirect(w, r, "/executions/"+exec.ID+"?from=jobs", http.StatusSeeOther)
}

func (h *WebHandler) handleWebJobDeploy(w http.ResponseWriter, r *http.Request) {
	jobID := r.FormValue("job_id")
	version := r.FormValue("version")
	force := r.FormValue("force") == "true" || r.FormValue("force") == "1"

	user := auth.UserFromContext(r.Context())
	triggeredBy := "web:unknown"
	if user != nil {
		triggeredBy = "web:" + user.Username
	}

	exec, err := h.jobSvc.DeployJob(r.Context(), services.DeployJobCommand{
		JobID:       jobID,
		Version:     version,
		Force:       force,
		TriggeredBy: triggeredBy,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	http.Redirect(w, r, "/executions/"+exec.ID+"?from=jobs", http.StatusSeeOther)
}

func (h *WebHandler) handleWebJobUndeploy(w http.ResponseWriter, r *http.Request) {
	jobID := r.FormValue("job_id")
	user := auth.UserFromContext(r.Context())
	triggeredBy := "web:unknown"
	if user != nil {
		triggeredBy = "web:" + user.Username
	}
	exec, err := h.jobSvc.UndeployJob(r.Context(), services.UndeployJobCommand{
		JobID:       jobID,
		TriggeredBy: triggeredBy,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
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

	execs, err := h.runner.StartBulkRunQueue(r.Context(), jobIDs, concurrency, triggeredBy)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if len(execs) == 1 {
		http.Redirect(w, r, "/executions/"+execs[0].ID+"?from=jobs", http.StatusSeeOther)
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

func (h *WebHandler) handleWebJobCreate(w http.ResponseWriter, r *http.Request) {
	var cmd services.CreateJobCommand
	cmd.ID = r.FormValue("id")
	cmd.Name = r.FormValue("name")
	cmd.ServerID = r.FormValue("server_id")
	cmd.GroupID = r.FormValue("group_id")
	cmd.ArtifactID = r.FormValue("artifact_id")
	cmd.ActiveVersion = r.FormValue("active_version")
	cmd.NexusRepo = r.FormValue("nexus_repo")
	cmd.DefaultContext = r.FormValue("default_context")
	cmd.AllowConcurrent = r.FormValue("allow_concurrent") == "1" || r.FormValue("allow_concurrent") == "on"

	retention, _ := strconv.Atoi(r.FormValue("retention_runs"))
	cmd.RetentionRuns = retention
	cmd.EnvFile = r.FormValue("env_file")
	cmd.Force = r.FormValue("force") == "true" || r.FormValue("force") == "1"

	if err := h.jobSvc.CreateJob(r.Context(), cmd); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/?tab=jobs", http.StatusSeeOther)
}

func (h *WebHandler) handleWebJobUpdate(w http.ResponseWriter, r *http.Request) {
	var cmd services.UpdateJobCommand
	cmd.ID = r.PathValue("id")
	cmd.Name = r.FormValue("name")
	cmd.ServerID = r.FormValue("server_id")
	cmd.GroupID = r.FormValue("group_id")
	cmd.ArtifactID = r.FormValue("artifact_id")
	cmd.ActiveVersion = r.FormValue("active_version")
	cmd.NexusRepo = r.FormValue("nexus_repo")
	cmd.DefaultContext = r.FormValue("default_context")
	cmd.AllowConcurrent = r.FormValue("allow_concurrent") == "1" || r.FormValue("allow_concurrent") == "on"

	retention, _ := strconv.Atoi(r.FormValue("retention_runs"))
	cmd.RetentionRuns = retention
	cmd.EnvFile = r.FormValue("env_file")

	if err := h.jobSvc.UpdateJob(r.Context(), cmd); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/?tab=jobs", http.StatusSeeOther)
}

func (h *WebHandler) handleWebJobDelete(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	if err := h.jobSvc.DeleteJob(r.Context(), jobID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/?tab=jobs", http.StatusSeeOther)
}
