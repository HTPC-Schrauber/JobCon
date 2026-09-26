package web

import (
	"jobcon/internal/auth"
	"log"
	"net/http"
	"strings"
)

func (h *WebHandler) handleExecutionsPage(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	status := r.URL.Query().Get("status")
	execs, err := h.db.ListExecutionsFiltered(status, 100)
	if err != nil {
		log.Printf("[Web] ListExecutionsFiltered error: %v", err)
	}

	data := map[string]any{
		"Title":        "Ausführungshistorie",
		"CurrentTab":   "executions",
		"User":         user,
		"Executions":   execs,
		"FilterStatus": status,
	}
	h.render(w, r, "executions_page.html", data)
}

func (h *WebHandler) handleExecutionPage(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id := r.PathValue("id")
	exec, err := h.db.GetExecution(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	from := r.URL.Query().Get("from")
	if from == "" {
		ref := r.Header.Get("Referer")
		if strings.Contains(ref, "/executions") {
			from = "executions"
		} else {
			from = "jobs"
		}
	}

	backURL := "/"
	backLabelKey := "executions.back_to_jobs"
	if from == "executions" {
		backURL = "/executions"
		backLabelKey = "executions.back_to_executions"
	}

	data := map[string]any{
		"Title":        "Execution " + exec.ID,
		"CurrentTab":   "executions",
		"User":         user,
		"Exec":         exec,
		"From":         from,
		"BackURL":      backURL,
		"BackLabelKey": backLabelKey,
	}
	h.render(w, r, "execution.html", data)
}
