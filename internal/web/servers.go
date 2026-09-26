package web

import (
	"fmt"
	"jobcon/internal/auth"
	"jobcon/internal/domain"
	"net/http"
	"net/url"
	"strings"
)

func (h *WebHandler) handleSettingsServers(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	servers, _ := h.db.ListServers()

	data := map[string]any{
		"Title":      "Execution Server",
		"CurrentTab": "settings",
		"User":       user,
		"Servers":    servers,
		"ErrorMsg":   r.URL.Query().Get("error"),
		"SuccessMsg": r.URL.Query().Get("success"),
	}
	h.render(w, r, "settings_servers.html", data)
}

func (h *WebHandler) handleWebServerCreate(w http.ResponseWriter, r *http.Request) {
	jobsDir := strings.TrimSpace(r.FormValue("jobs_dir"))
	if jobsDir == "" {
		jobsDir = "/opt/talend/jobs"
	}
	scriptsDir := strings.TrimSpace(r.FormValue("scripts_dir"))
	if scriptsDir == "" {
		scriptsDir = "/opt/talend/scripts"
	}
	server := &domain.Server{
		ID:         r.FormValue("id"),
		Name:       r.FormValue("name"),
		Host:       r.FormValue("host"),
		User:       r.FormValue("user"),
		SSHKeyPath: r.FormValue("ssh_key_path"),
		JobsDir:    jobsDir,
		ScriptsDir: scriptsDir,
		EnvFile:    strings.TrimSpace(r.FormValue("env_file")),
	}
	var port int
	if _, err := fmt.Sscanf(r.FormValue("port"), "%d", &port); err == nil && port > 0 {
		server.Port = port
	}
	keepReleases := 3
	if _, err := fmt.Sscanf(r.FormValue("keep_releases"), "%d", &keepReleases); err != nil || keepReleases <= 0 {
		keepReleases = 3
	}
	server.KeepReleases = keepReleases

	if err := h.db.CreateServer(server); err != nil {
		http.Redirect(w, r, "/settings/servers?error="+url.QueryEscape("Fehler beim Anlegen: "+err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/settings/servers?success="+url.QueryEscape("Execution Server erfolgreich angelegt."), http.StatusSeeOther)
}

func (h *WebHandler) handleWebServerUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	jobsDir := strings.TrimSpace(r.FormValue("jobs_dir"))
	if jobsDir == "" {
		jobsDir = "/opt/talend/jobs"
	}
	scriptsDir := strings.TrimSpace(r.FormValue("scripts_dir"))
	if scriptsDir == "" {
		scriptsDir = "/opt/talend/scripts"
	}
	server := &domain.Server{
		ID:         id,
		Name:       r.FormValue("name"),
		Host:       r.FormValue("host"),
		User:       r.FormValue("user"),
		SSHKeyPath: r.FormValue("ssh_key_path"),
		JobsDir:    jobsDir,
		ScriptsDir: scriptsDir,
		EnvFile:    strings.TrimSpace(r.FormValue("env_file")),
	}
	var port int
	if _, err := fmt.Sscanf(r.FormValue("port"), "%d", &port); err == nil && port > 0 {
		server.Port = port
	} else {
		server.Port = 22
	}
	keepReleases := 3
	if _, err := fmt.Sscanf(r.FormValue("keep_releases"), "%d", &keepReleases); err != nil || keepReleases <= 0 {
		keepReleases = 3
	}
	server.KeepReleases = keepReleases

	if err := h.db.UpdateServer(server); err != nil {
		http.Redirect(w, r, "/settings/servers?error="+url.QueryEscape("Fehler beim Aktualisieren: "+err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/settings/servers?success="+url.QueryEscape("Execution Server erfolgreich aktualisiert."), http.StatusSeeOther)
}

func (h *WebHandler) handleWebServerDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	targetServerID := strings.TrimSpace(r.FormValue("target_server_id"))

	jobs, err := h.db.GetJobsByServerID(id)
	if err != nil {
		http.Redirect(w, r, "/settings/servers?error="+url.QueryEscape("Fehler beim Abrufen der Jobs: "+err.Error()), http.StatusSeeOther)
		return
	}

	if len(jobs) > 0 {
		if targetServerID == "" {
			http.Redirect(w, r, "/settings/servers?error="+url.QueryEscape(fmt.Sprintf("Server wird noch von %d Job(s) verwendet. Bitte wählen Sie einen alternativen Zielserver aus.", len(jobs))), http.StatusSeeOther)
			return
		}
		if err := h.db.DeleteServerWithJobReassignment(id, targetServerID); err != nil {
			http.Redirect(w, r, "/settings/servers?error="+url.QueryEscape("Fehler bei der Job-Migration / Löschung: "+err.Error()), http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/settings/servers?success="+url.QueryEscape(fmt.Sprintf("Execution Server erfolgreich gelöscht und %d Job(s) auf Zielserver umgestellt.", len(jobs))), http.StatusSeeOther)
		return
	}

	if err := h.db.DeleteServer(id); err != nil {
		http.Redirect(w, r, "/settings/servers?error="+url.QueryEscape("Fehler beim Löschen: "+err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/settings/servers?success="+url.QueryEscape("Execution Server erfolgreich gelöscht."), http.StatusSeeOther)
}
