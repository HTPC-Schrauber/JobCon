package web

import (
	"fmt"
	"jobcon/internal/auth"
	"jobcon/internal/runner"
	"net/http"
	"strconv"
	"strings"
)

func (h *WebHandler) handleSettingsSystem(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	tokens, _ := h.db.ListAPITokens()

	baseURL, _ := h.db.GetSetting("nexus_base_url", h.cfg.Nexus.BaseURL)
	nexusUser, _ := h.db.GetSetting("nexus_username", h.cfg.Nexus.Username)
	nexusPass, _ := h.db.GetEncryptedSetting("nexus_password", h.cfg.Nexus.Password)
	anonSetting, _ := h.db.GetSetting("nexus_anonymous", "")

	isAnonymous := false
	if anonSetting == "true" {
		isAnonymous = true
	} else if anonSetting == "false" {
		isAnonymous = false
	} else if nexusUser == "" {
		isAnonymous = true
	}

	repos := h.getNexusRepositories()
	syncStatus := h.getNexusSyncStatus()
	concurrency := h.getMaxConcurrentJobs()
	artifactCount, _ := h.db.GetNexusArtifactCount()

	data := map[string]any{
		"Title":              "System & Tokens",
		"CurrentTab":         "settings",
		"User":               user,
		"Tokens":             tokens,
		"NexusBaseURL":       baseURL,
		"NexusUser":          nexusUser,
		"NexusAnonymous":     isAnonymous,
		"HasPassword":        nexusPass != "",
		"NexusRepositories":  repos,
		"NexusSyncStatus":    syncStatus,
		"NexusSyncTimeAgo":   formatTimeAgo(syncStatus.LastSyncedAt),
		"NexusSyncInterval":  syncStatus.IntervalMinutes,
		"NexusArtifactCount": artifactCount,
		"MaxConcurrentJobs":  concurrency,
		"SuccessMsg":         r.URL.Query().Get("success"),
		"ErrorMsg":           r.URL.Query().Get("error"),
		"NewTokenRaw":        strings.TrimSpace(r.URL.Query().Get("token")),
	}
	h.render(w, r, "settings_system.html", data)
}

func (h *WebHandler) handleSetLanguage(w http.ResponseWriter, r *http.Request) {
	lang := strings.TrimSpace(r.FormValue("lang"))
	if lang == "" {
		lang = r.URL.Query().Get("lang")
	}
	if lang == "" {
		lang = "en"
	}

	u := auth.UserFromContext(r.Context())
	if u == nil {
		u, _ = h.authMW.AuthenticateRequest(r)
	}
	if u != nil {
		_ = h.db.SetUserPreference(u.ID, "language", lang)
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "jobcon_lang",
		Value:    lang,
		Path:     "/",
		Secure:   h.isCookieSecure(r),
		MaxAge:   365 * 86400,
		SameSite: http.SameSiteLaxMode,
	})

	referer := r.Referer()
	if referer == "" {
		referer = "/"
	}
	http.Redirect(w, r, referer, http.StatusSeeOther)
}

func (h *WebHandler) handleWebConcurrency(w http.ResponseWriter, r *http.Request) {
	conStr := strings.TrimSpace(r.FormValue("max_concurrent_jobs"))
	if c, err := strconv.Atoi(conStr); err == nil && c > 0 && c <= runner.MaxBulkConcurrency {
		_ = h.db.SetSetting("max_concurrent_jobs", strconv.Itoa(c))
	}
	http.Redirect(w, r, "/settings/system?success=Gleichzeitige+Jobs+erfolgreich+gespeichert", http.StatusSeeOther)
}

func (h *WebHandler) handleWebSettingsCleanRetention(w http.ResponseWriter, r *http.Request) {
	purged := h.storage.CleanAllRetention(h.db)
	msg := fmt.Sprintf("%d+alte+Logs+erfolgreich+bereinigt", purged)
	http.Redirect(w, r, "/settings/system?success="+msg, http.StatusSeeOther)
}
