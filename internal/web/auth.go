package web

import (
	"jobcon/internal/auth"
	"net/http"
)

func (h *WebHandler) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if u := auth.UserFromContext(r.Context()); u != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	h.render(w, r, "login.html", map[string]any{
		"Error": r.URL.Query().Get("error"),
	})
}

func (h *WebHandler) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	username := r.FormValue("username")
	password := r.FormValue("password")

	user, err := h.authenticator.Authenticate(r.Context(), username, password)
	if err != nil {
		http.Redirect(w, r, "/login?error=Ungültiger+Benutzername+oder+Passwort", http.StatusSeeOther)
		return
	}

	sessionToken := h.sessions.CreateSession(user)
	http.SetCookie(w, &http.Cookie{
		Name:     "jobcon_session",
		Value:    sessionToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   h.isCookieSecure(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400, // 24 hours
	})

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *WebHandler) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("jobcon_session"); err == nil {
		h.sessions.DestroySession(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "jobcon_session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   h.isCookieSecure(r),
		MaxAge:   -1,
	})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
