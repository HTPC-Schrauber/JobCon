package web

import (
	"context"
	"encoding/json"
	"jobcon/internal/config"
	"jobcon/internal/nexus"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func (h *WebHandler) handleWebSettingsNexus(w http.ResponseWriter, r *http.Request) {
	baseURL := strings.TrimSpace(r.FormValue("nexus_base_url"))
	nexusUser := strings.TrimSpace(r.FormValue("nexus_username"))
	nexusPass := r.FormValue("nexus_password")
	isAnon := r.FormValue("nexus_anonymous") == "true" || r.FormValue("nexus_anonymous") == "on"

	if baseURL == "" {
		http.Redirect(w, r, "/settings/system?error=Nexus+Basis-URL+darf+nicht+leer+sein", http.StatusSeeOther)
		return
	}
	if !nexus.SafeNexusURLRegex.MatchString(baseURL) {
		http.Redirect(w, r, "/settings/system?error=Ungültige+Nexus+Basis-URL.+Erlaubt+sind+nur+HTTP-+oder+HTTPS-URLs.", http.StatusSeeOther)
		return
	}
	if err := nexus.ValidateNexusURL(baseURL); err != nil {
		http.Redirect(w, r, "/settings/system?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}

	_ = h.db.SetSetting("nexus_base_url", baseURL)
	if isAnon {
		_ = h.db.SetSetting("nexus_anonymous", "true")
		_ = h.db.SetSetting("nexus_username", "")
		_ = h.db.SetEncryptedSetting("nexus_password", "")
	} else {
		_ = h.db.SetSetting("nexus_anonymous", "false")
		_ = h.db.SetSetting("nexus_username", nexusUser)
		if strings.TrimSpace(nexusPass) != "" {
			_ = h.db.SetEncryptedSetting("nexus_password", nexusPass)
		}
	}

	// Trigger background sync of .nexus_auth to all registered servers
	if h.runner != nil {
		go h.runner.SyncNexusAuthToAllServers(context.Background())
	}

	http.Redirect(w, r, "/settings/system?success=Nexus-Einstellungen+erfolgreich+gespeichert", http.StatusSeeOther)
}

func (h *WebHandler) handleWebNexusRepoAdd(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.FormValue("repo_id"))
	label := strings.TrimSpace(r.FormValue("repo_label"))
	if id == "" {
		http.Redirect(w, r, "/settings/system?error=Repository-ID+darf+nicht+leer+sein", http.StatusSeeOther)
		return
	}
	if label == "" {
		label = id
	}

	repos := h.getNexusRepositories()
	exists := false
	for i, repo := range repos {
		if repo.ID == id {
			repos[i].Label = label
			exists = true
			break
		}
	}
	if !exists {
		repos = append(repos, config.NexusRepository{ID: id, Label: label})
	}

	bytes, _ := json.Marshal(repos)
	_ = h.db.SetSetting("nexus_repositories", string(bytes))

	http.Redirect(w, r, "/settings/system?success=Repository+erfolgreich+hinzugefügt", http.StatusSeeOther)
}

func (h *WebHandler) handleWebNexusRepoDelete(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.FormValue("repo_id"))
	repos := h.getNexusRepositories()
	var updated []config.NexusRepository
	for _, repo := range repos {
		if repo.ID != id {
			updated = append(updated, repo)
		}
	}
	bytes, _ := json.Marshal(updated)
	_ = h.db.SetSetting("nexus_repositories", string(bytes))

	http.Redirect(w, r, "/settings/system?success=Repository+erfolgreich+entfernt", http.StatusSeeOther)
}

func (h *WebHandler) handleWebNexusSync(w http.ResponseWriter, r *http.Request) {
	if h.syncer != nil {
		go func() {
			_ = h.syncer.Sync(context.Background())
		}()
	}
	referer := r.Referer()
	if referer == "" {
		referer = "/settings/system"
	}
	http.Redirect(w, r, referer, http.StatusSeeOther)
}

func (h *WebHandler) handleWebNexusInterval(w http.ResponseWriter, r *http.Request) {
	intvStr := strings.TrimSpace(r.FormValue("interval_minutes"))
	if intvStr == "" {
		intvStr = strings.TrimSpace(r.FormValue("interval"))
	}
	if intv, err := strconv.Atoi(intvStr); err == nil && intv >= 0 {
		if h.syncer != nil {
			_ = h.syncer.SetIntervalMinutes(intv)
		} else {
			_ = h.db.SetSetting("nexus_sync_interval_minutes", strconv.Itoa(intv))
		}
	}
	http.Redirect(w, r, "/settings/system?success=Intervall+erfolgreich+gespeichert", http.StatusSeeOther)
}
