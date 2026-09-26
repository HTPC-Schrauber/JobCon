package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"jobcon/internal/auth"
	"jobcon/internal/config"
	"jobcon/internal/db"
	"jobcon/internal/domain"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

func (h *WebHandler) handleSettingsUsers(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	users, _ := h.db.ListUsers()
	activeLocalAdmins, _ := h.db.CountActiveLocalAdmins()

	data := map[string]any{
		"Title":             "Benutzerverwaltung",
		"CurrentTab":        "settings",
		"User":              user,
		"Users":             users,
		"ActiveLocalAdmins": activeLocalAdmins,
		"SuccessMsg":        r.URL.Query().Get("success"),
		"ErrorMsg":          r.URL.Query().Get("error"),
	}
	h.render(w, r, "settings_users.html", data)
}

func (h *WebHandler) handleSettingsLDAP(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	ldapCfg := h.ldapService.GetEffectiveConfig()

	data := map[string]any{
		"Title":           "LDAP / Active Directory",
		"CurrentTab":      "settings",
		"User":            user,
		"LDAPConfig":      ldapCfg,
		"HasBindPassword": ldapCfg.BindPassword != "",
		"SuccessMsg":      r.URL.Query().Get("success"),
		"ErrorMsg":        r.URL.Query().Get("error"),
	}
	h.render(w, r, "settings_ldap.html", data)
}

func (h *WebHandler) handleWebUserCreate(w http.ResponseWriter, r *http.Request) {
	authSource := strings.TrimSpace(r.FormValue("auth_source"))
	if authSource == "" {
		authSource = "local"
	}
	username := strings.TrimSpace(r.FormValue("username"))
	displayName := strings.TrimSpace(r.FormValue("display_name"))
	email := strings.TrimSpace(r.FormValue("email"))
	role := strings.TrimSpace(r.FormValue("role"))
	if role == "" {
		role = auth.RoleViewer
	}

	if username == "" {
		http.Redirect(w, r, "/settings/users?error="+url.QueryEscape("Benutzername darf nicht leer sein"), http.StatusSeeOther)
		return
	}

	var hash string
	if authSource == "local" {
		password := r.FormValue("password")
		if password == "" {
			http.Redirect(w, r, "/settings/users?error="+url.QueryEscape("Passwort für lokalen Benutzer erforderlich"), http.StatusSeeOther)
			return
		}
		var err error
		hash, err = auth.HashPassword(password)
		if err != nil {
			http.Redirect(w, r, "/settings/users?error="+url.QueryEscape("Passwort-Hash fehlgeschlagen"), http.StatusSeeOther)
			return
		}
	} else {
		hash = ""
		// If display name is empty, attempt to resolve via LDAP lookup
		if displayName == "" {
			if info, err := h.ldapService.LookupUser(username); err == nil && info != nil {
				displayName = info.DisplayName
				if email == "" {
					email = info.Email
				}
			}
		}
		if displayName == "" {
			displayName = username
		}
	}

	user := &domain.User{
		ID:           uuid.New().String(),
		Username:     username,
		PasswordHash: hash,
		DisplayName:  displayName,
		Email:        email,
		Role:         role,
		AuthSource:   authSource,
		IsActive:     true,
	}

	if err := h.db.CreateUser(user); err != nil {
		http.Redirect(w, r, "/settings/users?error="+url.QueryEscape("Fehler beim Anlegen: "+err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/settings/users?success="+url.QueryEscape("Benutzer erfolgreich angelegt."), http.StatusSeeOther)
}

func (h *WebHandler) handleWebUserUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	user, err := h.db.GetUserByID(id)
	if err != nil {
		http.Redirect(w, r, "/settings/users?error="+url.QueryEscape("Benutzer nicht gefunden"), http.StatusSeeOther)
		return
	}

	displayName := strings.TrimSpace(r.FormValue("display_name"))
	email := strings.TrimSpace(r.FormValue("email"))
	role := strings.TrimSpace(r.FormValue("role"))
	isActiveStr := r.FormValue("is_active")

	if displayName != "" {
		user.DisplayName = displayName
	}
	user.Email = email
	if role != "" {
		user.Role = role
	}
	if isActiveStr != "" {
		user.IsActive = (isActiveStr == "true" || isActiveStr == "1")
	}

	if err := h.db.UpdateUser(user); err != nil {
		if errors.Is(err, db.ErrLastAdminProtection) {
			http.Redirect(w, r, "/settings/users?error="+url.QueryEscape("Der letzte aktive lokale Administrator darf nicht deaktiviert oder herabgestuft werden, um ein Aussperren zu verhindern."), http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/settings/users?error="+url.QueryEscape("Fehler beim Aktualisieren: "+err.Error()), http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/settings/users?success="+url.QueryEscape("Benutzer erfolgreich aktualisiert."), http.StatusSeeOther)
}

func (h *WebHandler) handleWebUserDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.db.DeleteUser(id); err != nil {
		if errors.Is(err, db.ErrLastAdminProtection) {
			http.Redirect(w, r, "/settings/users?error="+url.QueryEscape("Der letzte aktive lokale Administrator darf nicht gelöscht werden, um ein Aussperren zu verhindern."), http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/settings/users?error="+url.QueryEscape("Fehler beim Löschen: "+err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/settings/users?success="+url.QueryEscape("Benutzer erfolgreich gelöscht."), http.StatusSeeOther)
}

func (h *WebHandler) handleWebUserPassword(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	password := r.FormValue("password")
	if password == "" {
		http.Redirect(w, r, "/settings/users?error="+url.QueryEscape("Passwort darf nicht leer sein"), http.StatusSeeOther)
		return
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		http.Redirect(w, r, "/settings/users?error="+url.QueryEscape("Passwort-Hash fehlgeschlagen"), http.StatusSeeOther)
		return
	}

	if err := h.db.UpdateUserPassword(id, hash); err != nil {
		http.Redirect(w, r, "/settings/users?error="+url.QueryEscape("Fehler beim Aktualisieren: "+err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/settings/users?success="+url.QueryEscape("Passwort erfolgreich aktualisiert."), http.StatusSeeOther)
}

func (h *WebHandler) handleWebUserLDAPLookup(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.URL.Query().Get("username"))
	w.Header().Set("Content-Type", "application/json")
	if username == "" {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "Bitte Loginnamen angeben"})
		return
	}

	info, err := h.ldapService.LookupUser(username)
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "user": info})
}

func (h *WebHandler) handleWebSettingsLDAP(w http.ResponseWriter, r *http.Request) {
	enabled := r.FormValue("ldap_enabled") == "true" || r.FormValue("ldap_enabled") == "on"
	proto := r.FormValue("ldap_protocol")
	useSSL := proto == "ldaps"
	host := strings.TrimSpace(r.FormValue("ldap_host"))
	portStr := strings.TrimSpace(r.FormValue("ldap_port"))
	insecure := r.FormValue("ldap_insecure_skip_verify") == "true" || r.FormValue("ldap_insecure_skip_verify") == "on"
	bindDN := strings.TrimSpace(r.FormValue("ldap_bind_dn"))
	bindPass := r.FormValue("ldap_bind_password")
	userBindTpl := strings.TrimSpace(r.FormValue("ldap_user_bind_template"))
	baseDN := strings.TrimSpace(r.FormValue("ldap_base_dn"))
	userFilter := strings.TrimSpace(r.FormValue("ldap_user_filter"))
	attrUser := strings.TrimSpace(r.FormValue("ldap_attr_username"))
	attrFirst := strings.TrimSpace(r.FormValue("ldap_attr_first_name"))
	attrLast := strings.TrimSpace(r.FormValue("ldap_attr_last_name"))
	attrMail := strings.TrimSpace(r.FormValue("ldap_attr_email"))

	if attrUser == "" {
		attrUser = "sAMAccountName"
	}
	if attrFirst == "" {
		attrFirst = "givenName"
	}
	if attrLast == "" {
		attrLast = "sn"
	}
	if attrMail == "" {
		attrMail = "mail"
	}

	if enabled {
		_ = h.db.SetSetting("ldap_enabled", "true")
	} else {
		_ = h.db.SetSetting("ldap_enabled", "false")
	}

	_ = h.db.SetSetting("ldap_host", host)
	_ = h.db.SetSetting("ldap_port", portStr)
	if useSSL {
		_ = h.db.SetSetting("ldap_use_ssl", "true")
	} else {
		_ = h.db.SetSetting("ldap_use_ssl", "false")
	}
	if insecure {
		_ = h.db.SetSetting("ldap_insecure_skip_verify", "true")
	} else {
		_ = h.db.SetSetting("ldap_insecure_skip_verify", "false")
	}

	_ = h.db.SetSetting("ldap_bind_dn", bindDN)
	if strings.TrimSpace(bindPass) != "" {
		_ = h.db.SetEncryptedSetting("ldap_bind_password", bindPass)
	}
	_ = h.db.SetSetting("ldap_user_bind_template", userBindTpl)
	_ = h.db.SetSetting("ldap_base_dn", baseDN)
	_ = h.db.SetSetting("ldap_user_filter", userFilter)
	_ = h.db.SetSetting("ldap_attr_username", attrUser)
	_ = h.db.SetSetting("ldap_attr_first_name", attrFirst)
	_ = h.db.SetSetting("ldap_attr_last_name", attrLast)
	_ = h.db.SetSetting("ldap_attr_email", attrMail)

	http.Redirect(w, r, "/settings/ldap?success="+url.QueryEscape("LDAP / Active Directory Einstellungen erfolgreich gespeichert."), http.StatusSeeOther)
}

func (h *WebHandler) handleWebSettingsLDAPTest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	proto := r.FormValue("ldap_protocol")
	useSSL := proto == "ldaps"
	host := strings.TrimSpace(r.FormValue("ldap_host"))
	portStr := strings.TrimSpace(r.FormValue("ldap_port"))
	port, _ := strconv.Atoi(portStr)
	if port <= 0 {
		if useSSL {
			port = 636
		} else {
			port = 389
		}
	}
	insecure := r.FormValue("ldap_insecure_skip_verify") == "true" || r.FormValue("ldap_insecure_skip_verify") == "on"
	bindDN := strings.TrimSpace(r.FormValue("ldap_bind_dn"))
	bindPass := r.FormValue("ldap_bind_password")
	if bindPass == "" {
		// Use existing password if not re-entered
		existingCfg := h.ldapService.GetEffectiveConfig()
		bindPass = existingCfg.BindPassword
	}
	baseDN := strings.TrimSpace(r.FormValue("ldap_base_dn"))

	testCfg := config.LDAPConfig{
		Host:               host,
		Port:               port,
		UseSSL:             useSSL,
		InsecureSkipVerify: insecure,
		BindDN:             bindDN,
		BindPassword:       bindPass,
		BaseDN:             baseDN,
	}

	if err := h.ldapService.TestConnection(testCfg); err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
		return
	}

	msg := fmt.Sprintf("Verbindung zu %s:%d erfolgreich aufgebaut", host, port)
	if bindDN != "" {
		msg += " und Service Account gebunden."
	} else {
		msg += " (TCP/TLS Handshake erfolgreich)."
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "message": msg})
}

func (h *WebHandler) handleWebSettingsLDAPTestUser(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	username := strings.TrimSpace(r.FormValue("test_username"))
	password := r.FormValue("test_password")

	if username == "" {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "Bitte Test-Benutzername eingeben"})
		return
	}

	var info *auth.LDAPUserInfo
	var err error

	if password != "" {
		// Test direct or service bind with password
		info, err = h.ldapService.AuthenticateLDAP(username, password)
	} else {
		// Lookup without password
		info, err = h.ldapService.LookupUser(username)
	}

	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "user": info})
}

func (h *WebHandler) handleWebTokenCreate(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	role := r.FormValue("role")

	fullToken, err := auth.GenerateAPIToken(40)
	if err != nil {
		http.Error(w, "Token-Generierung fehlgeschlagen", http.StatusInternalServerError)
		return
	}

	hash := sha256.Sum256([]byte(fullToken))
	tokenHash := hex.EncodeToString(hash[:])

	token := &domain.APIToken{
		ID:        uuid.New().String(),
		Name:      name,
		TokenHash: tokenHash,
		Role:      role,
	}

	if err := h.db.CreateAPIToken(token); err != nil {
		http.Error(w, "Fehler beim Anlegen: "+err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/settings/system?token="+url.QueryEscape(fullToken), http.StatusSeeOther)
}

func (h *WebHandler) handleWebTokenDelete(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("id")
	_ = h.db.DeleteAPIToken(id)
	http.Redirect(w, r, "/settings/system", http.StatusSeeOther)
}
