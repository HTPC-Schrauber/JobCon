package auth

import (
	"crypto/tls"
	"errors"
	"fmt"
	"jobcon/internal/config"
	"jobcon/internal/db"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
)

var (
	ErrLDAPDisabled    = errors.New("LDAP / Active Directory Authentifizierung ist deaktiviert")
	ErrLDAPUserNotFound = errors.New("Benutzer in LDAP / Active Directory nicht gefunden")
	ErrEmptyPassword   = errors.New("Passwort darf nicht leer sein")
)

type LDAPUserInfo struct {
	DN          string `json:"dn"`
	Username    string `json:"username"`
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
}

type LDAPClient interface {
	Bind(username, password string) error
	Search(searchRequest *ldap.SearchRequest) (*ldap.SearchResult, error)
	Close()
}

type LDAPService struct {
	db  *db.DB
	cfg *config.Config
}

func NewLDAPService(database *db.DB, cfg *config.Config) *LDAPService {
	return &LDAPService{
		db:  database,
		cfg: cfg,
	}
}

// GetEffectiveConfig returns the active LDAP configuration, reading SQLite settings with fallback to config.yaml
func (s *LDAPService) GetEffectiveConfig() config.LDAPConfig {
	var baseCfg config.LDAPConfig
	if s.cfg != nil {
		baseCfg = s.cfg.Auth.LDAP
	}
	return GetEffectiveLDAPConfig(s.db, baseCfg)
}

// GetEffectiveLDAPConfig merges SQLite settings on top of baseCfg
func GetEffectiveLDAPConfig(database *db.DB, baseCfg config.LDAPConfig) config.LDAPConfig {
	cfg := baseCfg
	if database == nil {
		return normalizeLDAPConfig(cfg)
	}

	if val, err := database.GetSetting("ldap_enabled", ""); err == nil && val != "" {
		cfg.Enabled = (val == "true" || val == "1")
	}
	if val, err := database.GetSetting("ldap_host", ""); err == nil && val != "" {
		cfg.Host = val
	}
	if val, err := database.GetSetting("ldap_port", ""); err == nil && val != "" {
		if p, err := strconv.Atoi(val); err == nil && p > 0 {
			cfg.Port = p
		}
	}
	if val, err := database.GetSetting("ldap_use_ssl", ""); err == nil && val != "" {
		cfg.UseSSL = (val == "true" || val == "1")
	}
	if val, err := database.GetSetting("ldap_insecure_skip_verify", ""); err == nil && val != "" {
		cfg.InsecureSkipVerify = (val == "true" || val == "1")
	}
	if val, err := database.GetSetting("ldap_bind_dn", ""); err == nil {
		cfg.BindDN = val
	}
	if val, err := database.GetEncryptedSetting("ldap_bind_password", ""); err == nil && val != "" {
		cfg.BindPassword = val
	}
	if val, err := database.GetSetting("ldap_user_bind_template", ""); err == nil {
		cfg.UserBindTemplate = val
	}
	if val, err := database.GetSetting("ldap_base_dn", ""); err == nil {
		cfg.BaseDN = val
	}
	if val, err := database.GetSetting("ldap_user_filter", ""); err == nil {
		cfg.UserFilter = val
	}
	if val, err := database.GetSetting("ldap_attr_username", ""); err == nil && val != "" {
		cfg.AttrUsername = val
	}
	if val, err := database.GetSetting("ldap_attr_first_name", ""); err == nil && val != "" {
		cfg.AttrFirstName = val
	}
	if val, err := database.GetSetting("ldap_attr_last_name", ""); err == nil && val != "" {
		cfg.AttrLastName = val
	}
	if val, err := database.GetSetting("ldap_attr_email", ""); err == nil && val != "" {
		cfg.AttrEmail = val
	}

	return normalizeLDAPConfig(cfg)
}

func normalizeLDAPConfig(cfg config.LDAPConfig) config.LDAPConfig {
	if cfg.AttrUsername == "" {
		cfg.AttrUsername = "sAMAccountName"
	}
	if cfg.AttrFirstName == "" {
		cfg.AttrFirstName = "givenName"
	}
	if cfg.AttrLastName == "" {
		cfg.AttrLastName = "sn"
	}
	if cfg.AttrEmail == "" {
		cfg.AttrEmail = "mail"
	}
	if cfg.Port == 0 {
		if cfg.UseSSL {
			cfg.Port = 636
		} else {
			cfg.Port = 389
		}
	}
	return cfg
}

// Dial connects to the LDAP server according to configuration
func (s *LDAPService) Dial(cfg config.LDAPConfig) (*ldap.Conn, error) {
	if strings.TrimSpace(cfg.Host) == "" {
		return nil, errors.New("kein LDAP-Host konfiguriert")
	}

	port := cfg.Port
	if port <= 0 {
		if cfg.UseSSL {
			port = 636
		} else {
			port = 389
		}
	}

	addr := fmt.Sprintf("%s:%d", cfg.Host, port)
	timeout := 10 * time.Second

	dialer := &net.Dialer{
		Timeout: timeout,
	}

	if cfg.UseSSL {
		tlsConfig := &tls.Config{
			ServerName:         cfg.Host,
			InsecureSkipVerify: cfg.InsecureSkipVerify,
		}
		return ldap.DialURL(fmt.Sprintf("ldaps://%s", addr), ldap.DialWithTLSConfig(tlsConfig), ldap.DialWithDialer(dialer))
	}

	return ldap.DialURL(fmt.Sprintf("ldap://%s", addr), ldap.DialWithDialer(dialer))
}

// BuildUserBindIdentity resolves the identity to use when authenticating directly as the user without a bind user
func BuildUserBindIdentity(username string, cfg config.LDAPConfig) string {
	cleanUsername := strings.TrimSpace(username)

	// If username already contains an @ (UPN format like user@domain.local) or \ (DOMAIN\user), use as is
	if strings.Contains(cleanUsername, "@") || strings.Contains(cleanUsername, `\`) {
		return cleanUsername
	}

	// 1. If explicit template is configured (e.g. "%s@domain.com" or "uid=%s,ou=users,dc=...")
	if cfg.UserBindTemplate != "" {
		if strings.Contains(cfg.UserBindTemplate, "%s") {
			return fmt.Sprintf(cfg.UserBindTemplate, cleanUsername)
		}
		return fmt.Sprintf("%s@%s", cleanUsername, cfg.UserBindTemplate)
	}

	// 2. Try to derive UPN suffix from BaseDN (e.g. "OU=Users,DC=intern,DC=firma,DC=de" -> "intern.firma.de")
	if cfg.BaseDN != "" {
		parts := strings.Split(cfg.BaseDN, ",")
		var dcParts []string
		for _, p := range parts {
			trimmed := strings.TrimSpace(p)
			if strings.HasPrefix(strings.ToLower(trimmed), "dc=") {
				dcParts = append(dcParts, trimmed[3:])
			}
		}
		if len(dcParts) > 0 {
			domain := strings.Join(dcParts, ".")
			return fmt.Sprintf("%s@%s", cleanUsername, domain)
		}
	}

	return cleanUsername
}

// AuthenticateLDAP attempts to authenticate the user against LDAP/AD.
// Supports both Direct User Bind (without service account) and Service Account Bind.
func (s *LDAPService) AuthenticateLDAP(username, password string) (*LDAPUserInfo, error) {
	if strings.TrimSpace(password) == "" {
		return nil, ErrEmptyPassword
	}

	cfg := s.GetEffectiveConfig()
	if !cfg.Enabled {
		return nil, ErrLDAPDisabled
	}

	conn, err := s.Dial(cfg)
	if err != nil {
		return nil, fmt.Errorf("LDAP-Verbindung fehlgeschlagen: %w", err)
	}
	defer conn.Close()

	var userInfo *LDAPUserInfo

	// Mode 1: Extra Bind User is configured
	if strings.TrimSpace(cfg.BindDN) != "" {
		if err := conn.Bind(cfg.BindDN, cfg.BindPassword); err != nil {
			return nil, fmt.Errorf("LDAP Service-Bind fehlgeschlagen: %w", err)
		}

		userEntry, err := s.searchUser(conn, username, cfg)
		if err != nil {
			return nil, err
		}

		// Verify user credentials by binding as the user's DN
		if err := conn.Bind(userEntry.DN, password); err != nil {
			return nil, ErrInvalidCredentials
		}

		userInfo = s.extractUserInfo(userEntry, username, cfg)
		return userInfo, nil
	}

	// Mode 2: Direct User Bind (No extra bind user required!)
	bindIdentity := BuildUserBindIdentity(username, cfg)
	if err := conn.Bind(bindIdentity, password); err != nil {
		return nil, ErrInvalidCredentials
	}

	// Once authenticated directly as the user, attempt to read user's attributes
	userEntry, err := s.searchUser(conn, username, cfg)
	if err == nil && userEntry != nil {
		userInfo = s.extractUserInfo(userEntry, username, cfg)
	} else {
		// Even if directory search is restricted, authentication was successful!
		userInfo = &LDAPUserInfo{
			DN:          bindIdentity,
			Username:    username,
			DisplayName: username,
		}
	}

	return userInfo, nil
}

// LookupUser searches for a user in LDAP/AD and returns their attributes without authenticating
func (s *LDAPService) LookupUser(username string) (*LDAPUserInfo, error) {
	cfg := s.GetEffectiveConfig()
	if !cfg.Enabled {
		return nil, ErrLDAPDisabled
	}

	conn, err := s.Dial(cfg)
	if err != nil {
		return nil, fmt.Errorf("LDAP-Verbindung fehlgeschlagen: %w", err)
	}
	defer conn.Close()

	if strings.TrimSpace(cfg.BindDN) != "" {
		if err := conn.Bind(cfg.BindDN, cfg.BindPassword); err != nil {
			return nil, fmt.Errorf("LDAP Service-Bind fehlgeschlagen: %w", err)
		}
	} else {
		// Attempt unauthenticated bind for lookup if permitted by directory
		_ = conn.UnauthenticatedBind("")
	}

	entry, err := s.searchUser(conn, username, cfg)
	if err != nil {
		return nil, err
	}

	return s.extractUserInfo(entry, username, cfg), nil
}

// TestConnection tests TCP/TLS connection and optional bind/search
func (s *LDAPService) TestConnection(cfg config.LDAPConfig) error {
	cfg = normalizeLDAPConfig(cfg)
	conn, err := s.Dial(cfg)
	if err != nil {
		return fmt.Errorf("Verbindung zu %s:%d fehlgeschlagen: %w", cfg.Host, cfg.Port, err)
	}
	defer conn.Close()

	if strings.TrimSpace(cfg.BindDN) != "" {
		if err := conn.Bind(cfg.BindDN, cfg.BindPassword); err != nil {
			return fmt.Errorf("Service-Bind für %q fehlgeschlagen: %w", cfg.BindDN, err)
		}

		if strings.TrimSpace(cfg.BaseDN) != "" {
			searchReq := ldap.NewSearchRequest(
				cfg.BaseDN,
				ldap.ScopeBaseObject, ldap.NeverDerefAliases, 0, 3, false,
				"(objectClass=*)",
				[]string{"dn"},
				nil,
			)
			if _, err := conn.Search(searchReq); err != nil {
				return fmt.Errorf("Suchbasis %q nicht lesbar: %w", cfg.BaseDN, err)
			}
		}
	}

	return nil
}

func (s *LDAPService) searchUser(conn *ldap.Conn, username string, cfg config.LDAPConfig) (*ldap.Entry, error) {
	if strings.TrimSpace(cfg.BaseDN) == "" {
		return nil, errors.New("keine Suchbasis (Base DN) konfiguriert")
	}

	escapedUser := ldap.EscapeFilter(username)
	attrUser := cfg.AttrUsername
	if attrUser == "" {
		attrUser = "sAMAccountName"
	}

	var filter string
	if strings.TrimSpace(cfg.UserFilter) != "" && strings.Contains(cfg.UserFilter, "%s") {
		filter = fmt.Sprintf(cfg.UserFilter, escapedUser)
	} else {
		filter = fmt.Sprintf("(%s=%s)", ldap.EscapeFilter(attrUser), escapedUser)
	}

	attributes := []string{
		"dn",
		attrUser,
		cfg.AttrFirstName,
		cfg.AttrLastName,
		cfg.AttrEmail,
		"displayName",
		"mail",
		"userPrincipalName",
	}

	searchReq := ldap.NewSearchRequest(
		cfg.BaseDN,
		ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 5, false,
		filter,
		attributes,
		nil,
	)

	sr, err := conn.Search(searchReq)
	if err != nil {
		return nil, fmt.Errorf("LDAP-Suche fehlgeschlagen: %w", err)
	}

	if len(sr.Entries) == 0 {
		return nil, ErrLDAPUserNotFound
	}
	if len(sr.Entries) > 1 {
		return nil, fmt.Errorf("mehrere Benutzer (%d) für %q in LDAP gefunden", len(sr.Entries), username)
	}

	return sr.Entries[0], nil
}

func (s *LDAPService) extractUserInfo(entry *ldap.Entry, fallbackUsername string, cfg config.LDAPConfig) *LDAPUserInfo {
	attrUser := cfg.AttrUsername
	if attrUser == "" {
		attrUser = "sAMAccountName"
	}
	username := entry.GetAttributeValue(attrUser)
	if username == "" {
		username = fallbackUsername
	}

	attrFirst := cfg.AttrFirstName
	if attrFirst == "" {
		attrFirst = "givenName"
	}
	firstName := entry.GetAttributeValue(attrFirst)

	attrLast := cfg.AttrLastName
	if attrLast == "" {
		attrLast = "sn"
	}
	lastName := entry.GetAttributeValue(attrLast)

	attrMail := cfg.AttrEmail
	if attrMail == "" {
		attrMail = "mail"
	}
	email := entry.GetAttributeValue(attrMail)
	if email == "" {
		email = entry.GetAttributeValue("mail")
	}

	var displayName string
	if firstName != "" && lastName != "" {
		displayName = fmt.Sprintf("%s %s", firstName, lastName)
	} else if firstName != "" {
		displayName = firstName
	} else if lastName != "" {
		displayName = lastName
	} else if dnAttr := entry.GetAttributeValue("displayName"); dnAttr != "" {
		displayName = dnAttr
	} else {
		displayName = username
	}

	return &LDAPUserInfo{
		DN:          entry.DN,
		Username:    username,
		FirstName:   firstName,
		LastName:    lastName,
		Email:       email,
		DisplayName: displayName,
	}
}
